package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/elforama/testbuddy/internal/connect"
	"github.com/elforama/testbuddy/internal/daemon"
	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/driver"
)

func openDriver(ctx context.Context, info device.Info) (driver.Driver, error) {
	return connect.Open(ctx, info)
}

// MCP: JSON-RPC 2.0 over stdio, one message per line. Every tool is a thin wrapper over a
// daemon call, so MCP and the CLI behave the same. Descriptions are short on purpose:
// clients resend them with every request.

type mcpTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

func schema(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

var devProp = map[string]any{"type": "string", "description": "device name/serial or session name; optional with one device"}

var mcpTools = []mcpTool{
	{"devices", "List devices and open sessions.", schema(map[string]any{})},
	{"open", "Connect to a device, optionally naming it (e.g. alice) for multi-device work.", schema(map[string]any{
		"device": devProp, "name": map[string]any{"type": "string"}, "app": map[string]any{"type": "string", "description": "default app ID, or a URL for web"},
		"platform": map[string]any{"type": "string", "enum": []string{"android", "ios", "web"}},
		"headless": map[string]any{"type": "boolean", "description": "start Chrome without a window; web only"}})},
	{"look", "The screen: one line per element with @refs, indented under row:/grid:/scrollable headers where layout matters; scroll areas say what is out of view (12 more below); * marks elements new since the last look/run. json returns the same as JSON. screenshot adds a small image.", schema(map[string]any{
		"device": devProp, "screenshot": map[string]any{"type": "boolean"}, "all": map[string]any{"type": "boolean"}, "json": map[string]any{"type": "boolean"}})},
	{"run", "Run flow commands (YAML; @refs from look allowed). Returns each result and what changed on screen.", schema(map[string]any{
		"device": devProp, "commands": map[string]any{"type": "string", "description": "e.g. \"- tapOn: @3\\n- inputText: hi\""},
		"json": map[string]any{"type": "boolean", "description": "return structured steps and the resulting screen"}}, "commands")},
	{"wait", "Wait (event-driven) until text appears, or is gone.", schema(map[string]any{
		"device": devProp, "text": map[string]any{"type": "string"}, "gone": map[string]any{"type": "boolean"},
		"timeout_ms": map[string]any{"type": "integer"}}, "text")},
	{"locate", "Inspect all matches and the runner's pick for a selector on the current screen, without acting.", schema(map[string]any{
		"device": devProp, "selector": map[string]any{"type": "string", "description": "text, @ref, or a YAML selector such as {id: login}"}}, "selector")},
	{"save_flow", "Save the commands that passed as a flow file.", schema(map[string]any{
		"device": devProp, "path": map[string]any{"type": "string"}}, "path")},
	{"close", "Disconnect a device.", schema(map[string]any{"device": devProp})},
}

type mcpRequest struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// mcpJob is a request waiting for its turn or being answered. ctx ends when the client
// cancels it.
type mcpJob struct {
	req    mcpRequest
	key    string
	ctx    context.Context
	cancel context.CancelFunc
}

// mcpServer answers requests one at a time, in the order they came, as tb mcp always has.
// stdin is read on its own, so a client's notifications/cancelled reaches the request it
// names while that request runs (issue #122): its daemon connection is closed, which cancels
// the command in the daemon, as a tb CLI command stops on Ctrl-C.
type mcpServer struct {
	mu      sync.Mutex // guards the fields below and every write to out
	cond    *sync.Cond
	queue   []*mcpJob
	pending map[string]*mcpJob // queued or running, by request ID; a cancel finds them here
	eof     bool
	out     *json.Encoder
}

// idKey is a request ID as the map key: the same ID compacted the same way, whether the
// client writes it as a number or a string.
func idKey(id json.RawMessage) string {
	var b bytes.Buffer
	if json.Compact(&b, id) != nil {
		return string(id)
	}
	return b.String()
}

// mcpQueued, when set (by tests), is told of each request cmdMCP queues, once every line
// before it has been handled.
var mcpQueued func(method string)

func cmdMCP(argv []string) int {
	s := &mcpServer{pending: map[string]*mcpJob{}, out: json.NewEncoder(os.Stdout)}
	s.cond = sync.NewCond(&s.mu)
	done := make(chan struct{})
	go func() { defer close(done); s.work() }()
	in := bufio.NewReaderSize(os.Stdin, 1<<20)
	for {
		line, err := in.ReadBytes('\n')
		if len(line) == 0 && err != nil {
			break
		}
		var req mcpRequest
		if json.Unmarshal(line, &req) != nil {
			continue
		}
		if len(req.ID) == 0 { // notification
			if req.Method == "notifications/cancelled" {
				var p struct {
					RequestID json.RawMessage `json:"requestId"`
				}
				if json.Unmarshal(req.Params, &p) == nil && len(p.RequestID) > 0 {
					s.cancel(idKey(p.RequestID))
				}
			}
			continue
		}
		ctx, cancel := context.WithCancel(context.Background())
		j := &mcpJob{req: req, key: idKey(req.ID), ctx: ctx, cancel: cancel}
		s.mu.Lock()
		// The spec forbids cancelling initialize; a cancel naming an ID already in flight
		// stays with the request that holds it.
		if _, dup := s.pending[j.key]; req.Method != "initialize" && !dup {
			s.pending[j.key] = j
		}
		s.queue = append(s.queue, j)
		s.cond.Signal()
		s.mu.Unlock()
		if mcpQueued != nil {
			mcpQueued(req.Method)
		}
	}
	// End of input: answer what is still queued (a client may pipe its requests and close
	// stdin), then exit.
	s.mu.Lock()
	s.eof = true
	s.cond.Signal()
	s.mu.Unlock()
	<-done
	return 0
}

// cancel ends the request with this ID if it is queued or running. Per the MCP spec it then
// gets no response: work checks for the cancel under the same lock before it writes, so a
// result that comes in after the cancel is dropped. A cancel for a request already answered,
// or unknown, is ignored.
func (s *mcpServer) cancel(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j, ok := s.pending[key]; ok {
		delete(s.pending, key)
		j.cancel()
	}
}

func (s *mcpServer) work() {
	for {
		s.mu.Lock()
		for len(s.queue) == 0 && !s.eof {
			s.cond.Wait()
		}
		if len(s.queue) == 0 {
			s.mu.Unlock()
			return
		}
		j := s.queue[0]
		s.queue[0] = nil
		s.queue = s.queue[1:]
		s.mu.Unlock()
		var reply map[string]any
		if j.ctx.Err() == nil { // not cancelled while it waited
			reply = mcpReply(j.ctx, j.req)
		}
		s.mu.Lock()
		if j.ctx.Err() == nil {
			s.out.Encode(reply)
		}
		if s.pending[j.key] == j {
			delete(s.pending, j.key)
		}
		s.mu.Unlock()
		j.cancel()
	}
}

func mcpReply(ctx context.Context, req mcpRequest) map[string]any {
	reply := map[string]any{"jsonrpc": "2.0", "id": req.ID}
	switch req.Method {
	case "initialize":
		reply["result"] = map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "testbuddy", "version": version},
			"instructions": "Drive Android, iOS or Chrome: open (optional), look to read the screen (@refs), run flow commands " +
				"(answers include what changed), wait for text, save_flow to keep a working flow. Run whole suites with the CLI: tb test.",
		}
	case "ping":
		reply["result"] = map[string]any{}
	case "tools/list":
		reply["result"] = map[string]any{"tools": mcpTools}
	case "tools/call":
		var p struct {
			Name string         `json:"name"`
			Args map[string]any `json:"arguments"`
		}
		json.Unmarshal(req.Params, &p)
		reply["result"] = mcpCall(ctx, p.Name, p.Args)
	default:
		reply["error"] = map[string]any{"code": -32601, "message": "method not found"}
	}
	return reply
}

