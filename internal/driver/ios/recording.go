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
	var t0 time.Time               // set before started closes
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

// statusBarHold is how long markRecordingStart shows its status bar time: a few frames.
const statusBarHold = 300 * time.Millisecond

// markRecordingStart marks the video's start, and returns when the mark showed and how long
// it stayed (zero when it could not mark).
//
// simctl writes a frame only when the screen changes, so the video of a still screen begins at
// the first change after "Recording started": in CI one began 43 s late, while a launch waited,
// and everything --annotate drew from tb's clock came 43 s behind the picture. So right after the
// start the driver changes the status bar's clock for a moment (the next minute) and clears it
// again: the video begins at that change. A simulator whose status bar has overrides of its own
// (screenshots at 9:41) is left as it is: clearing would drop them.
//
// The video does not always begin at the mark (CI, PR #393: it showed 2.4 s in, the video
// beginning earlier), so --annotate looks for it in the video (driver.RecordingMark): shown is
// when the override returned, the status bar changed by then.
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

// RecordingMark is the last recording's start mark (driver.RecordingMark).
func (d *Driver) RecordingMark() (time.Time, time.Duration, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.recordT0, d.recordMarkHeld, d.recordMarkHeld > 0 && !d.recordT0.IsZero()
}

// hasStatusBarOverrides reads `simctl status_bar list`: an override is a "Name: value" line
// (the heading "Current Status Bar Overrides:" has nothing after its colon).
func hasStatusBarOverrides(list string) bool {
	for _, l := range strings.Split(list, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(l), ":"); ok && k != "" && strings.TrimSpace(v) != "" {
			return true
		}
	}
	return false
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

// RecordingStarted is when the last recording's video begins: the status bar change that
// markRecordingStart made, else when simctl said it started ("Recording started", read as it
// came). Zero when neither is known.
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
