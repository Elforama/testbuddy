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

type Client struct {
	c net.Conn
	r *bufio.Reader
}

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

func Connect() (*Client, error) {
	if c, err := net.Dial("unix", SocketPath()); err == nil {
		cl := newClient(c)
		resp, err := cl.Call(Request{Cmd: "ping"}, nil)
		if err == nil && resp.Text == BuildID() {
			return cl, nil
		}
		cl.stop()
		cl.Close()
		for i := 0; i < 100 && Running(); i++ {
			time.Sleep(10 * time.Millisecond)
		}
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
		time.Sleep(20 * time.Millisecond)
	}
}

var ErrNotRunning = errors.New("tb daemon is not running")

var stopWait = stopRequestsLimit + stopHooksLimit + 2*time.Second

func StopDaemon() (Response, error) {
	c, err := net.DialTimeout("unix", SocketPath(), 300*time.Millisecond)
	if err != nil {
		return Response{}, ErrNotRunning
	}
	cl := newClient(c)
	defer cl.Close()
	return cl.stop()
}

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

func Running() bool {
	c, err := net.DialTimeout("unix", SocketPath(), 300*time.Millisecond)
	if err != nil {
		return false
	}
	c.Close()
	return true
}
