package ios

// Test doubles: a fake XCUITest runner speaking the JSON-lines protocol (docs/protocol.md) on a
// loopback port, and stub xcrun/sqlite3/pgrep scripts on a PATH that holds nothing else. No
// simulator, xcrun or xcodebuild is ever run.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/driver"
)

const testUDID = "4C6F0D2E-0000-4000-8000-TESTUDID0001"

var noReply = map[string]any{"__noreply": true}

type runnerConn struct {
	c   net.Conn
	wmu sync.Mutex
}

func (rc *runnerConn) send(m map[string]any, bin []byte) error {
	if bin != nil {
		m["bin"] = len(bin)
	}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	rc.wmu.Lock()
	defer rc.wmu.Unlock()
	if _, err := rc.c.Write(append(b, '\n')); err != nil {
		return err
	}
	if bin != nil {
		_, err = rc.c.Write(bin)
	}
	return err
}

// fakeRunner is the iOS runner: a greeting, recorded requests, per-op handlers.
type fakeRunner struct {
	t  *testing.T
	ln net.Listener

	mu       sync.Mutex
	reqs     []map[string]any
	handlers map[string]func(req map[string]any) map[string]any
	conns    []*runnerConn
	hello    map[string]any
}

func newFakeRunner(t *testing.T) *fakeRunner {
	return newFakeRunnerAt(t, "127.0.0.1:0", testUDID, BuildID())
}

// newFakeRunnerAt serves addr as the runner of udid built as build.
func newFakeRunnerAt(t *testing.T, addr, udid, build string) *fakeRunner {
	t.Helper()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	return serveRunner(t, ln, udid, build, nil)
}

// serveRunner runs a fake runner on ln (safe to call from any goroutine); configure, if not nil,
// adjusts it before it accepts connections.
func serveRunner(t *testing.T, ln net.Listener, udid, build string, configure func(*fakeRunner)) *fakeRunner {
	r := &fakeRunner{t: t, ln: ln, handlers: map[string]func(map[string]any) map[string]any{},
		hello: map[string]any{"ev": "hello", "platform": "ios", "version": build, "build": build, "udid": udid, "w": 393, "h": 852, "seq": 0}}
	r.on("info", func(map[string]any) map[string]any {
		return map[string]any{"w": 393, "h": 852, "udid": udid, "build": build, "agent": "test", "seq": 0}
	})
	r.on("ping", func(map[string]any) map[string]any { return nil })
	if configure != nil {
		configure(r)
	}
	go r.acceptLoop()
	t.Cleanup(r.close)
	return r
}

func (r *fakeRunner) port() int { return r.ln.Addr().(*net.TCPAddr).Port }

func (r *fakeRunner) on(op string, h func(req map[string]any) map[string]any) {
	r.mu.Lock()
	r.handlers[op] = h
	r.mu.Unlock()
}

func (r *fakeRunner) setHello(h map[string]any) {
	r.mu.Lock()
	r.hello = h
	r.mu.Unlock()
}

func (r *fakeRunner) acceptLoop() {
	for {
		c, err := r.ln.Accept()
		if err != nil {
			return
		}
		rc := &runnerConn{c: c}
		r.mu.Lock()
		r.conns = append(r.conns, rc)
		hello := map[string]any{}
		for k, v := range r.hello {
			hello[k] = v
		}
		r.mu.Unlock()
		go func() {
			rc.send(hello, nil)
			r.serve(rc)
		}()
	}
}

func (r *fakeRunner) serve(rc *runnerConn) {
	br := bufio.NewReader(rc.c)
	for {
		line, err := br.ReadBytes('\n')
		if err != nil {
			return
		}
		var req map[string]any
		if json.Unmarshal(line, &req) != nil {
			continue
		}
		op, _ := req["op"].(string)
		r.mu.Lock()
		r.reqs = append(r.reqs, req)
		h := r.handlers[op]
		r.mu.Unlock()
		var res map[string]any
		if h != nil {
			res = h(req)
		}
		if res != nil && res["__noreply"] == true {
			continue
		}
		out := map[string]any{"id": req["id"], "ok": true}
		var bin []byte
		for k, v := range res {
			if k == "_bin" {
				bin = v.([]byte)
				continue
			}
			out[k] = v
		}
		if h == nil {
			out["ok"] = false
			out["error"] = fmt.Sprintf("unknown op %q", op)
		}
		rc.send(out, bin)
	}
}

