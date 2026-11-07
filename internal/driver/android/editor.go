package android

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
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
	if d.editorPort == 0 {
		out, e := d.adb.Run(ctx, "forward", "tcp:0", editorSocket)
		if e != nil {
			return result, false, e
		}
		port, e := strconv.Atoi(strings.TrimSpace(out))
		if e != nil || port <= 0 {
			return result, false, fmt.Errorf("invalid editor forward %q", out)
		}
		d.editorPort = port
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(d.editorPort)))
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
		return result, false, nil // explicitly no dispatch and same focus
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
	m, err = d.call(ctx, "settle", map[string]any{"since": prepared.Seq, "timeout": timeout.Milliseconds(), "noChange": noChange, "react": o.React}, timeout+time.Second)
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
	result.Seq = d.seq(response.Seq)
	result.SettleMs = int(time.Since(settled).Milliseconds())
	return result, true, nil
}

func (d *Driver) closeEditorForward() {
	d.editorMu.Lock()
	defer d.editorMu.Unlock()
	if d.editorPort == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	d.adb.Run(ctx, "forward", "--remove", "tcp:"+strconv.Itoa(d.editorPort))
	d.editorPort = 0
}