// mcpCall runs one tool over its own daemon connection. When ctx ends, the connection is
// closed, and the daemon cancels the command it runs for it.
func mcpCall(ctx context.Context, name string, args map[string]any) map[string]any {
	str := func(k string) string { s, _ := args[k].(string); return s }
	call := func(cmd string, a map[string]any) daemon.Response {
		if ctx.Err() != nil {
			return daemon.Response{Exit: 1, Text: cmd + " was cancelled"}
		}
		c, err := daemon.Connect()
		if err != nil {
			return daemon.Response{Exit: 1, Text: err.Error()}
		}
		defer c.Close()
		stop := context.AfterFunc(ctx, func() { c.Close() })
		defer stop()
		raw, _ := json.Marshal(a)
		cwd, _ := os.Getwd()
		var parts strings.Builder
		resp, err := c.Call(daemon.Request{Cmd: cmd, Args: raw, Cwd: cwd}, func(r daemon.Response) { parts.WriteString(r.Text) })
		if err != nil {
			return daemon.Response{Exit: 1, Text: err.Error()}
		}
		resp.Text = parts.String() + resp.Text
		return resp
	}
	var resp daemon.Response
	var image []byte
	var structured json.RawMessage
	switch name {
	case "devices":
		resp = call("devices", nil)
	case "open":
		headless, _ := args["headless"].(bool)
		params := map[string]any{"device": str("device"), "name": str("name"), "app": str("app"), "platform": str("platform")}
		if headless {
			params["headless"] = true
		}
		resp = call("open", params)
	case "look":
		shot, _ := args["screenshot"].(bool)
		all, _ := args["all"].(bool)
		asJSON, _ := args["json"].(bool)
		resp = call("look", map[string]any{"device": str("device"), "all": all, "shot": shot, "json": asJSON})
		if asJSON && len(resp.JSON) > 0 {
			resp.Text = string(resp.JSON)
		}
		if shot {
			for _, l := range strings.Split(resp.Text, "\n") {
				if p, ok := strings.CutPrefix(l, "screenshot: "); ok {
					image, _ = os.ReadFile(p)
				}
			}
		}
	case "run":
		params := map[string]any{"device": str("device"), "commands": toCommandList([]string{str("commands")})}
		if asJSON, _ := args["json"].(bool); asJSON {
			params["json"] = true
		}
		resp = call("do", params)
		if params["json"] == true && len(resp.JSON) > 0 {
			structured = resp.JSON
			resp.Text = string(structured)
		}
	case "wait":
		key := "visible"
		if g, _ := args["gone"].(bool); g {
			key = "notVisible"
		}
		t := 17000
		if f, ok := args["timeout_ms"].(float64); ok {
			t = int(f)
		}
		sel, _ := json.Marshal(str("text"))
		resp = call("do", map[string]any{"device": str("device"),
			"commands": fmt.Sprintf("- extendedWaitUntil:\n    %s: %s\n    timeout: %d\n", key, sel, t)})
	case "save_flow":
		resp = call("save", map[string]any{"device": str("device"), "path": str("path")})
	case "locate":
		resp = call("locate", map[string]any{"device": str("device"), "selector": str("selector")})
		if len(resp.JSON) > 0 {
			structured = resp.JSON
			resp.Text = string(structured)
		}
	case "close":
		resp = call("close", map[string]any{"device": str("device")})
	default:
		resp = daemon.Response{Exit: 2, Text: "unknown tool " + name}
	}
	content := []map[string]any{{"type": "text", "text": resp.Text}}
	if image != nil {
		content = append(content, map[string]any{"type": "image", "mimeType": "image/png", "data": base64.StdEncoding.EncodeToString(image)})
	}
	result := map[string]any{"content": content, "isError": resp.Exit != 0}
	if len(structured) > 0 {
		result["structuredContent"] = structured
	}
	return result
}
