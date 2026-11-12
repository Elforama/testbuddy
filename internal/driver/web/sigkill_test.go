//go:build !windows

package web

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/device"
)

// openHelperEnv makes this test binary, run again, act as a tb that opens a browser and waits
// to be killed (TestOpenHelperProcess).
const openHelperEnv = "TB_TEST_OPEN_HELPER"

// TestOpenHelperProcess is not a test: run by TestSigkilledTbLeavesNoBrowser in a process of
// its own, it opens the stub browser the way tb does, prints the profile and waits.
func TestOpenHelperProcess(t *testing.T) {
	if os.Getenv(openHelperEnv) != "1" {
		t.Skip("run by TestSigkilledTbLeavesNoBrowser")
	}
	d, err := Open(ctxT(t), device.WebDevice(1))
	if err != nil {
		os.Stdout.WriteString("error " + err.Error() + "\n")
		os.Exit(1)
	}
	os.Stdout.WriteString("profile " + d.profile + "\n")
	time.Sleep(60 * time.Second) // killed long before
	os.Exit(1)
}

// issue #81: a tb killed with SIGKILL (a CI job cancelled or timed out) could not close its
// browser: Chrome, in a process group of its own since issue #124, kept running with its helper
// processes, and its tb-chrome-* profile stayed on disk.
func TestSigkilledTbLeavesNoBrowser(t *testing.T) {
	f := newFakeBrowser(t, true, onePage)
	helperFile := withHelper(t, f, true) // a browser that runs until it is killed
	chromeFile := filepath.Join(f.dir, "chrome.pid")
	rulesPath := filepath.Join(f.dir, "chrome.rules")
	rules, err := os.ReadFile(rulesPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rulesPath, append([]byte("echo $$ > "+sq(chromeFile)+"\n"), rules...), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestOpenHelperProcess$", "-test.count=1")
	cmd.Env = append(os.Environ(), openHelperEnv+"=1")
	cmd.Stderr = os.Stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	line := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			if strings.HasPrefix(sc.Text(), "profile ") || strings.HasPrefix(sc.Text(), "error ") {
				line <- sc.Text()
				return
			}
		}
		line <- ""
	}()
	var profile string
	select {
	case l := <-line:
		if !strings.HasPrefix(l, "profile ") {
			t.Fatalf("the helper tb did not open the browser: %q", l)
		}
		profile = strings.TrimPrefix(l, "profile ")
	case <-time.After(15 * time.Second):
		t.Fatal("the helper tb did not open the browser within 15 s")
	}
	browser := helperPID(t, chromeFile) // both killed when the test ends, if still running
	helper := helperPID(t, helperFile)
	if _, err := os.Stat(profile); err != nil {
		t.Fatalf("no profile before the kill: %v", err)
	}

	cmd.Process.Signal(syscall.SIGKILL)
	cmd.Wait()

	if !gone(browser) {
		t.Error("issue #81: the browser still runs after tb was killed with SIGKILL")
	}
	if !gone(helper) {
		t.Error("issue #81: a helper process of the browser still runs after tb was killed with SIGKILL")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(profile); os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Errorf("issue #81: the profile %s is still on disk after tb was killed with SIGKILL", profile)
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
}
