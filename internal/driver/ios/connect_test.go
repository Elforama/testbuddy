package ios

// Finding, reusing and replacing the runner on the simulator's preferred port. The fake runner
// listens on a free port in the runner range, and the simulator's UDID is chosen so that port is
// its preferred one: connect probes only ports the test controls.

import (
	"fmt"
	"math/rand/v2"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/device"
)

// guardOffset: freePorts reserves runner-range port p by listening on p+guardOffset for the
// rest of the test (issue #259). The runner port itself must stay free (the test, or a runner
// xcodebuild "starts", listens there later), so the guard is what another freePorts caller, in
// this go test process or another one running at once, finds taken. The OS releases it when the
// process dies, so a killed run leaves nothing behind.
const guardOffset = 20000

// pickStart picks the first of starts candidate positions freePorts tries: at random, so
// concurrent runs rarely even try the same ports. The reservation tests pin it so that only the
// guards keep callers apart.
var pickStart = func(starts int) int { return rand.IntN(starts) }

// freePorts finds n consecutive free ports in the runner range (none of them a real runner's)
// and keeps them this test's until it ends (issue #259): two go test runs of this package at
// once used to pick the same ports, from the top of the range, and fight over them. It reserves
// the whole window connect and ReleaseDevice probe from the first port, so another test's fake
// runner is never asked for its info, told to exit, or adopted.
func freePorts(t *testing.T, n int) int {
	t.Helper()
	window := max(n, portTries)
	starts := portSpan - window + 1
	first := pickStart(starts)
	for i := 0; i < starts; i++ {
		p := basePort + (first+i)%starts
		if guards := reservePorts(p, n, window); guards != nil {
			t.Cleanup(func() {
				for _, g := range guards {
					g.Close()
				}
			})
			return p
		}
	}
	// Not a skip: a broken or leaked reservation must not pass as green.
	t.Fatalf("no %d free ports (window %d) in the runner range: all %d starts taken", n, window, starts)
	return 0
}

// reservePorts takes the guards of ports p to p+window-1 and checks that p to p+n-1 are free;
// nil (with nothing held) if a guard is another caller's or a port is in use.
func reservePorts(p, n, window int) []net.Listener {
	var guards []net.Listener
	release := func() []net.Listener {
		for _, g := range guards {
			g.Close()
		}
		return nil
	}
	for i := 0; i < window; i++ {
		g, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p+i+guardOffset))
		if err != nil {
			return release()
		}
		guards = append(guards, g)
	}
	// Only the first n ports are checked free: the rest of the window is guarded but may be in
	// use. A real runner at p+n..p+window-1 serves another simulator, which connect passes over
	// and ReleaseDevice leaves alone, so that is harmless.
	for i := 0; i < n; i++ {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p+i))
		if err != nil {
			return release()
		}
		ln.Close()
	}
	return guards
}

// udidFor returns a simulator UDID whose preferred port is port.
func udidFor(t *testing.T, port int) string {
	t.Helper()
	for i := 0; i < 1_000_000; i++ {
		u := fmt.Sprintf("TEST0000-0000-4000-8000-%012d", i)
		if preferredPort(u) == port {
			return u
		}
	}
	t.Fatalf("no udid maps to port %d", port)
	return ""
}

// Open refuses a device that is not an iOS simulator.
func TestOpenRejectsOtherPlatforms(t *testing.T) {
	if _, err := Open(ctxT(t), device.Info{Platform: device.Android, Serial: "emulator-5554"}); err == nil ||
		!strings.Contains(err.Error(), "emulator-5554 is not an iOS simulator") {
		t.Errorf("err = %v", err)
	}
}

// A runner of this build serving this simulator is reused: no xcodebuild, nothing stopped.
func TestOpenReusesRunner(t *testing.T) {
	for _, probe := range []string{"", "0"} {
		t.Run("TB_IOS_PROBE="+probe, func(t *testing.T) {
			ts := stubTools(t)
			t.Setenv("TB_IOS_PROBE", probe)
			port := freePorts(t, 1)
			udid := udidFor(t, port)
			r := newFakeRunnerAt(t, fmt.Sprintf("127.0.0.1:%d", port), udid, BuildID())
			d, err := Open(ctxT(t), device.Info{Platform: device.IOS, Serial: udid, Name: "iPhone"})
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			if d.Port() != port {
				t.Errorf("port %d, want %d", d.Port(), port)
			}
			if len(ts.calls("xcrun")) != 0 || len(r.requests("exit")) != 0 {
				t.Errorf("xcrun %v, exit %v", ts.calls("xcrun"), r.requests("exit"))
			}
			wantProbe := filepath.Join(d.dir, "Release-iphonesimulator", "libTBProbe.dylib")
			if probe == "0" {
				wantProbe = ""
			}
			if d.probe != wantProbe {
				t.Errorf("probe %q, want %q", d.probe, wantProbe)
			}
			if _, err := os.Stat(filepath.Join(d.dir, "tb-agent.xctestrun")); err != nil {
				t.Errorf("agent not unpacked: %v", err)
			}
		})
	}
}

