package daemon

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const childEnv = "TB_DAEMON_TEST_CHILD"

// TestMain guards against Connect's spawn path: it starts os.Executable() (this test
// binary) as "<exe> daemon"; such a child must exit at once instead of running the tests.
func TestMain(m *testing.M) {
	if (len(os.Args) > 1 && os.Args[1] == "daemon") || os.Getenv(childEnv) == "1" {
		os.Exit(0)
	}
	log.SetOutput(io.Discard) // the server logs start-up and panics
	os.Exit(m.Run())
}

const wait = 5 * time.Second

// home points TB_HOME at a fresh short temp dir (unix socket paths are length-limited).
func home(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "tbd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv("TB_HOME", dir)
	return dir
}

// serve starts a server with the given handlers and stops it when the test ends.
func serve(t *testing.T, handlers map[string]Handler) *Server {
	t.Helper()
	s := NewServer(time.Hour)
	for k, h := range handlers {
		s.Handle(k, h)
	}
	done := make(chan error, 1)
	go func() { done <- s.Serve() }()
	deadline := time.Now().Add(wait)
	for !Running() {
		select {
		case err := <-done:
			t.Fatalf("Serve returned early: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("server did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Cleanup(func() {
		s.Stop()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Serve returned %v", err)
			}
		case <-time.After(wait):
			t.Error("Serve did not return after Stop")
		}
	})
	return s
}

func dial(t *testing.T) *Client {
	t.Helper()
	c, err := net.Dial("unix", SocketPath())
	if err != nil {
		t.Fatal(err)
	}
	cl := newClient(c)
	t.Cleanup(func() { cl.Close() })
	return cl
}

// Dir is TB_HOME when set, else ~/.testbuddy.
func TestDir(t *testing.T) {
	t.Setenv("TB_HOME", "/custom/state")
	if Dir() != "/custom/state" {
		t.Errorf("Dir = %q", Dir())
	}
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("TB_HOME", "")
	if want := filepath.Join(h, ".testbuddy"); Dir() != want {
		t.Errorf("Dir = %q, want %q", Dir(), want)
	}
}

// The log lives in the state directory.
func TestLogPath(t *testing.T) {
	t.Setenv("TB_HOME", "/state")
	if LogPath() != "/state/daemon.log" {
		t.Errorf("LogPath = %q", LogPath())
	}
}

func hashedSocket(dir string) string {
	sum := sha256.Sum256([]byte(dir))
	return filepath.Join(os.TempDir(), "tb-"+hex.EncodeToString(sum[:6])+".sock")
}

// SocketPath is <dir>/tb.sock while that is under 100 bytes, else a temp-dir socket named by a
// hash of the directory.
func TestSocketPath(t *testing.T) {
	dirOfLen := func(total int) string { // a clean dir whose <dir>/tb.sock is total bytes long
		return "/" + strings.Repeat("d", total-len("/tb.sock")-1)
	}
	cases := []struct {
		name, dir, want string
	}{
		{"short", "/tmp/tbhome", "/tmp/tbhome/tb.sock"},
		{"99 bytes stays", dirOfLen(99), dirOfLen(99) + "/tb.sock"},
		{"100 bytes is hashed", dirOfLen(100), hashedSocket(dirOfLen(100))},
		{"very long is hashed", "/" + strings.Repeat("x/", 120), hashedSocket("/" + strings.Repeat("x/", 120))},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("TB_HOME", c.dir)
			if got := SocketPath(); got != c.want {
				t.Errorf("SocketPath = %q, want %q", got, c.want)
			}
		})
	}
}

// The hashed fallback is stable for a directory, distinct between directories, and short.
func TestSocketPathHashedFallback(t *testing.T) {
	long := "/" + strings.Repeat("long-directory-name/", 8)
	t.Setenv("TB_HOME", long)
	a := SocketPath()
	if SocketPath() != a {
		t.Error("hashed socket path is not stable")
	}
	if !regexp.MustCompile(`^tb-[0-9a-f]{12}\.sock$`).MatchString(filepath.Base(a)) {
		t.Errorf("hashed name = %q", filepath.Base(a))
	}
	if filepath.Dir(a) != filepath.Clean(os.TempDir()) {
		t.Errorf("hashed socket dir = %q, want the temp dir", filepath.Dir(a))
	}
	if len(a) >= 104 {
		t.Errorf("hashed socket path is %d bytes, over the unix limit", len(a))
	}
	t.Setenv("TB_HOME", long+"other")
	if SocketPath() == a {
		t.Error("different directories share a hashed socket")
	}
}

