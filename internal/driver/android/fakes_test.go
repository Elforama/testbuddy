package android

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/driver"
)

const testSerial = "emulator-5554"

var noReply = map[string]any{"__noreply": true}

type agentConn struct {
	c   net.Conn
	wmu sync.Mutex
}

func (ac *agentConn) send(m map[string]any, bin []byte) error {
	if bin != nil {
		m["bin"] = len(bin)
	}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	ac.wmu.Lock()
	defer ac.wmu.Unlock()
	if _, err := ac.c.Write(append(b, '\n')); err != nil {
		return err
	}
	if bin != nil {
		_, err = ac.c.Write(bin)
	}
	return err
}

type fakeAgent struct {
	t  *testing.T
	ln net.Listener

	mu         sync.Mutex
	reqs       []map[string]any
	arrivedOps []string
	handlers   map[string]func(req map[string]any) map[string]any
	conns      []*agentConn
	hello      func(n int) map[string]any
	accepted   int
}

func newFakeAgent(t *testing.T) *fakeAgent {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	a := &fakeAgent{t: t, ln: ln, handlers: map[string]func(map[string]any) map[string]any{}}
	a.hello = func(int) map[string]any {
		return map[string]any{"ev": "hello", "platform": "android", "version": agentVersion(), "w": 1080, "h": 2400, "seq": 0}
	}
	a.on("info", func(map[string]any) map[string]any {
		return map[string]any{"w": 1080, "h": 2400, "sdk": 34, "density": 2.75, "keyboard": false}
	})
	a.on("ping", func(map[string]any) map[string]any { return map[string]any{"seq": 0} })
	a.on("sh", func(map[string]any) map[string]any { return map[string]any{"code": 0, "out": ""} })
	a.on("location", func(req map[string]any) map[string]any {
		if req["off"] == true {
			return map[string]any{"off": true}
		}
		return map[string]any{"providers": []string{"gps", "network", "fused"}}
	})
	go a.acceptLoop()
	t.Cleanup(a.close)
	return a
}

func (a *fakeAgent) greet(f func(n int) map[string]any) {
	a.mu.Lock()
	a.hello = f
	a.mu.Unlock()
}

func (a *fakeAgent) port() int { return a.ln.Addr().(*net.TCPAddr).Port }

func (a *fakeAgent) on(op string, h func(req map[string]any) map[string]any) {
	a.mu.Lock()
	a.handlers[op] = h
	a.mu.Unlock()
}

func (a *fakeAgent) acceptLoop() {
	for {
		c, err := a.ln.Accept()
		if err != nil {
			return
		}
		ac := &agentConn{c: c}
		a.mu.Lock()
		a.accepted++
		n := a.accepted
		a.conns = append(a.conns, ac)
		hello := a.hello(n)
		a.mu.Unlock()
		go func() {
			if hello != nil {
				ac.send(hello, nil)
			}
			a.serve(ac)
		}()
	}
}

func (a *fakeAgent) serve(ac *agentConn) {
	lines := make(chan []byte, 64)
	go func() {
		defer close(lines)
		r := bufio.NewReader(ac.c)
		for {
			line, err := r.ReadBytes('\n')
			if err != nil {
				return
			}
			var req map[string]any
			if json.Unmarshal(line, &req) == nil {
				a.mu.Lock()
				a.arrivedOps = append(a.arrivedOps, fmt.Sprint(req["op"]))
				a.mu.Unlock()
			}
			lines <- line
		}
	}()
	for line := range lines {
		var req map[string]any
		if json.Unmarshal(line, &req) != nil {
			continue
		}
		op, _ := req["op"].(string)
		a.mu.Lock()
		a.reqs = append(a.reqs, req)
		h := a.handlers[op]
		a.mu.Unlock()
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
		ac.send(out, bin)
	}
}

func (a *fakeAgent) push(ev map[string]any) {
	a.mu.Lock()
	var ac *agentConn
	if len(a.conns) > 0 {
		ac = a.conns[len(a.conns)-1]
	}
	a.mu.Unlock()
	if ac == nil {
		a.t.Fatal("push: no connection")
	}
	ac.send(ev, nil)
}

func (a *fakeAgent) dropConns() {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, ac := range a.conns {
		ac.c.Close()
	}
	a.conns = nil
}

func (a *fakeAgent) close() {
	a.ln.Close()
	a.dropConns()
}

func (a *fakeAgent) connCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.accepted
}

func (a *fakeAgent) requests(op string) []map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []map[string]any
	for _, r := range a.reqs {
		if op == "" || r["op"] == op {
			out = append(out, r)
		}
	}
	return out
}

