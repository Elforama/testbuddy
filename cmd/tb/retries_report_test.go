//go:build !windows

package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/connect"
	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/engine"
	"github.com/elforama/testbuddy/internal/orchestrate"
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
			select { // the flow is waiting on the screen
			case <-h.entered:
			case <-time.After(10 * time.Second):
				t.Fatal("the flow never read the screen")
			}
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
	// #117 review: the hook crashed before printing the flow's end; the console still shows
	// it failed, with the crash and where its files are (stderr: --json keeps stdout for JSON)
	debugDir := filepath.Join(dir, "debug")
	for _, want := range []string{"✗ Looks for a missing button failed", "screenshot exploded", "debug: " + debugDir} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the console lacks %q:\n%s", want, stderr)
		}
	}
	matches, _ := filepath.Glob(filepath.Join(debugDir, "*", "Looks_for_a_missing_button", "error.txt"))
	if len(matches) != 1 {
		t.Fatalf("error.txt: %v", matches)
	}
	if b, _ := os.ReadFile(matches[0]); !strings.Contains(string(b), "screenshot exploded") || !strings.Contains(string(b), "Checkout") {
		t.Errorf("error.txt = %q; want the flow's error and the crash", b)
	}
	b, _ := os.ReadFile(filepath.Join(filepath.Dir(matches[0]), "log.txt"))
	if !strings.Contains(string(b), "screenshot exploded") || strings.Contains(string(b), "screenshot*.png") {
		t.Errorf("log.txt should end with the crash and not point at screen files that were not saved:\n%s", b)
	}
}

// #117 review: a flow whose Finished hook crashed after printing "passed" (here, in effect,
// the console's line followed by the crash) is shown failed after all, with the crash and
// its debug folder; error.txt gets the error and its log ends with the failure.
func TestReportCrashAfterPassed(t *testing.T) {
	var buf strings.Builder
	con := newConsole(&buf, false, false)
	logs := newFlowLogs()
	dir := filepath.Join(t.TempDir(), "Login")
	j := job("Login", "/w/login.yaml", 0, 0)
	devs := map[string]device.Info{"": {Platform: device.Android, Name: "Pixel", Serial: "p"}}
	logs.open(dir, j, devs)
	passed := orchestrate.Result{Job: j, Devices: devs, Status: engine.Passed, Duration: time.Second}
	// what the hook did before it crashed
	logs.close(dir, passed)
	con.flowEnd(passed, dir)
	crash := &orchestrate.HookCrash{Hook: "Finished", Value: "timeline exploded", Stack: []byte("goroutine 1")}
	r := passed
	r.Status, r.Err = engine.Failed, crash
	reportCrash(con, logs, dir, r)
	out := buf.String()
	pass, fail := strings.Index(out, "✓ Login passed"), strings.Index(out, "✗ Login failed")
	if pass < 0 || fail < pass || !strings.Contains(out[fail:], "timeline exploded") || !strings.Contains(out[fail:], "debug: "+dir) {
		t.Errorf("console:\n%s\nwant the passed line, then a ✗ line with the crash and the debug folder", out)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "error.txt")); !strings.Contains(string(b), "timeline exploded") {
		t.Errorf("error.txt = %q", b)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "log.txt"))
	if s := string(b); !strings.Contains(s, "PASSED in 1s") || !strings.Contains(s, "FAILED in 1s: tb crashed reporting this flow") ||
		strings.Index(s, "FAILED") < strings.Index(s, "PASSED") {
		t.Errorf("log.txt:\n%s\nwant the hook's PASSED, then the crash's FAILED", s)
	}
}

// failsAsCancelledApp is a fakeApp whose launch fails for a reason of its own, as the run
// is cancelled (Ctrl-C while it fails).
type failsAsCancelledApp struct{ *fakeApp }

func (a failsAsCancelledApp) LaunchApp(ctx context.Context, id string, o driver.LaunchOpts) error {
	syscall.Kill(os.Getpid(), syscall.SIGINT)
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
	}
	return errors.New("the app crashed on launch")
}

// #117 review: a flow that failed for its own reason as the run was cancelled keeps that
// reason in the reports; it is not called cancelled.
func TestFailureAsCancelledKeepsItsReason(t *testing.T) {
	catchSignals(t)
	old := failureArtifactTimeout
	failureArtifactTimeout = 300 * time.Millisecond
	t.Cleanup(func() { failureArtifactTimeout = old })
	app := withFakePhone(t)
	restore := connect.Override(device.Android, func(ctx context.Context, i device.Info) (driver.Driver, error) {
		return failsAsCancelledApp{app}, nil
	})
	t.Cleanup(restore)
	dir := t.TempDir()
	flow := writeFlow(t, dir, "pass.yaml", passingFlow)
	out := filepath.Join(dir, "report.xml")
	stdout, _ := capture(t, func() {
		cmdTest([]string{flow, "--retries", "2", "--json", "--format", "junit", "--output", out, "--debug-output", filepath.Join(dir, "debug")})
	})
	sum := jsonSummary(t, stdout)
	if len(sum.Flows) != 1 || sum.Flows[0].Status != "ERROR" || !strings.Contains(sum.Flows[0].Error, "the app crashed on launch") ||
		strings.Contains(sum.Flows[0].Error, "cancelled") {
		t.Errorf("JSON summary %+v; want the flow failed with its own reason", sum)
	}
	if b, _ := os.ReadFile(out); !strings.Contains(string(b), "the app crashed on launch") {
		t.Errorf("the JUnit report lacks the flow's own reason:\n%s", b)
	}
}
