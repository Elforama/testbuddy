//go:build !windows

package ios

// clearState reinstalls the app even when the run is cancelled (issue #49).

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// issue #49: a run cancelled (Ctrl-C, a timeout) once clearState has started to uninstall the
// app still installs it again. Cancelling between the two used to kill `simctl install` (or
// skip it) and leave the app uninstalled, so every later flow failed "not installed". The
// uninstall took the location grant with it, so the location cache is forgotten as well, and a
// cancelled run that waits on the pair says so.
func TestClearStateCancelledMidReinstallKeepsAppInstalled(t *testing.T) {
	defer func(v time.Duration, w io.Writer) { reinstallNotice, progressOut = v, w }(reinstallNotice, progressOut)
	reinstallNotice = 50 * time.Millisecond
	for _, during := range []string{"uninstall", "install"} {
		t.Run("cancelled during "+during, func(t *testing.T) {
			ts := stubTools(t)
			bundle := makeBundle(t, ts.home)
			state := t.TempDir()
			installed := filepath.Join(state, "installed") // the fake simulator has the app
			started := filepath.Join(state, "started")
			goOn := filepath.Join(state, "go")
			os.WriteFile(installed, nil, 0o644)
			// The step cancelled in blocks until the test has cancelled the context.
			block := ": > " + sq(started) + "; while [ ! -e " + sq(goOn) + " ]; do /bin/sleep 0.01; done; "
			uninstall := "/bin/rm -f " + sq(installed) + "; "
			install := ""
			if during == "uninstall" {
				uninstall += block
			} else {
				install = block
			}
			install += ": > " + sq(installed) + "; "
			ts.set("xcrun", []stubRule{
				{match: app + " app", out: bundle + "\n"},
				{match: "simctl uninstall " + testUDID, sh: uninstall},
				{match: "simctl install " + testUDID, sh: install},
			})
			var said syncBuffer
			progressOut = &said
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			go func() {
				for range 2000 {
					if _, err := os.Stat(started); err == nil {
						cancel()
						time.Sleep(150 * time.Millisecond) // the pair outlives the cancellation
						os.WriteFile(goOn, nil, 0o644)
						return
					}
					time.Sleep(5 * time.Millisecond)
				}
			}()
			d := &Driver{info: testInfo(), locationSet: map[string]string{app: "always"}}
			err := d.ClearState(ctx, app)
			if ctx.Err() == nil {
				t.Fatalf("the context was never cancelled (err %v)", err)
			}
			if _, serr := os.Stat(installed); serr != nil {
				t.Fatalf("the app was left uninstalled (ClearState: %v; simctl %q)", err, ts.simctl())
			}
			if !errors.Is(err, context.Canceled) {
				t.Errorf("err = %v, want the cancellation", err)
			}
			d.mu.Lock()
			_, cached := d.locationSet[app]
			d.mu.Unlock()
			if cached {
				t.Error("the location cache still holds the app: a later `location: always` would be skipped")
			}
			if !strings.Contains(said.String(), "finishing clearState's reinstall of "+app) {
				t.Errorf("printed %q, want the wait announced", said.String())
			}
		})
	}
}

// syncBuffer is a bytes.Buffer safe for a writer and a reader on different goroutines.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// issue #49: the uninstall and install run in a process group of their own, so a Ctrl-C at the
// terminal (sent to tb's whole group) does not reach them.
func TestClearStateReinstallOwnSession(t *testing.T) {
	ts := stubTools(t)
	bundle := makeBundle(t, ts.home)
	pgids := filepath.Join(t.TempDir(), "pgids")
	record := "/bin/ps -o pgid= -p $$ >> " + sq(pgids) + "; "
	ts.set("xcrun", []stubRule{
		{match: app + " app", out: bundle + "\n"},
		{match: "simctl uninstall " + testUDID, sh: record},
		{match: "simctl install " + testUDID, sh: record},
	})
	if err := (&Driver{info: testInfo()}).ClearState(ctxT(t), app); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(pgids)
	got := strings.Fields(string(b))
	if len(got) != 2 {
		t.Fatalf("recorded %q, want the uninstall's and the install's group", got)
	}
	own := strconv.Itoa(syscall.Getpgrp())
	for _, g := range got {
		if g == own {
			t.Errorf("simctl ran in tb's process group %s: a Ctrl-C would kill it", own)
		}
	}
}

