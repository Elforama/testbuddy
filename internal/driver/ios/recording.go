package ios

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/elforama/testbuddy/internal/tether"
)

var recordStarted = 5 * time.Second

var recordStopWait = 10 * time.Second

func (d *Driver) StartRecording(ctx context.Context, path string) error {
	d.mu.Lock()
	busy := d.recording
	if !busy {
		d.recording, d.recordStarting = true, make(chan struct{})
	}
	d.mu.Unlock()
	if busy {
		return errors.New("already recording")
	}
	ok := false
	defer func() {
		d.mu.Lock()
		if !ok {
			d.recording = false
		}
		close(d.recordStarting)
		d.recordStarting = nil
		d.mu.Unlock()
	}()
	p, err := tether.Start([]string{"xcrun", "simctl", "io", d.info.Serial, "recordVideo", "--codec=h264", "--force", path}, nil)
	if err != nil {
		return err
	}
	started := make(chan struct{})
	read := make(chan []string, 1)
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
			if !once && strings.Contains(sc.Text(), "Recording started") {
				once = true
				t0 = time.Now()
				close(started)
			}
		}
		read <- said
	}()
	exited := make(chan error, 1)
	go func() { exited <- p.Wait() }()
	select {
	case <-started:
	case <-exited:
		var said []string
		select {
		case said = <-read:
		case <-time.After(time.Second):
		}
		return fmt.Errorf("simctl recordVideo stopped at once: %s", strings.Join(said, "; "))
	case <-time.After(recordStarted):
		d.note(fmt.Sprintf("ios: simctl recordVideo did not confirm it started within %s; recording on, the video may miss the start", recordStarted))
	case <-ctx.Done():
		p.Kill()
		<-exited
		return ctx.Err()
	}
	ok = true
	d.mu.Lock()
	d.recordProc, d.recordPath, d.recordExited = p, path, exited
	d.recordT0 = time.Time{}
	select {
	case <-started:
		d.recordT0 = t0
	default:
	}
	d.mu.Unlock()
	d.mu.Lock()
	d.recordMarkHeld = 0
	d.mu.Unlock()
	if mark, held := d.markRecordingStart(ctx); !mark.IsZero() {
		d.mu.Lock()
		d.recordT0, d.recordMarkHeld = mark, held
		d.mu.Unlock()
	}
	return nil
}

const statusBarHold = 300 * time.Millisecond

func (d *Driver) markRecordingStart(ctx context.Context) (time.Time, time.Duration) {
	bound := func() (context.Context, context.CancelFunc) { return context.WithTimeout(ctx, 5*time.Second) }
	c, cancel := bound()
	list, err := d.simctl(c, "status_bar", d.info.Serial, "list")
	cancel()
	if err != nil || hasStatusBarOverrides(list) {
		return time.Time{}, 0
	}
	c, cancel = bound()
	_, err = d.simctl(c, "status_bar", d.info.Serial, "override", "--time", time.Now().Add(time.Minute).Format("3:04"))
	shown := time.Now()
	cancel()
	if err != nil {
		return time.Time{}, 0
	}
	time.Sleep(statusBarHold)
	c, cancel = bound()
	_, err = d.simctl(c, "status_bar", d.info.Serial, "clear")
	cleared := time.Now()
	cancel()
	if err != nil {
		return shown, 0
	}
	return shown, cleared.Sub(shown)
}

func (d *Driver) RecordingMark() (time.Time, time.Duration, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.recordT0, d.recordMarkHeld, d.recordMarkHeld > 0 && !d.recordT0.IsZero()
}

func hasStatusBarOverrides(list string) bool {
	for _, l := range strings.Split(list, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(l), ":"); ok && k != "" && strings.TrimSpace(v) != "" {
			return true
		}
	}
	return false
}

func (d *Driver) endRecording() {
	d.mu.Lock()
	starting := d.recordStarting
	d.mu.Unlock()
	if starting != nil {
		<-starting
	}
	d.StopRecording(context.Background())
}

func (d *Driver) RecordingStarted() time.Time {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.recordT0
}

func (d *Driver) StopRecording(ctx context.Context) error {
	d.mu.Lock()
	p, path, done := d.recordProc, d.recordPath, d.recordExited
	d.recordProc = nil
	d.mu.Unlock()
	if p == nil {
		return nil
	}
	defer func() {
		d.mu.Lock()
		d.recording = false
		d.mu.Unlock()
	}()
	p.Interrupt()
	select {
	case <-done:
	case <-time.After(recordStopWait):
		p.Kill()
		<-done
		return errors.New("simctl recordVideo did not stop")
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("no recording at %s: %w", path, err)
	}
	return nil
}
