//go:build !windows

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/connect"
	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/driver"
)

// jsonSummary reads tb test --json's output.
func jsonSummary(t *testing.T, out string) (sum struct {
	Passed, Failed int
	Flows          []struct{ Name, Status, Error string }
}) {
	t.Helper()
	i := strings.Index(out, "{")
	if i < 0 {
		t.Fatalf("no JSON summary in %q", out)
	}
	if err := json.Unmarshal([]byte(out[i:]), &sum); err != nil {
		t.Fatalf("summary %q: %v", out, err)
	}
	return sum
}

// issue #117: with --retries, a flow that fails because the run is cancelled (Ctrl-C) is not
// retried, so its failure is its result: it is in every report tb writes (JUnit, HTML and the
// JSON summary), failed, with its reason. The hook used to drop it, expecting a retry.
func TestCancelledRetriedFlowIsReported(t *testing.T) {
	catchSignals(t)
	old := failureArtifactTimeout
	failureArtifactTimeout = 300 * time.Millisecond
	t.Cleanup(func() { failureArtifactTimeout = old })
	for _, format := range []string{"junit", "html"} {
		t.Run(format, func(t *testing.T) {
			h := withHangingPhone(t)
			dir := t.TempDir()
			flow := writeFlow(t, dir, "pass.yaml", passingFlow)
			out := filepath.Join(dir, "report."+format)
			var code int
			var stdout string
			done := make(chan struct{})
			go func() {
				defer close(done)
				stdout, _ = capture(t, func() {
					code = cmdTest([]string{flow, "--retries", "2", "--json", "--format", format, "--output", out,
						"--debug-output", filepath.Join(dir, "debug")})
				})
			}()
			time.Sleep(300 * time.Millisecond) // the flow is waiting on the screen
			syscall.Kill(os.Getpid(), syscall.SIGINT)
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				close(h.release)
				<-done
				t.Fatal("tb test did not stop on SIGINT")
			}
			if code != 130 {
				t.Errorf("exit %d, want 130 (cancelled)", code)
			}
			sum := jsonSummary(t, stdout)
			if sum.Failed != 1 || len(sum.Flows) != 1 || sum.Flows[0].Status != "ERROR" || !strings.Contains(sum.Flows[0].Error, "cancelled") {
				t.Errorf("issue #117: JSON summary %+v; want the cancelled flow, failed, saying it was cancelled", sum)
			}
			b, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(b), "Go to details and back") {
				t.Errorf("issue #117: the %s report lacks the cancelled flow:\n%s", format, b)
			} else if !strings.Contains(string(b), "cancelled: the run was stopped") {
				t.Errorf("the %s report does not say the flow was cancelled:\n%s", format, b)
			}
			if format == "junit" && !strings.Contains(string(b), "<failure") {
				t.Errorf("issue #117: the JUnit report does not show the cancelled flow failed:\n%s", b)
			}
		})
	}
}

// panicShotApp is a fakeApp whose full-size screenshot panics: saveFailure, in the run's
// Finished hook, takes one for a failed flow.
type panicShotApp struct{ *fakeApp }

func (p panicShotApp) Screenshot(ctx context.Context, s float64, fm string) ([]byte, error) {
	panic("screenshot exploded")
}

// issue #117: a panic in the Finished hook (here, while saving a failed flow's files) does
// not end tb: the run finishes, the flow is in the report, failed, naming the crash, and the
// device is closed.
func TestFinishedHookPanicIsReported(t *testing.T) {
	app := withFakePhone(t)
	restore := connect.Override(device.Android, func(ctx context.Context, i device.Info) (driver.Driver, error) {
		return panicShotApp{app}, nil
	})
	t.Cleanup(restore)
	dir := t.TempDir()
	flow := writeFlow(t, dir, "fail.yaml", failingFlow)
	out := filepath.Join(dir, "report.xml")
	var code int
	var crashed any
	stdout, stderr := capture(t, func() {
		defer func() { crashed = recover() }()
		code = cmdTest([]string{flow, "--json", "--format", "junit", "--output", out, "--debug-output", filepath.Join(dir, "debug")})
	})
	if crashed != nil {
		t.Fatalf("issue #117: the hook's panic escaped tb test: %v", crashed)
	}
	if code != 1 {
		t.Errorf("exit %d, want 1\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	sum := jsonSummary(t, stdout)
	if sum.Failed != 1 || len(sum.Flows) != 1 || sum.Flows[0].Status != "ERROR" ||
		!strings.Contains(sum.Flows[0].Error, "screenshot exploded") || !strings.Contains(sum.Flows[0].Error, "Checkout") {
		t.Errorf("JSON summary %+v; want the flow failed with its own error and the crash", sum)
	}
	if b, _ := os.ReadFile(out); !strings.Contains(string(b), "screenshot exploded") {
		t.Errorf("the JUnit report does not name the crash:\n%s", b)
	}
	app.get(func() {
		if !app.closed {
			t.Error("the device was not closed after the run")
		}
	})
}