// BuildID identifies the running binary as "<size>-<mtime nanos>".
func TestBuildID(t *testing.T) {
	id := BuildID()
	if !regexp.MustCompile(`^[0-9]+-[0-9]+$`).MatchString(id) {
		t.Fatalf("BuildID = %q", id)
	}
	if computeBuildID() != id {
		t.Error("BuildID changed while the binary did not")
	}
	exe, _ := os.Executable()
	st, _ := os.Stat(exe)
	if want := fmt.Sprintf("%d-%d", st.Size(), st.ModTime().UnixNano()); id != want {
		t.Errorf("BuildID = %q, want %q", id, want)
	}
}

// Running is false with nothing on the socket and true while a server listens.
func TestRunning(t *testing.T) {
	home(t)
	if Running() {
		t.Fatal("Running with no server")
	}
	serve(t, nil)
	if !Running() {
		t.Error("not Running while serving")
	}
}

// A request reaches its handler with args and cwd, and the response comes back.
func TestServeCall(t *testing.T) {
	home(t)
	serve(t, map[string]Handler{
		"echo": func(ctx context.Context, req Request, send func(Response)) Response {
			if ctx == nil || ctx.Err() != nil {
				return Response{Exit: 9, Text: "bad context"}
			}
			return Response{OK: true, Text: req.Cmd + "|" + string(req.Args) + "|" + req.Cwd,
				JSON: json.RawMessage(`{"k":1}`), Image: []byte{0, 1, 2}}
		},
	})
	cl := dial(t)
	resp, err := cl.Call(Request{Cmd: "echo", Args: json.RawMessage(`{"device":"alice"}`), Cwd: "/work"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK || resp.Exit != 0 || resp.Text != `echo|{"device":"alice"}|/work` {
		t.Errorf("resp = %+v", resp)
	}
	if string(resp.JSON) != `{"k":1}` || string(resp.Image) != "\x00\x01\x02" || resp.More {
		t.Errorf("resp payload = %+v", resp)
	}
}

// Streamed parts arrive in order with More set; the final response never has More.
func TestServeStreaming(t *testing.T) {
	home(t)
	serve(t, map[string]Handler{
		"watch": func(ctx context.Context, req Request, send func(Response)) Response {
			for i := 1; i <= 3; i++ {
				send(Response{Text: fmt.Sprintf("part %d\n", i)})
			}
			return Response{OK: true, Text: "done", More: true} // More is cleared on the final one
		},
	})
	cl := dial(t)
	var parts []string
	resp, err := cl.Call(Request{Cmd: "watch"}, func(r Response) {
		if !r.More {
			t.Error("part without More")
		}
		parts = append(parts, r.Text)
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(parts, "") != "part 1\npart 2\npart 3\n" {
		t.Errorf("parts = %q", parts)
	}
	if resp.Text != "done" || resp.More || !resp.OK {
		t.Errorf("final = %+v", resp)
	}
	// Without an onPart callback the parts are skipped.
	resp, err = cl.Call(Request{Cmd: "watch"}, nil)
	if err != nil || resp.Text != "done" {
		t.Errorf("nil onPart: %+v %v", resp, err)
	}
}

// Unknown commands answer exit 2 and the connection stays usable.
func TestServeUnknownCommand(t *testing.T) {
	home(t)
	serve(t, map[string]Handler{"ok": func(context.Context, Request, func(Response)) Response { return Response{OK: true, Text: "fine"} }})
	cl := dial(t)
	resp, err := cl.Call(Request{Cmd: "nope"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Exit != 2 || resp.OK || resp.Text != `unknown command "nope"` {
		t.Errorf("resp = %+v", resp)
	}
	if resp, err := cl.Call(Request{Cmd: "ok"}, nil); err != nil || resp.Text != "fine" {
		t.Errorf("after unknown: %+v %v", resp, err)
	}
}

// A line that is not a request answers exit 2 "bad request" and the connection stays usable.
func TestServeBadRequest(t *testing.T) {
	home(t)
	serve(t, map[string]Handler{"ok": func(context.Context, Request, func(Response)) Response { return Response{OK: true, Text: "fine"} }})
	c, err := net.Dial("unix", SocketPath())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(wait))
	r := bufio.NewReader(c)
	io.WriteString(c, "this is not json\n")
	line, err := r.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var resp Response
	json.Unmarshal(line, &resp)
	if resp.Exit != 2 || !strings.HasPrefix(resp.Text, "bad request: ") {
		t.Errorf("resp = %+v", resp)
	}
	io.WriteString(c, `{"cmd":"ok"}`+"\n")
	line, _ = r.ReadBytes('\n')
	json.Unmarshal(line, &resp)
	if resp.Text != "fine" {
		t.Errorf("after bad request: %s", line)
	}
}

// A panicking handler answers exit 3 "internal error" instead of killing the daemon.
func TestServeHandlerPanic(t *testing.T) {
	home(t)
	serve(t, map[string]Handler{
		"boom": func(context.Context, Request, func(Response)) Response { panic("kaboom") },
		"ok":   func(context.Context, Request, func(Response)) Response { return Response{OK: true, Text: "fine"} },
	})
	cl := dial(t)
	resp, err := cl.Call(Request{Cmd: "boom"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Exit != 3 || resp.Text != "internal error: kaboom" || resp.More {
		t.Errorf("resp = %+v", resp)
	}
	if resp, err := cl.Call(Request{Cmd: "ok"}, nil); err != nil || resp.Text != "fine" {
		t.Errorf("after panic: %+v %v", resp, err)
	}
	if !Running() {
		t.Error("daemon died after a handler panic")
	}
}

// One connection serves many requests in turn; many clients are served at once.
func TestServeManyRequestsAndClients(t *testing.T) {
	home(t)
	var calls atomic.Int64
	serve(t, map[string]Handler{
		"n": func(_ context.Context, req Request, _ func(Response)) Response {
			calls.Add(1)
			return Response{OK: true, Text: string(req.Args)}
		},
	})
	cl := dial(t)
	for i := 0; i < 5; i++ {
		resp, err := cl.Call(Request{Cmd: "n", Args: json.RawMessage(fmt.Sprint(i))}, nil)
		if err != nil || resp.Text != fmt.Sprint(i) {
			t.Fatalf("request %d: %+v %v", i, resp, err)
		}
	}
	var wg sync.WaitGroup
	for g := 0; g < 10; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			c, err := net.Dial("unix", SocketPath())
			if err != nil {
				t.Error(err)
				return
			}
			cl := newClient(c)
			defer cl.Close()
			for i := 0; i < 5; i++ {
				want := fmt.Sprint(g*100 + i)
				resp, err := cl.Call(Request{Cmd: "n", Args: json.RawMessage(want)}, nil)
				if err != nil || resp.Text != want {
					t.Errorf("client %d: %+v %v", g, resp, err)
					return
				}
			}
		}(g)
	}
	wg.Wait()
	if calls.Load() != 55 {
		t.Errorf("handler calls = %d", calls.Load())
	}
}

// A second daemon on the same socket refuses to start.
func TestServeAlreadyRunning(t *testing.T) {
	home(t)
	serve(t, nil)
	if err := NewServer(time.Hour).Serve(); err == nil || err.Error() != "a tb daemon is already running" {
		t.Errorf("second Serve = %v", err)
	}
}

// Serve creates the state dir, replaces a stale socket file and makes the socket owner-only.
func TestServeSetup(t *testing.T) {
	base := home(t)
	nested := filepath.Join(base, "a", "b")
	t.Setenv("TB_HOME", nested)
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(SocketPath(), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	os.RemoveAll(nested) // and let Serve create the directory itself
	os.MkdirAll(filepath.Dir(nested), 0o700)
	serve(t, nil)
	st, err := os.Stat(nested)
	if err != nil || !st.IsDir() {
		t.Fatalf("state dir not created: %v", err)
	}
	sock, err := os.Stat(SocketPath())
	if err != nil {
		t.Fatal(err)
	}
	if sock.Mode()&os.ModeSocket == 0 {
		t.Errorf("socket path is %v, not a socket", sock.Mode())
	}
	if sock.Mode().Perm() != 0o600 {
		t.Errorf("socket mode = %v, want 0600", sock.Mode().Perm())
	}
}

// A stale regular file at the socket path is replaced by the new socket.
func TestServeReplacesStaleSocket(t *testing.T) {
	home(t)
	if err := os.WriteFile(SocketPath(), []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	serve(t, nil)
	if st, err := os.Stat(SocketPath()); err != nil || st.Mode()&os.ModeSocket == 0 {
		t.Errorf("socket not replaced: %v %v", st, err)
	}
}

// Serve fails when the state dir cannot be created.
func TestServeBadDir(t *testing.T) {
	base := home(t)
	file := filepath.Join(base, "file")
	os.WriteFile(file, nil, 0o600)
	t.Setenv("TB_HOME", filepath.Join(file, "sub"))
	if err := NewServer(time.Hour).Serve(); err == nil {
		t.Error("Serve succeeded under a regular file")
	}
}

// Serve fails when it cannot listen in the state dir (here: a read-only directory).
func TestServeListenError(t *testing.T) {
	dir := home(t)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	if err := NewServer(time.Hour).Serve(); err == nil {
		t.Error("Serve succeeded in a read-only directory")
	}
}

// A client that leaves before its answer is written does not take the daemon down.
func TestServeClientGoneBeforeAnswer(t *testing.T) {
	home(t)
	started, release := make(chan struct{}), make(chan struct{})
	serve(t, map[string]Handler{
		"slow": func(context.Context, Request, func(Response)) Response {
			close(started)
			<-release
			return Response{OK: true, Text: strings.Repeat("x", 1<<20)}
		},
		"ok": func(context.Context, Request, func(Response)) Response { return Response{OK: true, Text: "fine"} },
	})
	c, err := net.Dial("unix", SocketPath())
	if err != nil {
		t.Fatal(err)
	}
	io.WriteString(c, `{"cmd":"slow"}`+"\n")
	<-started
	c.Close()
	time.Sleep(20 * time.Millisecond)
	close(release)
	cl := dial(t)
	if resp, err := cl.Call(Request{Cmd: "ok"}, nil); err != nil || resp.Text != "fine" {
		t.Errorf("after a client left: %+v %v", resp, err)
	}
}

// Stop makes Serve return nil, removes the socket, and is safe to call twice.
func TestStop(t *testing.T) {
	home(t)
	s := NewServer(time.Hour)
	done := make(chan error, 1)
	go func() { done <- s.Serve() }()
	for i := 0; !Running(); i++ {
		if i > 1000 {
			t.Fatal("server did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	s.Stop()
	s.Stop()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve = %v", err)
		}
	case <-time.After(wait):
		t.Fatal("Serve did not return")
	}
	if _, err := os.Stat(SocketPath()); !os.IsNotExist(err) {
		t.Errorf("socket left behind: %v", err)
	}
	if Running() {
		t.Error("still Running after Stop")
	}
}

// On exit the daemon removes the socket only if it is still its own (a replacement daemon may
// have created its own at the same path).
func TestStopKeepsReplacementSocket(t *testing.T) {
	home(t)
	s := NewServer(time.Hour)
	done := make(chan error, 1)
	go func() { done <- s.Serve() }()
	for i := 0; !Running(); i++ {
		if i > 1000 {
			t.Fatal("server did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	os.Remove(SocketPath())
	if err := os.WriteFile(SocketPath(), []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.Stop()
	<-done
	b, err := os.ReadFile(SocketPath())
	if err != nil || string(b) != "replacement" {
		t.Errorf("replacement socket changed: %q %v", b, err)
	}
}

// fakeDaemon accepts one connection on the socket and runs fn on it.
func fakeDaemon(t *testing.T, fn func(net.Conn)) {
	t.Helper()
	ln, err := net.Listen("unix", SocketPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		fn(c)
	}()
}

// A daemon that hangs up mid-call is reported as lost.
func TestClientLostDaemon(t *testing.T) {
	home(t)
	fakeDaemon(t, func(c net.Conn) {
		bufio.NewReader(c).ReadBytes('\n')
		io.WriteString(c, `{"ok":true,"more":true,"text":"partial"}`+"\n")
	})
	cl := dial(t)
	var parts int
	_, err := cl.Call(Request{Cmd: "x"}, func(Response) { parts++ })
	if err == nil || !strings.HasPrefix(err.Error(), "lost the tb daemon: ") {
		t.Errorf("err = %v", err)
	}
	if parts != 1 {
		t.Errorf("parts before loss = %d", parts)
	}
}

// A garbled answer is a decode error.
func TestClientBadResponse(t *testing.T) {
	home(t)
	fakeDaemon(t, func(c net.Conn) {
		bufio.NewReader(c).ReadBytes('\n')
		io.WriteString(c, "garbage\n")
	})
	cl := dial(t)
	if _, err := cl.Call(Request{Cmd: "x"}, nil); err == nil || strings.Contains(err.Error(), "lost") {
		t.Errorf("err = %v, want a decode error", err)
	}
}

// The client sends exactly one JSON line per request, omitting empty args and cwd.
func TestClientRequestLine(t *testing.T) {
	home(t)
	got := make(chan string, 1)
	fakeDaemon(t, func(c net.Conn) {
		line, _ := bufio.NewReader(c).ReadBytes('\n')
		got <- string(line)
		io.WriteString(c, `{"ok":true}`+"\n")
	})
	cl := dial(t)
	if _, err := cl.Call(Request{Cmd: "ping"}, nil); err != nil {
		t.Fatal(err)
	}
	if line := <-got; line != `{"cmd":"ping"}`+"\n" {
		t.Errorf("request line = %q", line)
	}
}

// Calling on a closed client fails on the write.
func TestClientClosed(t *testing.T) {
	home(t)
	serve(t, nil)
	c, err := net.Dial("unix", SocketPath())
	if err != nil {
		t.Fatal(err)
	}
	cl := newClient(c)
	cl.Close()
	if _, err := cl.Call(Request{Cmd: "ping"}, nil); err == nil {
		t.Error("Call on a closed client succeeded")
	}
}

// Responses marshal compactly: zero fields are omitted except ok and exit.
func TestResponseJSON(t *testing.T) {
	b, _ := json.Marshal(Response{})
	if string(b) != `{"ok":false,"exit":0}` {
		t.Errorf("empty response = %s", b)
	}
	b, _ = json.Marshal(Request{Cmd: "look", Args: json.RawMessage(`{"all":true}`), Cwd: "/w"})
	if string(b) != `{"cmd":"look","args":{"all":true},"cwd":"/w"}` {
		t.Errorf("request = %s", b)
	}
	var r Response
	json.Unmarshal([]byte(`{"ok":true,"exit":1,"text":"t","image":"AAE=","more":true}`), &r)
	if !r.OK || r.Exit != 1 || r.Text != "t" || string(r.Image) != "\x00\x01" || !r.More {
		t.Errorf("decoded = %+v", r)
	}
}

// Connect reuses a running daemon of the same build without starting another.
func TestConnectSameBuild(t *testing.T) {
	home(t)
	var pings atomic.Int64
	serve(t, map[string]Handler{
		"ping": func(context.Context, Request, func(Response)) Response {
			pings.Add(1)
			return Response{OK: true, Text: BuildID()}
		},
		"echo": func(_ context.Context, req Request, _ func(Response)) Response { return Response{OK: true, Text: "hi"} },
	})
	cl, err := Connect()
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	if pings.Load() != 1 {
		t.Errorf("pings = %d", pings.Load())
	}
	if resp, err := cl.Call(Request{Cmd: "echo"}, nil); err != nil || resp.Text != "hi" {
		t.Errorf("call over Connect's client: %+v %v", resp, err)
	}
	if _, err := os.Stat(LogPath()); !os.IsNotExist(err) {
		t.Error("Connect spawned a daemon (log file created) although one was running")
	}
}

// Connect asks a daemon from another build to stop and starts a new one. Here the old one
// keeps listening, so the reconnect after the (immediately exiting) spawn reaches it again.
func TestConnectOtherBuild(t *testing.T) {
	home(t)
	t.Setenv(childEnv, "1") // the spawned child (this test binary) exits at once
	// This old daemon does not stop on "stop": Connect waits for it only so long.
	defer func(old time.Duration) { stopWait = old }(stopWait)
	stopWait = 100 * time.Millisecond
	var stops atomic.Int64
	serve(t, map[string]Handler{
		"ping": func(context.Context, Request, func(Response)) Response { return Response{OK: true, Text: "old-build"} },
		"stop": func(context.Context, Request, func(Response)) Response { stops.Add(1); return Response{OK: true} },
		"echo": func(context.Context, Request, func(Response)) Response { return Response{OK: true, Text: "still here"} },
	})
	cl, err := Connect()
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	if stops.Load() != 1 {
		t.Errorf("stop requests = %d, want 1", stops.Load())
	}
	if _, err := os.Stat(LogPath()); err != nil {
		t.Errorf("spawn did not open the daemon log: %v", err)
	}
	if resp, err := cl.Call(Request{Cmd: "echo"}, nil); err != nil || resp.Text != "still here" {
		t.Errorf("after reconnect: %+v %v", resp, err)
	}
}

// spawn starts "<exe> daemon" detached, creating the state dir and its log file.
func TestSpawn(t *testing.T) {
	base := home(t)
	t.Setenv(childEnv, "1")
	t.Setenv("TB_HOME", filepath.Join(base, "new"))
	if err := spawn(); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(Dir())
	if err != nil || !st.IsDir() || st.Mode().Perm() != 0o700 {
		t.Errorf("state dir: %v %v", st, err)
	}
	lf, err := os.Stat(LogPath())
	if err != nil || lf.Mode().Perm() != 0o600 {
		t.Errorf("log file: %v %v", lf, err)
	}
}

// spawn fails when the state dir cannot be made.
func TestSpawnBadDir(t *testing.T) {
	base := home(t)
	file := filepath.Join(base, "file")
	os.WriteFile(file, nil, 0o600)
	t.Setenv("TB_HOME", filepath.Join(file, "sub"))
	if err := spawn(); err == nil {
		t.Error("spawn succeeded under a regular file")
	}
}

// syncBuf is a log destination the test can read while the server writes to it.
type syncBuf struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// issue #57: a client that disconnects while its request runs (Ctrl-C on a `tb do` that
// waits for an element, a closed terminal) cancels that request. The handler's context ends
// soon after the connection closes, not when the work would have ended on its own; the
// daemon log says why, and the daemon serves the next client.
func TestServeClientDisconnectCancelsRequest(t *testing.T) {
	home(t)
	logs := &syncBuf{}
	log.SetOutput(logs)
	t.Cleanup(func() { log.SetOutput(io.Discard) })
	started, release := make(chan struct{}), make(chan struct{})
	ended := make(chan error, 1)
	serve(t, map[string]Handler{
		"wait": func(ctx context.Context, _ Request, _ func(Response)) Response {
			close(started)
			select {
			case <-ctx.Done():
				ended <- ctx.Err()
			case <-release: // the test gave up
				ended <- nil
			}
			return Response{OK: true}
		},
		"ok": func(context.Context, Request, func(Response)) Response { return Response{OK: true, Text: "fine"} },
	})
	t.Cleanup(func() { close(release) })
	c, err := net.Dial("unix", SocketPath())
	if err != nil {
		t.Fatal(err)
	}
	io.WriteString(c, `{"cmd":"wait"}`+"\n")
	select {
	case <-started:
	case <-time.After(wait):
		t.Fatal("the handler did not start")
	}
	c.Close()
	select {
	case err := <-ended:
		if err != context.Canceled {
			t.Errorf("handler context ended with %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("issue #57: the request was still running 2s after its client disconnected: its context was not cancelled")
	}
	cl := dial(t)
	if resp, err := cl.Call(Request{Cmd: "ok"}, nil); err != nil || resp.Text != "fine" {
		t.Errorf("after a client left: %+v %v", resp, err)
	}
	deadline := time.Now().Add(wait)
	for !strings.Contains(logs.String(), "wait: cancelled, the client disconnected") {
		if time.Now().After(deadline) {
			t.Fatalf("the daemon log does not say the request was cancelled:\n%s", logs.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// issue #57: watching for a disconnect costs no goroutine per request, and a connection's
// goroutines end with it.
func TestServeNoGoroutineLeak(t *testing.T) {
	home(t)
	serve(t, map[string]Handler{
		"n": func(context.Context, Request, func(Response)) Response { return Response{OK: true} },
	})
	settle := func(max int) int { // the goroutine count once it is at most max (or after a while)
		deadline := time.Now().Add(wait)
		for {
			n := runtime.NumGoroutine()
			if n <= max || time.Now().After(deadline) {
				return n
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	base := runtime.NumGoroutine()
	cl := dial(t)
	for i := 0; i < 200; i++ {
		if _, err := cl.Call(Request{Cmd: "n"}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if n := settle(base + 3); n > base+3 {
		t.Errorf("goroutines grew from %d to %d over 200 requests on one connection", base, n)
	}
	for i := 0; i < 50; i++ {
		c, err := net.Dial("unix", SocketPath())
		if err != nil {
			t.Fatal(err)
		}
		cl := newClient(c)
		if _, err := cl.Call(Request{Cmd: "n"}, nil); err != nil {
			t.Fatal(err)
		}
		cl.Close()
	}
	if n := settle(base + 3); n > base+3 {
		t.Errorf("goroutines grew from %d to %d after 50 connections came and went", base, n)
	}
}

// issue #58: a stop command's answer reaches its client; then the server cancels the
// requests still running, waits for them to end, and runs its stop hooks (the session
// service closes its devices there) before Serve returns. The cancelled request's client is
// told the daemon stopped.
func TestStopAfterReplyCancelsThenRunsHooks(t *testing.T) {
	home(t)
	s := NewServer(time.Hour)
	var waitEnded, hookRan atomic.Bool
	started := make(chan struct{})
	s.Handle("wait", func(ctx context.Context, _ Request, _ func(Response)) Response {
		close(started)
		<-ctx.Done()
		time.Sleep(50 * time.Millisecond) // a command takes a moment to wind down
		waitEnded.Store(true)
		return Response{Exit: 1, Text: ctx.Err().Error()}
	})
	s.Handle("stop", func(ctx context.Context, _ Request, _ func(Response)) Response {
		StopAfterReply(ctx)
		return Response{OK: true, Text: "stopping"}
	})
	s.OnStop(func(ctx context.Context) {
		if !waitEnded.Load() {
			t.Error("the stop hook ran while a request was still running")
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Error("the stop hook has no time limit")
		}
		hookRan.Store(true)
	})
	done := make(chan error, 1)
	go func() { done <- s.Serve() }()
	for i := 0; !Running(); i++ {
		if i > 1000 {
			t.Fatal("server did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	waiting := dial(t)
	waitResp := make(chan Response, 1)
	go func() { r, _ := waiting.Call(Request{Cmd: "wait"}, nil); waitResp <- r }()
	<-started
	if resp, err := dial(t).Call(Request{Cmd: "stop"}, nil); err != nil || resp.Text != "stopping" {
		t.Fatalf("stop = %+v, %v (its answer must reach the client)", resp, err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve = %v", err)
		}
	case <-time.After(wait):
		t.Fatal("Serve did not return")
	}
	if !hookRan.Load() {
		t.Error("the stop hook did not run")
	}
	if r := <-waitResp; r.OK || !strings.Contains(r.Text, "the tb daemon stopped") {
		t.Errorf("the cancelled request answered %+v", r)
	}
}

// issue #124 (part 3): StopDaemon (`tb daemon stop`) returns once the daemon has finished
// stopping, its stop hooks included (closing the sessions' devices and freeing their locks),
// not as soon as the stop command's answer arrives: a tb started right after it could
// otherwise find a device still held. Connect, replacing a daemon from another build, stops
// it the same way.
func TestStopDaemonWaitsForStopHooks(t *testing.T) {
	home(t)
	s := NewServer(time.Hour)
	s.Handle("stop", func(ctx context.Context, _ Request, _ func(Response)) Response {
		StopAfterReply(ctx)
		return Response{OK: true, Text: "tb daemon stopped"}
	})
	var hookDone atomic.Bool
	s.OnStop(func(ctx context.Context) {
		time.Sleep(300 * time.Millisecond) // closing the devices takes a moment
		hookDone.Store(true)
	})
	done := make(chan error, 1)
	go func() { done <- s.Serve() }()
	for i := 0; !Running(); i++ {
		if i > 1000 {
			t.Fatal("server did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	resp, err := StopDaemon()
	if err != nil || !resp.OK || resp.Text != "tb daemon stopped" {
		t.Errorf("StopDaemon = %+v, %v", resp, err)
	}
	if !hookDone.Load() {
		t.Error("issue #124: StopDaemon returned before the daemon finished stopping (its stop hooks still ran)")
	}
	select {
	case <-done:
	case <-time.After(wait):
		t.Fatal("Serve did not return")
	}
}

// StopDaemon with no daemon running says so.
func TestStopDaemonNotRunning(t *testing.T) {
	home(t)
	if _, err := StopDaemon(); !errors.Is(err, ErrNotRunning) {
		t.Errorf("StopDaemon with no daemon = %v, want ErrNotRunning", err)
	}
}
