//go:build !windows

package web

import (
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

func useWatchdog(t *testing.T, path string) {
	old := watchdogPath
	watchdogPath = func() string { return path }
	t.Cleanup(func() { watchdogPath = old })
}

func writeRules(t *testing.T, f *fakeBrowser, rules string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.dir, "chrome.rules"), []byte(rules), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestOpenBrowserExits(t *testing.T) {
	for _, watchdog := range []bool{true, false} {
		t.Run("watchdog="+strconv.FormatBool(watchdog), func(t *testing.T) {
			f := newFakeBrowser(t, false, onePage)
			if !watchdog {
				useWatchdog(t, "")
			}
			writeRules(t, f, "echo 'no display found' >&2\nexit 3\n")
			start := time.Now()
			_, err := Open(ctxT(t), device.WebDevice(1))
			if err == nil || !strings.Contains(err.Error(), "exited before") || !strings.Contains(err.Error(), "no display found") {
				t.Errorf("issue #81: err = %v", err)
			}
			if took := time.Since(start); took > 5*time.Second {
				t.Errorf("issue #81: Open took %v to see the browser exit", took)
			}
		})
	}
	t.Run("missing binary", func(t *testing.T) {
		newFakeBrowser(t, false, onePage)
		t.Setenv("TB_CHROME", filepath.Join(t.TempDir(), "no-chrome"))
		start := time.Now()
		if _, err := Open(ctxT(t), device.WebDevice(1)); err == nil {
			t.Error("Open started a browser that is not there")
		}
		if took := time.Since(start); took > 5*time.Second {
			t.Errorf("issue #81: Open took %v to fail", took)
		}
	})
}

func zombie(t *testing.T, pid int) bool {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		out, _ := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
		st := strings.TrimSpace(string(out))
		if strings.HasPrefix(st, "Z") {
			return true
		}
		if st == "" || time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestDirectBrowserNotReapedBeforeClose(t *testing.T) {
	f := newFakeBrowser(t, true, onePage)
	useWatchdog(t, "")
	d, err := Open(ctxT(t), device.WebDevice(1))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if d.lifeline != nil {
		t.Fatal("started through a watchdog")
	}
	os.WriteFile(filepath.Join(f.dir, "quit"), nil, 0o644)
	if !zombie(t, d.cmd.Process.Pid) {
		t.Error("issue #81: the browser was reaped before Close: Kill could signal a reused pid")
	}
}

func TestOpenWithoutAWorkingWatchdog(t *testing.T) {
	other := filepath.Join(t.TempDir(), "other-tb")
	if err := os.WriteFile(other, []byte("#!/bin/sh\necho 'unknown command' >&2\nexit 2\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{"removed": filepath.Join(t.TempDir(), "gone"), "another tb": other} {
		t.Run(name, func(t *testing.T) {
			newFakeBrowser(t, true, onePage)
			useWatchdog(t, path)
			d, err := Open(ctxT(t), device.WebDevice(1))
			if err != nil {
				t.Fatal(err)
			}
			if d.lifeline != nil || d.cmd.Path == path {
				t.Error("the browser was not started directly")
			}
			d.Close()
		})
	}
}

func TestWatchdogExitsWithTheBrowser(t *testing.T) {
	f := newFakeBrowser(t, true, onePage)
	pidFile := withHelper(t, f, false)
	d, err := Open(ctxT(t), device.WebDevice(1))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if d.lifeline == nil {
		t.Fatal("no watchdog")
	}
	helper := helperPID(t, pidFile)
	os.WriteFile(filepath.Join(f.dir, "quit"), nil, 0o644)
	select {
	case <-d.waited:
	case <-time.After(5 * time.Second):
		t.Fatal("issue #81: the watchdog still runs after the browser exited")
	}
	if !gone(helper) {
		t.Error("issue #81: the browser's helper still runs after the browser exited")
	}
}

func TestKillKillsAWatchdogThatDoesNotAnswer(t *testing.T) {
	f := newFakeBrowser(t, true, onePage)
	deaf := filepath.Join(t.TempDir(), "deaf-watchdog")
	browserFile := filepath.Join(f.dir, "browser.pid")
	script := "#!/bin/sh\nshift 2\n\"$@\" &\necho $! > " + sq(browserFile) + "\necho '" + watchdogReady + "' $!\nexec 1>&-\n" +
		"while [ -d " + sq(f.dir) + " ]; do /bin/sleep 0.05; done\n"
	if err := os.WriteFile(deaf, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if b, err := os.ReadFile(browserFile); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && pid > 0 {
				syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
	useWatchdog(t, deaf)
	d, err := Open(ctxT(t), device.WebDevice(1))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if d.lifeline == nil {
		t.Fatal("no watchdog")
	}
	start := time.Now()
	d.Kill()
	took := time.Since(start)
	select {
	case <-d.waited:
	default:
		t.Error("Kill returned with the watchdog still running")
	}
	if took < 1900*time.Millisecond || took > 4*time.Second {
		t.Errorf("Kill took %v, want about 2 s", took)
	}
}

func TestWatchdogVariableAloneRunsNoWatchdog(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), watchdogEnv+"="+watchdogProto)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("err = %v, output %q", err, out)
	}
}

func TestRemoveProfileOnlyOurs(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	ours, _ := os.MkdirTemp("", "tb-chrome-")
	nested := filepath.Join(os.TempDir(), "x", "tb-chrome-1")
	other := filepath.Join(os.TempDir(), "my-files")
	elsewhere := filepath.Join(t.TempDir(), "tb-chrome-2")
	for _, p := range []string{nested, other, elsewhere} {
		os.MkdirAll(p, 0o755)
	}
	for _, p := range []string{ours, nested, other, elsewhere, "tb-chrome-3"} {
		removeProfile(p)
	}
	if _, err := os.Stat(ours); !os.IsNotExist(err) {
		t.Error("the profile is still there")
	}
	for _, p := range []string{nested, other, elsewhere} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s was removed", p)
		}
	}
}

