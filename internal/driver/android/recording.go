package android

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/elforama/testbuddy/internal/annotate"
	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/tether"
)

var (
	_ driver.RecordingParts = (*Driver)(nil)
	_ driver.RecordingClock = (*Driver)(nil)
)

func (d *Driver) RecordingStarted() time.Time { return d.recordT0 }

const (
	recordRemote = "/sdcard/tb-recording"
	recordStop   = "/sdcard/tb-recording.stop"
	recordRate   = "4000000"
)

const recordStopSh = "touch " + recordStop + "; pkill -INT -f '^[^ ]*screenrecord .*/sdcard/tb-recordin[g]'"

const recordLeftoversSh = `pkill -f 'tb-recordin[g]-\$i\.mp4'; pkill -f '^[^ ]*screenrecord .*/sdcard/tb-recordin[g]'; ` +
	"rm -f " + recordRemote + "-*.mp4 " + recordStop

var recordStarted = 5 * time.Second

const recordCleanupWait = 10 * time.Second

var recordStopWait = 10 * time.Second

func recordScript(unlimited bool) string {
	if unlimited {
		return fmt.Sprintf("screenrecord --verbose --time-limit 0 --bit-rate %s %s-1.mp4", recordRate, recordRemote)
	}
	return fmt.Sprintf("i=1; while [ ! -f %s ]; do screenrecord --verbose --bit-rate %s %s-$i.mp4 || break; i=$((i+1)); done",
		recordStop, recordRate, recordRemote)
}

func partPath(path string, n int) string {
	ext := filepath.Ext(path)
	return fmt.Sprintf("%s-part%d%s", strings.TrimSuffix(path, ext), n, ext)
}

func remoteParts(ls string) []string {
	type part struct {
		n    int
		name string
	}
	var parts []part
	for _, f := range strings.Fields(ls) {
		base := filepath.Base(f)
		num := strings.TrimSuffix(strings.TrimPrefix(base, filepath.Base(recordRemote)+"-"), ".mp4")
		if n, err := strconv.Atoi(num); err == nil && strings.HasSuffix(base, ".mp4") {
			parts = append(parts, part{n, f})
		}
	}
	sort.Slice(parts, func(i, j int) bool { return parts[i].n < parts[j].n })
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = p.name
	}
	return out
}

func (d *Driver) unlimitedRecording(ctx context.Context) bool {
	if d.recordAny == nil {
		out, _, err := d.Sh(ctx, "screenrecord --help 2>&1")
		ok := strings.Contains(out, "Set to 0")
		if err != nil {
			return ok
		}
		d.recordAny = &ok
	}
	return *d.recordAny
}

func (d *Driver) StartRecording(ctx context.Context, path string) error {
	if d.recordCmd != nil {
		return errors.New("already recording")
	}
	d.Sh(ctx, recordLeftoversSh)
	adb := device.ADB()
	p, err := tether.Start([]string{adb, "-s", d.info.Serial, "shell", recordScript(d.unlimitedRecording(ctx))},
		[][]string{{adb, "-s", d.info.Serial, "shell", recordStopSh}})
	if err != nil {
		return err
	}
	started := make(chan struct{})
	ended := make(chan []string, 1)
	var t0 time.Time
	go func() {
		var said []string
		once := false
		sc := bufio.NewScanner(p.Out)
		for sc.Scan() {
			said = append(said, sc.Text())
			if len(said) > 20 {
				said = said[1:]
			}
			if !once && strings.HasPrefix(sc.Text(), "Content area") {
				once = true
				t0 = time.Now()
				close(started)
			}
		}
		ended <- said
	}()
	d.recordT0 = time.Time{}
	select {
	case <-started:
		d.recordT0 = t0
	case said := <-ended:
		select {
		case <-started:
			d.recordT0 = t0
		default:
			p.Wait()
			return fmt.Errorf("screenrecord stopped at once: %s", strings.Join(said, "; "))
		}
	case <-time.After(recordStarted):
		d.note(fmt.Sprintf("android: screenrecord did not confirm it started within %s; recording on, the video may miss the start", recordStarted))
	case <-ctx.Done():
		d.stopOnDevice(ctx)
		d.waitRecorder(ctx, p)
		d.removeOnDevice(ctx)
		return ctx.Err()
	}
	d.recordCmd, d.recordPath, d.recordParts = p, path, nil
	return nil
}

