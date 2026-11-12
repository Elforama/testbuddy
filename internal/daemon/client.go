package daemon

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"time"
)

// Client is a connection to the daemon.
type Client struct {
	c net.Conn
	r *bufio.Reader
}

// BuildID identifies this binary as it was when the process started (its size and
// modification time), so a client can tell a daemon started from an older build.
func BuildID() string { return startBuildID }

var startBuildID = computeBuildID()

func computeBuildID() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	st, err := os.Stat(exe)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%d-%d", st.Size(), st.ModTime().UnixNano())
}

// Connect connects to the daemon, starting it if it is not running, and replacing it if it
// runs an older build of tb.
func Connect() (*Client, error) {
	if c, err := net.Dial("unix", SocketPath()); err == nil {
		cl := newClient(c)
		resp, err := cl.Call(Request{Cmd: "ping"}, nil)
		if err == nil && resp.Text == BuildID() {
			return cl, nil
		}
		// Wait until the old daemon has closed its devices and freed their locks: the new
		// one may open the same device at once (issue #124).
		cl.stop()
		cl.Close()
	}
	if err := spawn(); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		c, err := net.Dial("unix", SocketPath())
		if err == nil {
			return newClient(c), nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("the tb daemon did not start (see %s): %w", LogPath(), err)
		}
		time.Sleep(20 * time.Millisecond) // daemon start-up only
	}
}

// ErrNotRunning is StopDaemon's error when no daemon answers.
var ErrNotRunning = errors.New("tb daemon is not running")

// stopWait bounds how long a client waits for a daemon to finish stopping: the daemon's own
// limits, and a little more.
var stopWait = stopRequestsLimit + stopHooksLimit + 2*time.Second

// StopDaemon stops the running daemon (it does not start one) and returns its answer once the
// daemon has finished stopping: its commands cancelled and its sessions' devices closed and
// freed, so a tb started right after finds them free (issue #124).
func StopDaemon() (Response, error) {
	c, err := net.DialTimeout("unix", SocketPath(), 300*time.Millisecond)
	if err != nil {
		return Response{}, ErrNotRunning
	}
	cl := newClient(c)
	defer cl.Close()
	return cl.stop()
}

// stop sends the stop command, then waits for the daemon to close the connection: it does so
// once it has finished stopping (a daemon from an older build, as its process exits).
func (c *Client) stop() (Response, error) {
	resp, err := c.Call(Request{Cmd: "stop"}, nil)
	if err != nil {
		return resp, err
	}
	c.c.SetReadDeadline(time.Now().Add(stopWait))
	for {
		if _, err := c.r.ReadBytes('\n'); err != nil {
			if errors.Is(err, os.ErrDeadlineExceeded) {
				return resp, fmt.Errorf("the tb daemon did not finish stopping within %s (see %s)", stopWait, LogPath())
			}
			return resp, nil
		}
	}
}

func newClient(c net.Conn) *Client { return &Client{c: c, r: bufio.NewReaderSize(c, 1<<20)} }

// spawn starts `tb daemon` in its own session so it outlives this command.
func spawn() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(Dir(), 0o700); err != nil {
		return err
	}
	logf, err := os.OpenFile(LogPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	cmd := exec.Command(exe, "daemon")
	cmd.Stdout = logf
	cmd.Stderr = logf
	cmd.Stdin = nil
	cmd.SysProcAttr = detached()
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// Call sends a request and calls onPart for each streamed part; it returns the final response.
func (c *Client) Call(req Request, onPart func(Response)) (Response, error) {
	b, _ := json.Marshal(req)
	if _, err := c.c.Write(append(b, '\n')); err != nil {
		return Response{}, err
	}
	for {
		line, err := c.r.ReadBytes('\n')
		if err != nil {
			return Response{}, fmt.Errorf("lost the tb daemon: %w", err)
		}
		var resp Response
		if err := json.Unmarshal(line, &resp); err != nil {
			return Response{}, err
		}
		if resp.More {
			if onPart != nil {
				onPart(resp)
			}
			continue
		}
		return resp, nil
	}
}

func (c *Client) Close() error { return c.c.Close() }

// Running reports whether a daemon answers on the socket.
func Running() bool {
	c, err := net.DialTimeout("unix", SocketPath(), 300*time.Millisecond)
	if err != nil {
		return false
	}
	c.Close()
	return true
}
