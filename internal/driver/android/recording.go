package android

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/driver"
)

var _ driver.RecordingParts = (*Driver)(nil)

// Recordings are made on the device as /sdcard/tb-recording-N.mp4 and copied at the stop.
const (
	recordRemote = "/sdcard/tb-recording"
	recordStop   = "/sdcard/tb-recording.stop"
	recordRate   = "4000000"
)

// recordStarted bounds the wait for screenrecord to start encoding (it says so on its
// verbose output); a recording slower to start still goes on.
var recordStarted = 5 * time.Second

// recordCleanupWait bounds each of StopRecording's cleanup commands (stopping screenrecord,
// removing its files), which run even when the caller's context is cancelled (issue #202).
const recordCleanupWait = 10 * time.Second

// recordScript is the device shell command that records: one unlimited recording where
// screenrecord takes --time-limit 0, else 3-minute parts, one after another, until the
// stop file appears.
func recordScript(unlimited bool) string {
	if unlimited {
		return fmt.Sprintf("screenrecord --verbose --time-limit 0 --bit-rate %s %s-1.mp4", recordRate, recordRemote)
	}
	return fmt.Sprintf("i=1; while [ ! -f %s ]; do screenrecord --verbose --bit-rate %s %s-$i.mp4 || break; i=$((i+1)); done",
		recordStop, recordRate, recordRemote)
}

// partPath is where part n (2, 3, ...) of a recording to path is saved.
func partPath(path string, n int) string {
	ext := filepath.Ext(path)
	return fmt.Sprintf("%s-part%d%s", strings.TrimSuffix(path, ext), n, ext)
}

// remoteParts orders the device's recording files by part number.
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

// unlimitedRecording asks the device once whether screenrecord can record without its
// 3-minute cap.
func (d *Driver) unlimitedRecording(ctx context.Context) bool {
	if d.recordAny == nil {
		out, _, _ := d.Sh(ctx, "screenrecord --help 2>&1")
		ok := strings.Contains(out, "Set to 0")
		d.recordAny = &ok
	}
	return *d.recordAny
}

// StartRecording records the screen until StopRecording, which saves it to path. It
// returns once the recording runs, so the screen from now on is in it.
func (d *Driver) StartRecording(ctx context.Context, path string) error {
	if d.recordCmd != nil {
		return errors.New("already recording")
	}
	d.Sh(ctx, fmt.Sprintf("rm -f %s-*.mp4 %s", recordRemote, recordStop))
	cmd := exec.Command(device.ADB(), "-s", d.info.Serial, "shell", recordScript(d.unlimitedRecording(ctx)))
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return err
	}
	started := make(chan struct{})
	ended := make(chan []string, 1)
	go func() {
		var said []string
		once := false
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			said = append(said, sc.Text())
			if len(said) > 20 {
				said = said[1:]
			}
			if !once && strings.HasPrefix(sc.Text(), "Content area") {
				once = true
				close(started)
			}
		}
		ended <- said
	}()
	select {
	case <-started:
	case said := <-ended:
		select {
		case <-started: // it started, then ended (a cap, a crash): StopRecording keeps what it saved
		default:
			cmd.Wait()
			return fmt.Errorf("screenrecord stopped at once: %s", strings.Join(said, "; "))
		}
	case <-time.After(recordStarted):
	case <-ctx.Done():
		cmd.Process.Kill()
		cmd.Wait()
		return ctx.Err()
	}
	d.recordCmd, d.recordPath, d.recordParts = cmd, path, nil
	return nil
}

// StopRecording ends the recording and copies it to the path StartRecording was given (a
// recording longer than screenrecord's cap goes on in part files next to it).
func (d *Driver) StopRecording(ctx context.Context) error {
	if d.recordCmd == nil {
		return nil
	}
	cmd := d.recordCmd
	d.recordCmd = nil
	// Stopping screenrecord and removing its files are cleanup: they run even when ctx is
	// cancelled, which no longer sends an agent request (issue #202). Only keeping the video
	// (listing and pulling it) ends with ctx.
	cleanup := func(sh string) {
		c, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordCleanupWait)
		defer cancel()
		d.Sh(c, sh)
	}
	// The stop file first: the parts loop must not start another part after the interrupt.
	cleanup(fmt.Sprintf("touch %s; pkill -INT screenrecord", recordStop))
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		cmd.Process.Kill()
	}
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

// RecordingParts are the files the last recording was saved as, in order.
func (d *Driver) RecordingParts() []string { return append([]string(nil), d.recordParts...) }