func (r *fakeRunner) push(ev map[string]any) {
	r.mu.Lock()
	var rc *runnerConn
	if len(r.conns) > 0 {
		rc = r.conns[len(r.conns)-1]
	}
	r.mu.Unlock()
	if rc == nil {
		r.t.Fatal("push: no connection")
	}
	rc.send(ev, nil)
}

func (r *fakeRunner) dropConns() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, rc := range r.conns {
		rc.c.Close()
	}
	r.conns = nil
}

func (r *fakeRunner) close() {
	r.ln.Close()
	r.dropConns()
}

func (r *fakeRunner) requests(op string) []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []map[string]any
	for _, q := range r.reqs {
		if op == "" || q["op"] == op {
			out = append(out, q)
		}
	}
	return out
}

func (r *fakeRunner) last(op string) map[string]any {
	r.t.Helper()
	rs := r.requests(op)
	if len(rs) == 0 {
		r.t.Fatalf("no %q request (got %v)", op, r.ops())
	}
	return rs[len(rs)-1]
}

func (r *fakeRunner) ops() []string {
	var out []string
	for _, q := range r.requests("") {
		out = append(out, fmt.Sprint(q["op"]))
	}
	return out
}

func (r *fakeRunner) reset() {
	r.mu.Lock()
	r.reqs = nil
	r.mu.Unlock()
}

func num(t *testing.T, req map[string]any, k string) float64 {
	t.Helper()
	v, ok := req[k].(float64)
	if !ok {
		t.Fatalf("request %v: %q is %T, want a number", req, k, req[k])
	}
	return v
}

func testInfo() device.Info {
	return device.Info{Platform: device.IOS, Serial: testUDID, Name: "iPhone Test", Emulator: true}
}

// newIOSDriver connects a Driver to the fake runner the way connect does (probePort + adopt).
func newIOSDriver(t *testing.T, r *fakeRunner) *Driver {
	t.Helper()
	d := &Driver{info: testInfo(), probe: "/agent/Release-iphonesimulator/libTBProbe.dylib", dir: t.TempDir()}
	c, in := probePort(r.port())
	if c == nil {
		t.Fatal("probePort found no runner")
	}
	d.adopt(c, r.port(), in)
	t.Cleanup(func() { d.Close() })
	return d
}