// startsRunnerAt makes the stub xcodebuild bring up a fresh fake runner on port, as a real one
// would; configure, if not nil, adjusts it before it accepts connections' requests.
func startsRunnerAt(t *testing.T, ts *tools, port int, udid string, configure func(*fakeRunner)) chan *fakeRunner {
	marker := filepath.Join(t.TempDir(), "xcodebuild-ran")
	ready := marker + "-ready"
	// Like a real xcodebuild, the stub keeps running while its runner serves (an xcodebuild
	// that exits ends the dial): it waits, with builtins only, until the fake runner listens.
	ts.set("xcrun", []stubRule{{match: "xcodebuild", sh: ": > " + sq(marker) + "; i=0; while [ ! -e " + sq(ready) + " ] && [ $i -lt 3000000 ]; do i=$((i+1)); done;"}})
	started := make(chan *fakeRunner, 1)
	go func() {
		defer close(started)
		for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(5 * time.Millisecond) {
			if _, err := os.Stat(marker); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Errorf("xcodebuild was never started")
				return
			}
		}
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			t.Errorf("listening as the new runner: %v", err)
			return
		}
		r := serveRunner(t, ln, udid, BuildID(), configure)
		os.WriteFile(ready, nil, 0o644)
		started <- r
	}()
	return started
}

// A runner of another build (or one that does not say whose it is) on this simulator's port is
// told to exit, and a runner of this build is started there.
func TestConnectReplacesStaleRunner(t *testing.T) {
	for _, staleUDID := range []string{"self", ""} {
		t.Run("udid="+staleUDID, func(t *testing.T) {
			ts := stubTools(t)
			port := freePorts(t, 1)
			udid := udidFor(t, port)
			if staleUDID == "self" {
				staleUDID = udid
			}
			old := newFakeRunnerAt(t, fmt.Sprintf("127.0.0.1:%d", port), staleUDID, "0123456789ab")
			lingers := staleUDID == "" // this one keeps answering for a moment after exit
			old.on("exit", func(map[string]any) map[string]any {
				go func() {
					if lingers {
						time.Sleep(150 * time.Millisecond)
					}
					old.close()
				}()
				return map[string]any{"bye": true}
			})
			started := startsRunnerAt(t, ts, port, udid, nil)
			d, err := Open(ctxT(t), device.Info{Platform: device.IOS, Serial: udid, Name: "iPhone"})
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			fresh := <-started
			if fresh == nil {
				t.Fatal("no new runner")
			}
			if len(old.requests("exit")) != 1 {
				t.Errorf("the stale runner got %d exits", len(old.requests("exit")))
			}
			if d.Port() != port || len(fresh.requests("info")) != 1 {
				t.Errorf("port %d, fresh runner requests %v", d.Port(), fresh.ops())
			}
			xc := ts.calls("xcrun")
			want := []string{"xcodebuild", "test-without-building", "-xctestrun", filepath.Join(d.dir, "tb-agent.xctestrun"), "-destination", "id=" + udid}
			if len(xc) != 1 || !reflect.DeepEqual(xc[0], want) {
				t.Errorf("xcrun %q", xc)
			}
			if len(ts.calls("pgrep")) != 1 {
				t.Error("other runners for this simulator were not looked for")
			}
		})
	}
}

// Another simulator's runner on the preferred port (a hash collision) is left alone; the next port is used.
func TestConnectSkipsAnotherSimulatorsRunner(t *testing.T) {
	ts := stubTools(t)
	port := freePorts(t, 2)
	udid := udidFor(t, port)
	other := newFakeRunnerAt(t, fmt.Sprintf("127.0.0.1:%d", port), "SOMEONE-ELSE", BuildID())
	started := startsRunnerAt(t, ts, port+1, udid, nil)
	d, err := Open(ctxT(t), device.Info{Platform: device.IOS, Serial: udid})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	<-started
	if d.Port() != port+1 {
		t.Errorf("port %d, want %d", d.Port(), port+1)
	}
	if len(other.requests("exit")) != 0 {
		t.Error("another simulator's runner was told to exit")
	}
}

// When xcodebuild cannot be started, Open says so.
func TestConnectRunnerCannotStart(t *testing.T) {
	ts := stubTools(t)
	port := freePorts(t, 1)
	udid := udidFor(t, port)
	ts.hide()
	if _, err := Open(ctxT(t), device.Info{Platform: device.IOS, Serial: udid}); err == nil || !strings.Contains(err.Error(), "starting xcodebuild") {
		t.Errorf("err = %v", err)
	}
}

// A runner that starts but fails its first info is reported.
func TestConnectRunnerInfoFails(t *testing.T) {
	ts := stubTools(t)
	port := freePorts(t, 1)
	udid := udidFor(t, port)
	started := startsRunnerAt(t, ts, port, udid, func(r *fakeRunner) {
		r.on("info", func(map[string]any) map[string]any { return map[string]any{"ok": false, "error": "not ready"} })
	})
	if _, err := Open(ctxT(t), device.Info{Platform: device.IOS, Serial: udid}); err == nil || !strings.Contains(err.Error(), "not ready") {
		t.Errorf("err = %v", err)
	}
	<-started
}
