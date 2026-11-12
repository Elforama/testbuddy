package android

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
)

const editorSocket = "localabstract:testbuddy-editor"

// Android blocks direct shell -> app Unix socket connections. Reach the
// installed service through adbd, retaining its shell/root peer-UID check.
func (d *Driver) editorBackspace(ctx context.Context, o driver.ActionOpts) (driver.ActionResult, bool, error) {
	var result driver.ActionResult
	d.stateMu.Lock()
	sdk := d.sdk
	d.stateMu.Unlock()
	if sdk < 33 || os.Getenv("TB_NO_A11Y_SERVICE") != "" {
		return result, false, nil
	}
	d.editorMu.Lock()
	defer d.editorMu.Unlock()
	start := time.Now()
	m, err := d.call(ctx, "editorTarget", nil, 3*time.Second)
	if err != nil {
		return result, false, err
	}
	var prepared struct {
		Target json.RawMessage `json:"target"`
		Seq    int64           `json:"seq"`
	}
	if err = m.Decode(&prepared); err != nil {
		return result, false, err
	}
	if len(prepared.Target) == 0 || string(prepared.Target) == "null" {
		return result, false, nil
	}
	conn, err := d.dialEditor(ctx)
	if err != nil {
		return result, false, err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	deadline := time.Now().Add(2500 * time.Millisecond)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	conn.SetDeadline(deadline)
	// No automatic retry after writing: the first request may have deleted.
	if _, err = conn.Write(append(prepared.Target, '\n')); err != nil {
		return result, true, err
	}
	reply, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return result, true, fmt.Errorf("editor Backspace reply lost; refusing duplicate key: %w", err)
	}
	switch strings.TrimSpace(reply) {
	case "unavailable":
		// The helper sent nothing. Recheck the same editor at the point of
		// injection, since the accessibility service and shell can disagree.
		result, err := d.action(ctx, "key", o, map[string]any{"code": 67, "expectedEditor": prepared.Target}, 0)
		return result, true, err
	case "sent":
	default:
		return result, true, fmt.Errorf("editor Backspace %s; refusing duplicate or wrong-field key", strings.TrimSpace(reply))
	}
	result.InjectMs = int(time.Since(start).Milliseconds())
	timeout := o.Timeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}
	noChange := o.NoChangeMs
	if noChange == 0 {
		noChange = 250
	}
	settled := time.Now()
	m, settledOn, err := d.callOn(ctx, "settle", map[string]any{"since": prepared.Seq, "timeout": timeout.Milliseconds(), "noChange": noChange, "react": o.React}, timeout+time.Second)
	if err != nil {
		return result, true, err
	}
	var response struct {
		Result string `json:"result"`
		Seq    int64  `json:"seq"`
	}
	if err = m.Decode(&response); err != nil {
		return result, true, err
	}
	result.Settle = driver.SettleResult(response.Result)
	result.Seq = d.seqOn(settledOn, response.Seq)
	result.SettleMs = int(time.Since(settled).Milliseconds())
	return result, true, nil
}

// dialEditor connects to the editor service through its adb forward; the caller holds
// editorMu. The forward does not last as long as the driver: an adb server restart drops it
// (issue #69). A dial that fails has written nothing, so forwarding again and dialing once more
// is safe. Nothing after a write is ever retried: a Backspace sent twice deletes two characters.
func (d *Driver) dialEditor(ctx context.Context) (net.Conn, error) {
	if d.editorOld.Swap(false) && d.editorPort != 0 {
		// The agent connection was replaced: keep the port unless adb no longer forwards it to
		// the editor, where something else may answer on it now. Unknown (adb cannot list) keeps
		// it: a dead one is still caught by the dial below. Never remove it blindly: after an adb
		// restart, another forward may own the number.
		if listed, known := d.editorForwardListed(ctx, d.editorPort); known && !listed {
			d.editorPort = 0
		}
	}
	for retried := false; ; retried = true {
		if d.editorPort == 0 {
			out, err := d.adb.Run(ctx, "forward", "tcp:0", editorSocket)
			if err != nil {
				return nil, err
			}
			port, err := strconv.Atoi(strings.TrimSpace(out))
			if err != nil || port <= 0 {
				return nil, fmt.Errorf("invalid editor forward %q", out)
			}
			d.editorPort = port
		}
		conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(d.editorPort)))
		if err == nil {
			return conn, nil
		}
		if ctx.Err() != nil || !connRefused(err) {
			// Cut short, or out of sockets: that says nothing about the forward. Keep it.
			return nil, err
		}
		// Refused: nothing listens on the port, so the forward is gone. Forget it, so a later
		// Backspace forwards again even when this one gives up.
		d.editorPort = 0
		if retried {
			return nil, err
		}
	}
}

// connRefused reports whether a dial was refused: nothing listened on the port. Windows
// reports WSAECONNREFUSED (10061), which syscall.ECONNREFUSED does not match there.
func connRefused(err error) bool {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	return errno == syscall.ECONNREFUSED || runtime.GOOS == "windows" && errno == 10061
}

// editorForwardListed reports whether adb lists local port as this device's forward to the
// editor, on any line of `adb forward --list`. known is false when adb could not list them.
func (d *Driver) editorForwardListed(ctx context.Context, port int) (listed, known bool) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := d.adb.Run(ctx, "forward", "--list")
	if err != nil {
		return false, false
	}
	local := "tcp:" + strconv.Itoa(port)
	for _, line := range strings.Split(out, "\n") {
		if f := strings.Fields(line); len(f) == 3 && f[0] == d.adb.Serial && f[1] == local && f[2] == editorSocket {
			return true, true
		}
	}
	return false, true
}

func (d *Driver) closeEditorForward() {
	d.editorMu.Lock()
	defer d.editorMu.Unlock()
	if d.editorPort == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// `adb forward --remove tcp:N` matches the local port only, whatever -s says: after an adb
	// restart, N may be another device's forward. Remove it only when adb lists it as ours.
	if listed, _ := d.editorForwardListed(ctx, d.editorPort); listed {
		d.adb.Run(ctx, "forward", "--remove", "tcp:"+strconv.Itoa(d.editorPort))
	}
	d.editorPort = 0
}