func ctxT(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func waitEvent(t *testing.T, ch <-chan driver.Event, kind string) driver.Event {
	t.Helper()
	timeout := time.After(3 * time.Second)
	for {
		select {
		case e, ok := <-ch:
			if !ok {
				t.Fatalf("events closed before %q", kind)
			}
			if e.Kind == kind {
				return e
			}
		case <-timeout:
			t.Fatalf("no %q event", kind)
		}
	}
}

// ---- stub tools ----

// stubRule answers an invocation whose space-joined arguments contain match.
type stubRule struct {
	match string
	out   string
	code  int
	sh    string // extra shell run before answering
}

func sq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// The stub executables are written once per test binary (macOS scans every new executable, which
// costs 20-150 ms per fresh script). Each one logs its arguments to $TB_STUB_DIR/<name>.log and
// sources $TB_STUB_DIR/<name>.rules, the current test's answers. Only shell builtins run.
var stubRoot string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "tb-driver-stubs-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	stubRoot = dir
	for _, p := range stubPaths() {
		script := "#!/bin/sh\n" +
			"[ -n \"$TB_STUB_DIR\" ] || exit 97\n" +
			// One write per invocation (O_APPEND), so concurrent invocations do not interleave.
			"rec=''; for a in \"$@\"; do rec=\"$rec$a\x1f\"; done; printf '%s\x1e' \"$rec\" >> \"$TB_STUB_DIR/" + filepath.Base(p) + ".log\"\n" +
			"if [ -f \"$TB_STUB_DIR/" + filepath.Base(p) + ".rules\" ]; then . \"$TB_STUB_DIR/" + filepath.Base(p) + ".rules\"; fi\n" +
			// simctl launch [options] <device> <app>: answer with the app and pid 4242.
			"if [ \"$1 $2\" = \"simctl launch\" ]; then shift 2; while [ \"${1#-}\" != \"$1\" ]; do shift; done; printf '%s: 4242\\n' \"$2\"; fi\n" +
			"exit 0\n"
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err == nil {
			err = os.WriteFile(p, []byte(script), 0o755)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// writeRules sets the answers of stub name for the test using dir as $TB_STUB_DIR: the first
// rule whose match is in the arguments prints its out and exits with its code; else nothing, 0.
func writeRules(t *testing.T, dir, name string, rules []stubRule) {
	t.Helper()
	var b strings.Builder
	b.WriteString("case \"$*\" in\n")
	for _, r := range rules {
		fmt.Fprintf(&b, "*%s*) %s printf '%%s' %s; exit %d;;\n", sq(r.match), r.sh, sq(r.out), r.code)
	}
	b.WriteString("esac\n")
	p := filepath.Join(dir, name+".rules")
	if err := os.WriteFile(p+".tmp", []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(p+".tmp", p); err != nil { // atomic: a rewrite mid-test never runs half the rules
		t.Fatal(err)
	}
}

func readStubLog(t *testing.T, log string) [][]string {
	t.Helper()
	b, err := os.ReadFile(log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out [][]string
	for _, rec := range strings.Split(string(b), "\x1e") {
		if rec == "" {
			continue
		}
		out = append(out, strings.Split(strings.TrimSuffix(rec, "\x1f"), "\x1f"))
	}
	return out
}

var stubNames = []string{"xcrun", "sqlite3", "pgrep"}

func stubPaths() []string {
	var out []string
	for _, n := range stubNames {
		out = append(out, filepath.Join(stubRoot, "bin", n))
	}
	return out
}

// tools is a PATH of stub commands only (xcrun, sqlite3, pgrep), so the real ones cannot run,
// and a temporary HOME (the TCC database, the agent cache).
type tools struct {
	t    *testing.T
	dir  string // $TB_STUB_DIR: this test's rules and logs
	home string
}

func stubTools(t *testing.T) *tools {
	t.Helper()
	ts := &tools{t: t, dir: t.TempDir(), home: t.TempDir()}
	t.Setenv("TB_STUB_DIR", ts.dir)
	t.Setenv("PATH", filepath.Join(stubRoot, "bin"))
	t.Setenv("HOME", ts.home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(ts.home, ".cache"))
	for _, name := range stubNames {
		if p, err := exec.LookPath(name); err != nil || filepath.Dir(p) != filepath.Join(stubRoot, "bin") {
			t.Fatalf("%s resolves to %q (%v), not the stub: refusing to run", name, p, err)
		}
	}
	return ts
}

func (ts *tools) set(name string, rules []stubRule) { writeRules(ts.t, ts.dir, name, rules) }

// hide empties PATH: no xcrun, sqlite3 or pgrep at all.
func (ts *tools) hide() { ts.t.Setenv("PATH", ts.t.TempDir()) }

// realSQLite puts the system's sqlite3 (found before stubTools) first on PATH.
func (ts *tools) realSQLite(sqlite string) {
	bin := ts.t.TempDir()
	if err := os.Symlink(sqlite, filepath.Join(bin, "sqlite3")); err != nil {
		ts.t.Fatal(err)
	}
	ts.t.Setenv("PATH", bin+string(os.PathListSeparator)+filepath.Join(stubRoot, "bin"))
}

func (ts *tools) calls(name string) [][]string {
	return readStubLog(ts.t, filepath.Join(ts.dir, name+".log"))
}

// simctl returns each xcrun invocation's arguments after "simctl", space-joined.
func (ts *tools) simctl() []string {
	var out []string
	for _, c := range ts.calls("xcrun") {
		if len(c) > 0 && c[0] == "simctl" {
			out = append(out, strings.Join(c[1:], " "))
		}
	}
	return out
}

// sql returns the SQL of each sqlite3 invocation (its last argument).
func (ts *tools) sql() []string {
	var out []string
	for _, c := range ts.calls("sqlite3") {
		out = append(out, c[len(c)-1])
	}
	return out
}
