//go:build !windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// issue #189: Ctrl-C during a run with more flows than devices. The flow running at the
// cancel stops as before (failed, cancelled, with its failure files); the flows still queued
// are not started ("▶" lines), get no failure files and no log, and are reported skipped in
// the console, JUnit, HTML and the JSON summary. tb stops in about one failure-file wait
// (the running flow's), not one per queued flow: each queued flow used to be started, fail at
// once, and wait failureArtifactTimeout on the device's screen in saveFailure.
func TestCancelSkipsQueuedFlows(t *testing.T) {
	catchSignals(t)
	old := failureArtifactTimeout
	failureArtifactTimeout = time.Second
	t.Cleanup(func() { failureArtifactTimeout = old })
	const queued = 4
	for _, format := range []string{"junit", "html", "json"} {
		t.Run(format, func(t *testing.T) {
			h := withHangingPhone(t) // one device: the flows run one at a time
			dir := t.TempDir()
			files := []string{writeFlow(t, dir, "f0.yaml", passingFlow)}
			for i := 1; i <= queued; i++ {
				files = append(files, writeFlow(t, dir, fmt.Sprintf("f%d.yaml", i),
					strings.Replace(passingFlow, "name: Go to details and back", fmt.Sprintf("name: Queued %d", i), 1)))
			}
			out := filepath.Join(dir, "report."+format)
			debug := filepath.Join(dir, "debug")
			args := append(files, "--debug-output", debug)
			if format == "json" {
				args = append(args, "--json") // the console shows only failures then
			} else {
				args = append(args, "--format", format, "--output", out)
			}
			var code int
			var stdout, stderr string
			done := make(chan struct{})
			go func() {
				defer close(done)
				stdout, stderr = capture(t, func() {
					code = cmdTest(args)
				})
			}()
			select { // the first flow is waiting on the screen
			case <-h.entered:
			case <-time.After(10 * time.Second):
				t.Fatal("the flow never read the screen")
			}
			signalled := time.Now()
			syscall.Kill(os.Getpid(), syscall.SIGINT)
			select {
			case <-done:
			case <-time.After(30 * time.Second):
				close(h.release)
				<-done
				t.Fatal("tb test did not stop on SIGINT")
			}
			took := time.Since(signalled)
			if code != 130 {
				t.Errorf("exit %d, want 130 (cancelled)", code)
			}
			// the running flow's saveFailure waits up to 2×failureArtifactTimeout (screenshot,
			// screen); each queued flow it used to start added as much again. The bound leaves
			// room for a slow CI machine: the "▶" count and the file checks below catch the bug
			// without timing.
			if limit := 2*failureArtifactTimeout + 2500*time.Millisecond; took > limit {
				t.Errorf("issue #189: tb test took %s to stop after SIGINT; want under %s (no failure-file wait for flows that never ran)", took, limit)
			}
			console := stdout // the console's stream: stderr with --json, which keeps stdout for JSON
			if format == "json" {
				console = stderr
			}
			for i := 1; i <= queued; i++ {
				if name := fmt.Sprintf("Queued %d", i); strings.Contains(console, name+" failed") {
					t.Errorf("issue #189: the console shows %s failed; it never ran:\n%s", name, console)
				}
			}
			if format != "json" {
				if n := strings.Count(console, "▶"); n != 1 {
					t.Errorf("issue #189: %d flows started; want 1 (the one running at the cancel):\n%s", n, console)
				}
				for i := 1; i <= queued; i++ {
					if name := fmt.Sprintf("Queued %d", i); !strings.Contains(console, "○ "+name+" skipped") {
						t.Errorf("issue #189: the console does not show %s skipped:\n%s", name, console)
					}
				}
				if !strings.Contains(console, fmt.Sprintf("%d skipped", queued)) {
					t.Errorf("issue #189: the summary does not count %d skipped:\n%s", queued, console)
				}
			}
			// no log, timeline or failure files for a flow that never ran
			filepath.Walk(debug, func(p string, info os.FileInfo, err error) error {
				if rel, _ := filepath.Rel(debug, p); err == nil && !info.IsDir() && strings.Contains(rel, "Queued") {
					t.Errorf("issue #189: %s written for a flow that never ran", rel)
				}
				return nil
			})
			if format != "json" {
				checkReport(t, format, out, queued)
				return
			}
			sum := jsonSummary(t, stdout)
			if sum.Failed != 1 || len(sum.Flows) != 1+queued {
				t.Errorf("JSON summary %+v; want the running flow failed and %d skipped", sum, queued)
			}
			for _, f := range sum.Flows {
				if f.Name == "Go to details and back" {
					if f.Status != "ERROR" || !strings.Contains(f.Error, "cancelled: the run was stopped") {
						t.Errorf("the running flow = %+v; want failed, cancelled", f)
					}
				} else if f.Status != "SKIPPED" || !strings.Contains(f.Error, "the run was stopped before this flow ran") {
					t.Errorf("issue #189: %+v; want SKIPPED: the run was stopped before this flow ran", f)
				}
			}
		})
	}
}

// checkReport checks the JUnit or HTML report of TestCancelSkipsQueuedFlows: the running flow
// failed, the queued ones skipped, saying why.
func checkReport(t *testing.T, format, out string, queued int) {
	t.Helper()
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	report := string(b)
	switch format {
	case "junit":
		if n := strings.Count(report, "<skipped"); n != queued {
			t.Errorf("issue #189: %d <skipped> in the JUnit report; want %d:\n%s", n, queued, report)
		}
		if n := strings.Count(report, "<failure"); n != 1 {
			t.Errorf("%d <failure> in the JUnit report; want 1 (the running flow):\n%s", n, report)
		}
	case "html":
		if !strings.Contains(report, fmt.Sprintf("%d skipped", queued)) {
			t.Errorf("issue #189: the HTML report does not count %d skipped:\n%s", queued, report)
		}
	}
	if !strings.Contains(report, "the run was stopped before this flow ran") {
		t.Errorf("issue #189: the %s report does not say why the queued flows did not run", format)
	}
}