const clockSamples = 4

func (d *Driver) measureClock(ctx context.Context) (time.Duration, bool) {
	best, ahead, ok := time.Duration(-1), time.Duration(0), false
	for range clockSamples {
		c, cancel := context.WithTimeout(ctx, 3*time.Second)
		sent := time.Now()
		out, _, err := d.Sh(c, "date +%s%N")
		got := time.Now()
		cancel()
		if err != nil {
			continue
		}
		dev, ok2 := parseDeviceNanos(out)
		if !ok2 {
			continue
		}
		if rtt := got.Sub(sent); best < 0 || rtt < best {
			best, ahead, ok = rtt, dev.Sub(sent.Add(rtt/2)), true
		}
	}
	return ahead, ok
}

func parseDeviceNanos(out string) (time.Time, bool) {
	s := strings.TrimSpace(out)
	if len(s) < 19 {
		return time.Time{}, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	t := time.Unix(0, n)
	return t, t.Year() >= 2000
}

func (d *Driver) VideoStarted(ctx context.Context, path string) (time.Time, bool) {
	t, err := annotate.FirstFrame(path)
	if err != nil {
		return time.Time{}, false
	}
	if !d.clockKnown {
		d.clockAhead, d.clockKnown = d.measureClock(ctx)
		if !d.clockKnown {
			return time.Time{}, false
		}
	}
	return t.Add(-d.clockAhead), true
}

func (d *Driver) StopRecording(ctx context.Context) error {
	if d.recordCmd == nil {
		return nil
	}
	cmd := d.recordCmd
	d.recordCmd = nil
	cleanup := func(sh string) {
		c, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordCleanupWait)
		defer cancel()
		d.Sh(c, sh)
	}
	cleanup(recordStopSh)
	d.waitRecorder(ctx, cmd)
	defer cleanup(fmt.Sprintf("rm -f %s-*.mp4 %s", recordRemote, recordStop))
	if err := ctx.Err(); err != nil {
		return err
	}
	ls, _, err := d.Sh(ctx, fmt.Sprintf("ls %s-*.mp4 2>/dev/null", recordRemote))
	if err != nil && ctx.Err() != nil {
		return err
	}
	parts := remoteParts(ls)
	if len(parts) == 0 {
		return errors.New("screenrecord saved no recording")
	}
	var saved []string
	for i, remote := range parts {
		local := d.recordPath
		if i > 0 {
			local = partPath(d.recordPath, i+1)
		}
		if _, err := d.adb.Run(ctx, "pull", remote, local); err != nil {
			return err
		}
		saved = append(saved, local)
	}
	d.recordParts = saved
	return nil
}

func (d *Driver) stopOnDevice(ctx context.Context) {
	c, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordCleanupWait)
	defer cancel()
	d.adb.Run(c, "shell", recordStopSh)
}

func (d *Driver) waitRecorder(ctx context.Context, p *tether.Proc) {
	done := make(chan error, 1)
	go func() { done <- p.Wait() }()
	select {
	case <-done:
	case <-time.After(recordStopWait):
		d.stopOnDevice(ctx)
		p.Kill()
		<-done
	}
}

func (d *Driver) endRecording(ctx context.Context) {
	if d.recordCmd == nil {
		return
	}
	cmd := d.recordCmd
	d.recordCmd = nil
	d.stopOnDevice(ctx)
	d.waitRecorder(ctx, cmd)
	d.removeOnDevice(ctx)
}

func (d *Driver) removeOnDevice(ctx context.Context) {
	c, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordCleanupWait)
	defer cancel()
	d.adb.Run(c, "shell", fmt.Sprintf("rm -f %s-*.mp4 %s", recordRemote, recordStop))
}

func (d *Driver) RecordingParts() []string { return append([]string(nil), d.recordParts...) }