// issue #49: an uninstall that fails after CoreSimulator removed the app installs it back; one
// that failed with the app still there changes nothing; one whose probe cannot tell keeps the
// copy and says where (no install over an app that may still hold its data).
func TestClearStateUninstallFailed(t *testing.T) {
	const gone = "An error was encountered processing the command (domain=NSPOSIXErrorDomain, code=2):\nNo such file or directory"
	for _, c := range []struct {
		name, probe string // the probe's answer after the failed uninstall ("" = still installed)
		want        string // the simctl calls' tail
		wantErr     string
	}{
		{"app removed", gone, "uninstall get_app_container install privacy", ""},
		{"app still there", "", "uninstall get_app_container", "uninstall failed"},
		{"probe failed", "CoreSimulator is busy", "uninstall get_app_container", "its bundle is kept at "},
	} {
		t.Run(c.name, func(t *testing.T) {
			ts := stubTools(t)
			bundle := makeBundle(t, ts.home)
			uninstalled := filepath.Join(t.TempDir(), "uninstalled")
			probe := ""
			if c.probe != "" {
				probe = "[ -e " + sq(uninstalled) + " ] && { printf '%s' " + sq(c.probe) + "; exit 2; };"
			}
			ts.set("xcrun", []stubRule{
				{match: app + " app", sh: probe, out: bundle + "\n"},
				{match: "simctl uninstall " + testUDID, sh: ": > " + sq(uninstalled) + ";", out: "uninstall failed", code: 1},
			})
			err := (&Driver{info: testInfo()}).ClearState(ctxT(t), app)
			var order []string
			for _, call := range ts.simctl() {
				order = append(order, strings.Fields(call)[0])
			}
			got := strings.Join(order, " ")
			if !strings.HasSuffix(got, c.want) {
				t.Errorf("simctl %s, want it to end %s", got, c.want)
			}
			if c.wantErr == "" && err != nil || c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)) {
				t.Errorf("err = %v, want %q", err, c.wantErr)
			}
			if err != nil && strings.Contains(err.Error(), "kept at ") {
				kept := strings.Fields(strings.SplitN(err.Error(), "kept at ", 2)[1])[0]
				kept = strings.TrimSuffix(kept, "):")
				if _, serr := os.Stat(filepath.Join(kept, "Info.plist")); serr != nil {
					t.Errorf("copy not kept at %q", kept)
				}
				os.RemoveAll(filepath.Dir(kept))
			}
		})
	}
}

// issue #49: the uncancellable uninstall-and-install has a bound of its own: an install that
// never ends gives up (naming the kept copy) instead of hanging the run, and what it started
// goes with it.
func TestClearStateReinstallBounded(t *testing.T) {
	defer func(v time.Duration) { reinstallWait = v }(reinstallWait)
	reinstallWait = 300 * time.Millisecond
	ts := stubTools(t)
	bundle := makeBundle(t, ts.home)
	child := filepath.Join(t.TempDir(), "child")
	ts.set("xcrun", []stubRule{
		{match: app + " app", out: bundle + "\n"},
		// A child that would hold simctl's output open for 30 s.
		{match: "simctl install " + testUDID, sh: "/bin/sleep 30 & echo $! >> " + sq(child) + "; wait;"},
	})
	start := time.Now()
	err := (&Driver{info: testInfo()}).ClearState(ctxT(t), app)
	if err == nil || !strings.Contains(err.Error(), "its bundle is kept at ") {
		t.Fatalf("err = %v", err)
	}
	if d := time.Since(start); d > 1500*time.Millisecond {
		t.Errorf("gave up after %s, want about %s", d, reinstallWait)
	}
	kept := strings.Fields(strings.SplitN(err.Error(), "kept at ", 2)[1])[0]
	os.RemoveAll(filepath.Dir(strings.TrimSuffix(kept, "):")))
	b, _ := os.ReadFile(child)
	pids := strings.Fields(string(b))
	if len(pids) == 0 {
		t.Fatal("the install's child was never started")
	}
	for _, p := range pids {
		pid, _ := strconv.Atoi(p)
		alive := true
		for range 100 {
			if syscall.Kill(pid, 0) != nil {
				alive = false
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if alive {
			syscall.Kill(pid, syscall.SIGKILL)
			t.Errorf("the install's child %d outlived the bound", pid)
		}
	}
}
