//go:build !windows

package ios

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func recorderStub(pidFile string, confirm bool) []stubRule {
	say := `echo "Recording started"; `
	if !confirm {
		say = ""
	}
	return []stubRule{{match: "recordVideo", sh: `trap 'echo "Recording completed. Writing to disk."; : > "$7"; exit 0' INT TERM; echo $$ > ` + sq(pidFile) + `; ` + say +
		`while :; do /bin/sleep 0.02; done;`}}
}

func readPid(t *testing.T, pidFile string) int {
	t.Helper()
	waitFile(t, pidFile)
	deadline := time.Now().Add(5 * time.Second)
	for {
		b, _ := os.ReadFile(pidFile)
		if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && pid > 0 {
			return pid
		}
		if time.Now().After(deadline) {
			t.Fatalf("no pid in %s", pidFile)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

func waitGone(t *testing.T, pid int, d time.Duration) bool {
	t.Helper()
	t.Cleanup(func() {
		if alive(pid) {
			syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	deadline := time.Now().Add(d)
	for {
		if !alive(pid) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestKilledTBStopsTheRecorder(t *testing.T) {
	ts := stubTools(t)
	pidFile := filepath.Join(t.TempDir(), "recorder.pid")
	ts.set("xcrun", recorderStub(pidFile, true))
	out := filepath.Join(t.TempDir(), "run.mp4")

	tb := exec.Command(os.Args[0])
	tb.Env = append(os.Environ(), killedTBEnv+"="+out)
	tb.Stderr = os.Stderr
	stdout, err := tb.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := tb.Start(); err != nil {
		t.Fatal(err)
	}
	defer tb.Wait()
	defer tb.Process.Kill()
	line, _ := bufio.NewReader(stdout).ReadString('\n')
	if line != "recording\n" {
		t.Fatalf("the tb to kill did not start recording: %q", line)
	}
	pid := readPid(t, pidFile)

	tb.Process.Signal(syscall.SIGKILL)
	tb.Wait()

	if !waitGone(t, pid, 5*time.Second) {
		t.Fatalf("issue #52: simctl recordVideo (pid %d) still records 5 s after tb was killed", pid)
	}
	if _, err := os.Stat(out); err != nil {
		t.Errorf("the recorder was not stopped with SIGINT (no file written): %v", err)
	}
}

func TestCloseStopsTheRecording(t *testing.T) {
	ts := stubTools(t)
	pidFile := filepath.Join(t.TempDir(), "recorder.pid")
	ts.set("xcrun", recorderStub(pidFile, true))
	d := &Driver{info: testInfo()}
	if err := d.StartRecording(ctxT(t), filepath.Join(t.TempDir(), "run.mp4")); err != nil {
		t.Fatal(err)
	}
	pid := readPid(t, pidFile)
	func() {
		defer func() {
			if p := recover(); p != nil {
				t.Errorf("Close with no runner connection: %v", p)
			}
		}()
		d.Close()
	}()
	if !waitGone(t, pid, 5*time.Second) {
		t.Fatalf("issue #52: simctl recordVideo (pid %d) still records after Close", pid)
	}
}

func TestCloseDuringStartStopsTheRecording(t *testing.T) {
	ts := stubTools(t)
	pidFile := filepath.Join(t.TempDir(), "recorder.pid")
	ts.set("xcrun", recorderStub(pidFile, false))
	old := recordStarted
	recordStarted = 500 * time.Millisecond
	t.Cleanup(func() { recordStarted = old })
	d := &Driver{info: testInfo()}
	started := make(chan error, 1)
	go func() { started <- d.StartRecording(context.Background(), filepath.Join(t.TempDir(), "run.mp4")) }()
	pid := readPid(t, pidFile)
	d.Close()
	if err := <-started; err != nil {
		t.Logf("start: %v", err)
	}
	if !waitGone(t, pid, 2*time.Second) {
		t.Fatalf("issue #52: simctl recordVideo (pid %d) still records after a Close during its start", pid)
	}
}

func TestRecordingStartTimeoutIsNoted(t *testing.T) {
	ts := stubTools(t)
	pidFile := filepath.Join(t.TempDir(), "recorder.pid")
	ts.set("xcrun", recorderStub(pidFile, false))
	old := recordStarted
	recordStarted = 200 * time.Millisecond
	t.Cleanup(func() { recordStarted = old })
	d := &Driver{info: testInfo()}
	if err := d.StartRecording(ctxT(t), filepath.Join(t.TempDir(), "run.mp4")); err != nil {
		t.Fatal(err)
	}
	pid := readPid(t, pidFile)
	defer waitGone(t, pid, 0)
	notes := strings.Join(d.Notes(), "\n")
	if !strings.Contains(notes, "did not confirm it started") {
		t.Errorf("issue #52: a start simctl never confirmed is silent: notes %q", notes)
	}
	if err := d.StopRecording(ctxT(t)); err != nil {
		t.Errorf("stop: %v", err)
	}
}

func TestRecordingStartsWithAStatusBarChange(t *testing.T) {
	ts := stubTools(t)
	pidFile := filepath.Join(t.TempDir(), "recorder.pid")
	ts.set("xcrun", append([]stubRule{{match: "status_bar", out: ""}}, recorderStub(pidFile, true)...))
	d := &Driver{info: testInfo()}
	before := time.Now()
	if err := d.StartRecording(ctxT(t), filepath.Join(t.TempDir(), "run.mp4")); err != nil {
		t.Fatal(err)
	}
	defer d.StopRecording(ctxT(t))
	var bar []string
	for _, c := range ts.simctl() {
		if strings.HasPrefix(c, "status_bar ") {
			bar = append(bar, c)
		}
	}
	udid := testInfo().Serial
	if len(bar) != 3 || bar[0] != "status_bar "+udid+" list" || !strings.HasPrefix(bar[1], "status_bar "+udid+" override --time ") || bar[2] != "status_bar "+udid+" clear" {
		t.Fatalf("status bar calls %q, want list, override --time, clear", bar)
	}
	if got := d.RecordingStarted(); got.Before(before) || got.After(time.Now()) {
		t.Errorf("RecordingStarted %v, not the status bar change's time", got)
	}
	shown, held, ok := d.RecordingMark()
	if !ok || !shown.Equal(d.RecordingStarted()) || held < statusBarHold {
		t.Errorf("RecordingMark = %v, %v, %v; want the start, held at least %s", shown, held, ok, statusBarHold)
	}
}

func TestRecordingLeavesAStatusBarOverrideAlone(t *testing.T) {
	ts := stubTools(t)
	pidFile := filepath.Join(t.TempDir(), "recorder.pid")
	ts.set("xcrun", append([]stubRule{{match: "status_bar", out: "Current Status Bar Overrides:\n=============================\nTime: 9:41\n"}}, recorderStub(pidFile, true)...))
	d := &Driver{info: testInfo()}
	if err := d.StartRecording(ctxT(t), filepath.Join(t.TempDir(), "run.mp4")); err != nil {
		t.Fatal(err)
	}
	defer d.StopRecording(ctxT(t))
	for _, c := range ts.simctl() {
		if strings.Contains(c, "override") || strings.Contains(c, "clear") {
			t.Errorf("changed the status bar someone set: %q", c)
		}
	}
	if d.RecordingStarted().IsZero() {
		t.Error("RecordingStarted is zero: want when simctl said it started")
	}
	if _, _, ok := d.RecordingMark(); ok {
		t.Error("RecordingMark says there is a mark; nothing was changed")
	}
}
