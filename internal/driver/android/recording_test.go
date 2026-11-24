//go:build !windows

package android

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/device"
)

func TestKilledTBHelper(t *testing.T) {
	path := os.Getenv(killedTBEnv)
	if path == "" {
		t.Skip("run by TestKilledTBStopsTheRecorder")
	}
	if p := device.ADB(); !strings.Contains(p, "tb-driver-stubs-") {
		t.Fatalf("adb is %s, not the stub: refusing to run", p)
	}
	a := recordingAgent(t, true, "")
	d := bareDriver(device.NewADB(testSerial))
	c, h, err := dialHello(a.port(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	d.adopt(c, a.port(), h)
	if err := d.StartRecording(ctxT(t), path); err != nil {
		t.Fatal(err)
	}
	os.Stdout.WriteString("\nrecording\n")
	select {}
}

func recorderRules(pidFile string, confirm bool) []stubRule {
	say := `echo "Content area is 1080x2400 at offset x=0 y=0"; `
	if !confirm {
		say = ""
	}
	return []stubRule{
		{match: "pkill -INT -f", sh: `read p < ` + sq(pidFile) + ` && kill -INT "$p";`},
		{match: "screenrecord --verbose", sh: `trap 'exit 0' INT TERM HUP; echo $$ > ` + sq(pidFile) + `; ` + say +
			`while :; do /bin/sleep 0.02; done;`}}
}

func fastStop(t *testing.T) {
	old := recordStopWait
	recordStopWait = 300 * time.Millisecond
	t.Cleanup(func() { recordStopWait = old })
}

func readPid(t *testing.T, pidFile string) int {
	t.Helper()
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

func stopsTBRecorders(calls []string) bool {
	for _, c := range calls {
		if strings.Contains(c, " shell ") && strings.Contains(c, "touch "+recordStop) && strings.Contains(c, "pkill -INT -f") &&
			strings.Contains(c, "tb-recordin[g]") {
			return true
		}
	}
	return false
}

func TestKilledTBStopsTheRecorder(t *testing.T) {
	s := stubADB(t, nil)
	pidFile := filepath.Join(t.TempDir(), "recorder.pid")
	s.rules(recorderRules(pidFile, true))
	out := filepath.Join(t.TempDir(), "run.mp4")

	tb := exec.Command(os.Args[0], "-test.run=^TestKilledTBHelper$")
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
	r := bufio.NewReader(stdout)
	for {
		line, err := r.ReadString('\n')
		if line == "recording\n" {
			break
		}
		if err != nil {
			t.Fatalf("the tb to kill did not start recording: %q", line)
		}
	}
	pid := readPid(t, pidFile)

	tb.Process.Signal(syscall.SIGKILL)
	tb.Wait()

	if !waitGone(t, pid, 5*time.Second) {
		t.Fatalf("issue #52: adb shell screenrecord (pid %d) still runs 5 s after tb was killed; adb calls\n%s", pid, strings.Join(s.joined(), "\n"))
	}
	if calls := s.joined(); !stopsTBRecorders(calls) {
		t.Errorf("issue #52: the device's screenrecord was not told to stop after tb was killed: adb calls\n%s", strings.Join(calls, "\n"))
	}
}

func TestCloseStopsTheRecording(t *testing.T) {
	fastStop(t)
	s := stubADB(t, nil)
	pidFile := filepath.Join(t.TempDir(), "recorder.pid")
	s.rules(recorderRules(pidFile, true))
	a := recordingAgent(t, true, "")
	d := stubbedDriver(t, s, a)
	if err := d.StartRecording(ctxT(t), filepath.Join(t.TempDir(), "run.mp4")); err != nil {
		t.Fatal(err)
	}
	pid := readPid(t, pidFile)
	d.Close()
	if !waitGone(t, pid, 5*time.Second) {
		t.Fatalf("issue #52: adb shell screenrecord (pid %d) still runs after Close", pid)
	}
	if !stopsTBRecorders(append(a.shCmds(), s.joined()...)) {
		t.Errorf("issue #52: Close did not stop screenrecord on the device: agent %q", a.shCmds())
	}
}

func TestRecordingStartEndsLeftovers(t *testing.T) {
	fastStop(t)
	s := stubADB(t, nil)
	pidFile := filepath.Join(t.TempDir(), "recorder.pid")
	s.rules(recorderRules(pidFile, true))
	a := recordingAgent(t, true, "")
	d := stubbedDriver(t, s, a)
	if err := d.StartRecording(ctxT(t), filepath.Join(t.TempDir(), "run.mp4")); err != nil {
		t.Fatal(err)
	}
	defer waitGone(t, readPid(t, pidFile), 0)
	sh := a.shCmds()
	if len(sh) == 0 || !strings.Contains(sh[0], "pkill -f") || !strings.Contains(sh[0], "tb-recordin[g]") ||
		!strings.HasSuffix(sh[0], "rm -f /sdcard/tb-recording-*.mp4 /sdcard/tb-recording.stop") {
		t.Errorf("issue #52: the start does not end a killed tb's screenrecord first: agent %q", sh)
	}
	for _, c := range sh {
		if strings.Contains(c, "pkill") && !strings.Contains(c, "tb-recordin[g]") {
			t.Errorf("a pkill not limited to tb's recordings: %q", c)
		}
	}
	d.StopRecording(ctxT(t))
}

func TestRecordingStartTimeoutIsNoted(t *testing.T) {
	fastStop(t)
	s := stubADB(t, nil)
	pidFile := filepath.Join(t.TempDir(), "recorder.pid")
	s.rules(recorderRules(pidFile, false))
	old := recordStarted
	recordStarted = 200 * time.Millisecond
	t.Cleanup(func() { recordStarted = old })
	d := stubbedDriver(t, s, recordingAgent(t, true, ""))
	if err := d.StartRecording(ctxT(t), filepath.Join(t.TempDir(), "run.mp4")); err != nil {
		t.Fatal(err)
	}
	defer waitGone(t, readPid(t, pidFile), 0)
	notes := strings.Join(d.Notes(), "\n")
	if !strings.Contains(notes, "did not confirm it started") {
		t.Errorf("issue #52: a start screenrecord never confirmed is silent: notes %q", notes)
	}
	d.StopRecording(ctxT(t))
	if calls := s.joined(); !stopsTBRecorders(calls) {
		t.Errorf("a recorder killed after the stop wait: the device was not told to stop through adb:\n%s", strings.Join(calls, "\n"))
	}
}

func TestCancelledStartStopsTheDevice(t *testing.T) {
	s := stubADB(t, nil)
	pidFile := filepath.Join(t.TempDir(), "recorder.pid")
	s.rules(recorderRules(pidFile, false))
	d := stubbedDriver(t, s, recordingAgent(t, true, ""))
	ctx, cancel := context.WithCancel(ctxT(t))
	go func() {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if b, _ := os.ReadFile(pidFile); len(b) > 0 {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		cancel()
	}()
	if err := d.StartRecording(ctx, filepath.Join(t.TempDir(), "run.mp4")); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled start = %v", err)
	}
	if !waitGone(t, readPid(t, pidFile), 5*time.Second) {
		t.Error("the host's adb shell runs on after a cancelled start")
	}
	if calls := s.joined(); !stopsTBRecorders(calls) {
		t.Errorf("issue #52: a cancelled start did not tell the device to stop: adb calls\n%s", strings.Join(calls, "\n"))
	}
	if calls := s.joined(); !containsAny(calls, " shell rm -f /sdcard/tb-recording-*.mp4 /sdcard/tb-recording.stop") {
		t.Errorf("issue #52: a cancelled start left its files on the device: adb calls\n%s", strings.Join(calls, "\n"))
	}
	if d.recordCmd != nil {
		t.Error("recording after a cancelled start")
	}
}
