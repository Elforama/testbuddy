package ios

// Issue #52: the recorder (simctl io recordVideo) must not outlive tb, even a tb killed with
// SIGKILL, and Close stops a recording; a start that simctl never confirms says so in the trace.

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

// killedTBEnv makes the test binary a tb that starts a recording and waits to be killed
// (killedTB); its value is the recording's path. TestMain runs it.
const killedTBEnv = "TB_TEST_IOS_KILLED_TB"

// killedTB starts a recording on the stub simctl, says so on stdout and waits forever: the test
// kills it with SIGKILL. The stubs' PATH and $TB_STUB_DIR come from the test.
func killedTB(path string) int {
	if p, err := exec.LookPath("xcrun"); err != nil || !strings.Contains(p, "tb-driver-stubs-") {
		os.Stderr.WriteString("xcrun is not the stub: refusing to run\n")
		return 3
	}
	d := &Driver{info: testInfo()}
	if err := d.StartRecording(context.Background(), path); err != nil {
		os.Stderr.WriteString(err.Error() + "\n")
		return 1
	}
	os.Stdout.WriteString("recording\n")
	select {}
}

// recorderStub answers recordVideo: it writes its pid to pidFile, says the recording started
// (confirm), and records until SIGINT or SIGTERM. Then, as real simctl does, it says so before it
// writes the file ($7) and exits: a recorder whose output nobody reads any more dies of SIGPIPE
// there and writes nothing (issue #52 review).
func recorderStub(pidFile string, confirm bool) []stubRule {
	say := `echo "Recording started"; `
	if !confirm {
		say = ""
	}
	return []stubRule{{match: "recordVideo", sh: `trap 'echo "Recording completed. Writing to disk."; : > "$7"; exit 0' INT TERM; echo $$ > ` + sq(pidFile) + `; ` + say +
		`while :; do /bin/sleep 0.02; done;`}}
}

// readPid waits for the stub recorder's pid.
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

// waitGone waits up to d for pid to be gone. The stub recorder is this test's own process: it
// is killed at the test's end either way, so a failing run leaves nothing behind.
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

// Issue #52: a tb killed with SIGKILL mid-recording (nothing in it can run) must not leave
// simctl recording: the recorder is stopped with SIGINT within seconds, its file written.
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
	defer tb.Process.Kill() // the tb this test started
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

// Issue #52: Close stops a recording in progress.
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

// Issue #52: a recording simctl never confirms goes on (it may only be slow), but the run's
// trace says the recorder did not confirm it started.
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