func TestWatchdogAnswerToNoOne(t *testing.T) {
	f := newFakeBrowser(t, true, onePage)
	browserFile := filepath.Join(f.dir, "browser.pid")
	writeRules(t, f, "echo $$ > "+sq(browserFile)+"\n"+
		"while [ -d "+sq(f.dir)+" ]; do /bin/sleep 0.02; done\n")
	profile, err := os.MkdirTemp("", "tb-chrome-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if b, err := os.ReadFile(browserFile); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && pid > 0 && !gone(pid) {
				syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
	in, keep, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer keep.Close()
	cmd := exec.Command(os.Args[0], watchdogArg, profile, stubChrome)
	cmd.Env = append(os.Environ(), watchdogEnv+"="+watchdogProto)
	cmd.Stdin, cmd.Stdout = in, w
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	in.Close()
	w.Close()
	done := make(chan struct{})
	go func() { cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		cmd.Process.Kill()
		<-done
		t.Fatal("the watchdog did not exit")
	}
	deadline := time.Now().Add(time.Second)
	for {
		if _, err := os.Stat(browserFile); err == nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if b, err := os.ReadFile(browserFile); err == nil {
		if pid, _ := strconv.Atoi(strings.TrimSpace(string(b))); pid > 0 && !gone(pid) {
			t.Error("issue #81: the browser still runs after its watchdog could not answer")
		}
	}
	if _, err := os.Stat(profile); !os.IsNotExist(err) {
		t.Error("issue #81: the profile is still on disk after the watchdog could not answer")
	}
}

func fakeChromeChild(log *[]string) *chromeChild {
	c := newChromeChild(4242)
	c.wait4 = func(pid int, st *syscall.WaitStatus, opts int) (int, error) {
		*log = append(*log, "reap")
		return pid, nil
	}
	c.kill = func(pid int, sig syscall.Signal) error {
		*log = append(*log, "kill "+strconv.Itoa(pid))
		return nil
	}
	return c
}

func TestWatchdogKillsTheGroupBeforeTheReap(t *testing.T) {
	var log []string
	fakeChromeChild(&log).end()
	if want := []string{"kill -4242", "kill 4242", "reap"}; strings.Join(log, ",") != strings.Join(want, ",") {
		t.Errorf("issue #81: end did %q, want %q: the group killed before Chrome is reaped", log, want)
	}
}

func TestWatchdogSweepsAfterAnExit(t *testing.T) {
	var log []string
	c := fakeChromeChild(&log)
	if !c.exitedOnItsOwn() {
		t.Fatal("the exit was not seen")
	}
	c.end()
	if want := []string{"reap", "kill -4242"}; strings.Join(log, ",") != strings.Join(want, ",") {
		t.Errorf("did %q, want %q", log, want)
	}
}