func (a *fakeAgent) last(op string) map[string]any {
	a.t.Helper()
	rs := a.requests(op)
	if len(rs) == 0 {
		a.t.Fatalf("no %q request (got %v)", op, a.ops())
	}
	return rs[len(rs)-1]
}

func (a *fakeAgent) ops() []string {
	var out []string
	for _, r := range a.requests("") {
		out = append(out, fmt.Sprint(r["op"]))
	}
	return out
}

func (a *fakeAgent) shCmds() []string {
	var out []string
	for _, r := range a.requests("sh") {
		out = append(out, fmt.Sprint(r["cmd"]))
	}
	return out
}

func (a *fakeAgent) arrived(op string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	for _, o := range a.arrivedOps {
		if o == op {
			n++
		}
	}
	return n
}

func (a *fakeAgent) reset() {
	a.mu.Lock()
	a.reqs = nil
	a.mu.Unlock()
}

func num(t *testing.T, req map[string]any, k string) float64 {
	t.Helper()
	v, ok := req[k].(float64)
	if !ok {
		t.Fatalf("request %v: %q is %T, want a number", req, k, req[k])
	}
	return v
}

func newDriver(t *testing.T, a *fakeAgent) *Driver {
	t.Helper()
	if _, set := os.LookupEnv("TB_NO_A11Y_SERVICE"); !set {
		t.Setenv("TB_NO_A11Y_SERVICE", "1")
	}
	d := bareDriver(&device.ADBDevice{Serial: testSerial})
	c, h, err := dialHello(a.port(), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	d.adopt(c, a.port(), h)
	t.Cleanup(func() { d.Close() })
	return d
}

func bareDriver(adb *device.ADBDevice) *Driver {
	return &Driver{adb: adb, info: device.Info{Platform: device.Android, Serial: testSerial, Name: "Pixel Test", Emulator: true},
		launch: map[string]string{}, subs: map[int]chan driver.Event{}}
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
		case e := <-ch:
			if e.Kind == kind {
				return e
			}
		case <-timeout:
			t.Fatalf("no %q event", kind)
		}
	}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

type stubRule struct {
	match string
	out   string
	code  int
	sh    string
}

func sq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

var stubRoot string

func TestMain(m *testing.M) {
	if os.Getenv(killedTBEnv) != "" {
		os.Exit(m.Run())
	}
	dir, err := os.MkdirTemp("", "tb-driver-stubs-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	stubRoot = dir
	for _, p := range stubPaths() {
		script := "#!/bin/sh\n" +
			"[ -n \"$TB_STUB_DIR\" ] || exit 97\n" +
			"rec=''; for a in \"$@\"; do rec=\"$rec$a\x1f\"; done; printf '%s\x1e' \"$rec\" >> \"$TB_STUB_DIR/" + filepath.Base(p) + ".log\"\n" +
			"if [ -f \"$TB_STUB_DIR/" + filepath.Base(p) + ".rules\" ]; then . \"$TB_STUB_DIR/" + filepath.Base(p) + ".rules\"; fi\n" +
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
	if err := os.Rename(p+".tmp", p); err != nil {
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

func stubPaths() []string {
	return []string{filepath.Join(stubRoot, "sdk", "platform-tools", "adb"), filepath.Join(stubRoot, "bin", "pgrep")}
}

type adbStub struct {
	t    *testing.T
	path string
	dir  string
	log  string
}

func stubADB(t *testing.T, rules []stubRule) *adbStub {
	t.Helper()
	dir := t.TempDir()
	s := &adbStub{t: t, path: stubPaths()[0], dir: dir, log: filepath.Join(dir, "adb.log")}
	t.Setenv("TB_STUB_DIR", dir)
	t.Setenv("ANDROID_HOME", filepath.Join(stubRoot, "sdk"))
	t.Setenv("ANDROID_SDK_ROOT", "")
	t.Setenv("PATH", filepath.Join(stubRoot, "bin"))
	t.Setenv("TMPDIR", t.TempDir())
	if got := device.ADB(); got != s.path {
		t.Fatalf("adb resolves to %q, not the stub %q: refusing to run", got, s.path)
	}
	s.rules(rules)
	return s
}

func (s *adbStub) rules(rules []stubRule) { writeRules(s.t, s.dir, "adb", rules) }

func (s *adbStub) tool(name string, rules []stubRule) string {
	writeRules(s.t, s.dir, name, rules)
	return filepath.Join(s.dir, name+".log")
}

func (s *adbStub) calls() [][]string { return readStubLog(s.t, s.log) }

func (s *adbStub) joined() []string {
	var out []string
	for _, c := range s.calls() {
		out = append(out, strings.Join(c, " "))
	}
	return out
}

func (s *adbStub) driver() *Driver { return bareDriver(device.NewADB(testSerial)) }

func containsAny(list []string, sub string) bool {
	for _, s := range list {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
