package ios

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// recordStarted bounds the wait for simctl to say the recording started; a recording
// slower to start still goes on.
var recordStarted = 5 * time.Second

// StartRecording records the simulator's screen to path until StopRecording. It returns
// once simctl says the recording started, so the screen from now on is in it.
func (d *Driver) StartRecording(ctx context.Context, path string) error {
	d.mu.Lock()
	busy := d.recordCmd != nil
	d.mu.Unlock()
	if busy {
		return errors.New("already recording")
	}
	cmd := exec.Command("xcrun", "simctl", "io", d.info.Serial, "recordVideo", "--codec=h264", "--force", path)
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		return err
	}
	started := make(chan struct{})
	var said bytes.Buffer
	var saidMu sync.Mutex
	go func() {
		once := false
		sc := bufio.NewScanner(pr)
		for sc.Scan() {
			saidMu.Lock()
			said.WriteString(sc.Text() + "; ")
			saidMu.Unlock()
			if !once && strings.Contains(sc.Text(), "Recording started") {
				once = true
				close(started)
			}
		}
	}()
	exited := make(chan error, 1)
	go func() { err := cmd.Wait(); pw.Close(); exited <- err }()
	select {
	case <-started:
	case <-exited:
		saidMu.Lock()
		defer saidMu.Unlock()
		return fmt.Errorf("simctl recordVideo stopped at once: %s", strings.TrimSuffix(said.String(), "; "))
	case <-time.After(recordStarted):
	case <-ctx.Done():
		cmd.Process.Kill()
		<-exited
		return ctx.Err()
	}
	d.mu.Lock()
	d.recordCmd, d.recordPath, d.recordExited = cmd, path, exited
	d.mu.Unlock()
	return nil
}

func (d *Driver) StopRecording(ctx context.Context) error {
	d.mu.Lock()
	cmd := d.recordCmd
	d.recordCmd = nil
	d.mu.Unlock()
	if cmd == nil {
		return nil
	}
	cmd.Process.Signal(os.Interrupt) // simctl finishes the file on SIGINT
	d.mu.Lock()
	done := d.recordExited
	d.mu.Unlock()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		cmd.Process.Kill()
		return errors.New("simctl recordVideo did not stop")
	}
	if _, err := os.Stat(d.recordPath); err != nil {
		return fmt.Errorf("no recording at %s: %w", d.recordPath, err)
	}
	return nil
}
