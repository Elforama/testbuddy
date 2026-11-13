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

// recordStarted bounds the wait for simctl to say the recording started; a recording
// slower to start still goes on.
var recordStarted = 5 * time.Second

// recordStopWait bounds the wait for simctl to write the file out once interrupted.
var recordStopWait = 10 * time.Second

// StartRecording records the simulator's screen to path until StopRecording. It returns
// once simctl says the recording started, so the screen from now on is in it.
//
// simctl runs under a watchdog (internal/tether): when tb is gone, killed with SIGKILL
// included, the watchdog interrupts it (it writes the file out) instead of leaving it recording
// until the simulator shuts down (issue #52).
func (d *Driver) StartRecording(ctx context.Context, path string) error {
	// The check and the claim under one lock: two starts cannot both record (issue #52).
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
		close(d.recordStarting) // Close waits for it
		d.recordStarting = nil
		d.mu.Unlock()
	}()
	p, err := tether.Start([]string{"xcrun", "simctl", "io", d.info.Serial, "recordVideo", "--codec=h264", "--force", path}, nil)
	if err != nil {
		return err
	}
	started := make(chan struct{})
	read := make(chan []string, 1) // what simctl said, once its output ended
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
		case <-time.After(time.Second): // something it started still holds its output
		}
		return fmt.Errorf("simctl recordVideo stopped at once: %s", strings.Join(said, "; "))
	case <-time.After(recordStarted):
		// Recording on: it may only be slow to start. The trace says so (issue #52).
		d.note(fmt.Sprintf("ios: simctl recordVideo did not confirm it started within %s; recording on, the video may miss the start", recordStarted))
	case <-ctx.Done():
		p.Kill()
		<-exited
		return ctx.Err()
	}
	ok = true
	d.mu.Lock()
	d.recordProc, d.recordPath, d.recordExited = p, path, exited
	d.mu.Unlock()
	return nil
}

// endRecording stops a recording still running when the driver closes (issue #52), simctl
// writing its file out. A start still in progress is waited for first (it is bounded: the
// watchdog's answer, then recordStarted), so the recording it makes is stopped too.
func (d *Driver) endRecording() {
	d.mu.Lock()
	starting := d.recordStarting
	d.mu.Unlock()
	if starting != nil {
		<-starting
	}
	d.StopRecording(context.Background())
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
	p.Interrupt() // simctl finishes the file on SIGINT
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
