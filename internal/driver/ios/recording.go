package ios

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"
)

func (d *Driver) StartRecording(ctx context.Context, path string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.recordCmd != nil {
		return errors.New("already recording")
	}
	cmd := exec.Command("xcrun", "simctl", "io", d.info.Serial, "recordVideo", "--codec=h264", "--force", path)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		return err
	}
	d.recordCmd, d.recordPath = cmd, path
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
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
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
