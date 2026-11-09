package web

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
)

// Network mocks (mockNetwork) go through Chrome's Fetch domain: while any mock is active,
// requests matching a mock's URL pattern pause in the browser (Fetch.requestPaused) and the
// driver answers each one: with the latest matching mock's response, as a network error, or
// by letting it go on to the network (another method, or a pattern Chrome matched more
// loosely). The page and every attached cross-origin frame intercept, since a frame's
// requests pause in its own session.
//
// A paused request still shows in the Network events, so the settle bookkeeping (in-flight
// XHR/fetch) sees a mocked request start and finish like a real one.

var _ driver.NetworkMocker = (*Driver)(nil)

// heldRequest is a paused request whose mocked response waits for the mock's delay.
type heldRequest struct {
	session, id string
	timer       *time.Timer
}

// MockNetwork adds a mock and makes the page and its frames intercept its URL pattern.
func (d *Driver) MockNetwork(ctx context.Context, m driver.NetworkMock) error {
	if m.URL == "" {
		return errors.New("mockNetwork: the url pattern is empty")
	}
	d.mockMu.Lock()
	d.mocks = append(d.mocks, m)
	d.fetchOn = true
	pats := d.patternsLocked()
	d.mockMu.Unlock()
	params := map[string]any{"patterns": pats}
	if err := d.page.call(ctx, "Fetch.enable", params, nil); err != nil {
		return err
	}
	for _, s := range d.frameSessions() {
		d.page.callIn(ctx, s, "Fetch.enable", params, nil) // a frame may be gone already
	}
	return nil
}

// ClearNetworkMocks drops every mock: requests held for a delay go on to the network at once,
// and the page and its frames stop intercepting. With nothing mocked it sends nothing.
func (d *Driver) ClearNetworkMocks(ctx context.Context) error {
	d.mockMu.Lock()
	on, held := d.fetchOn, d.held
	d.mocks, d.fetchOn, d.held = nil, false, nil
	for _, h := range held {
		h.timer.Stop()
	}
	d.mockMu.Unlock()
	for _, h := range heldInOrder(held) {
		d.page.callIn(ctx, h.session, "Fetch.continueRequest", map[string]any{"requestId": h.id}, nil)
	}
	if !on {
		return nil
	}
	err := d.page.call(ctx, "Fetch.disable", nil, nil)
	for _, s := range d.frameSessions() {
		d.page.callIn(ctx, s, "Fetch.disable", nil, nil)
	}
	return err
}

// dropHeld forgets held requests without answering them (the browser is closing).
func (d *Driver) dropHeld() {
	d.mockMu.Lock()
	defer d.mockMu.Unlock()
	for _, h := range d.held {
		h.timer.Stop()
	}
	d.held, d.mocks = nil, nil
}

func heldInOrder(held map[string]*heldRequest) []*heldRequest {
	out := make([]*heldRequest, 0, len(held))
	for _, h := range held {
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].session+"/"+out[i].id < out[j].session+"/"+out[j].id })
	return out
}

// patternsLocked is the Fetch.enable pattern list: each mock's URL once, in order.
func (d *Driver) patternsLocked() []map[string]any {
	seen := map[string]bool{}
	pats := []map[string]any{}
	for _, m := range d.mocks {
		if !seen[m.URL] {
			seen[m.URL] = true
			pats = append(pats, map[string]any{"urlPattern": m.URL, "requestStage": "Request"})
		}
	}
	return pats
}

// mockPatterns is the pattern list while mocks are active, else nil.
func (d *Driver) mockPatterns() []map[string]any {
	d.mockMu.Lock()
	defer d.mockMu.Unlock()
	if !d.fetchOn {
		return nil
	}
	return d.patternsLocked()
}

// frameSessions lists the attached cross-origin frames' sessions, in attach order.
func (d *Driver) frameSessions() []string {
	d.mu.Lock()
	fs := make([]*frame, 0, len(d.frames))
	for _, f := range d.frames {
		fs = append(fs, f)
	}
	d.mu.Unlock()
	sort.Slice(fs, func(i, j int) bool { return fs[i].order < fs[j].order })
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.session
	}
	return out
}

// onRequestPaused answers an intercepted request. It runs on the connection's read loop, so
// the answer is sent from another goroutine (a call waits for a reply the loop delivers).
func (d *Driver) onRequestPaused(params json.RawMessage, session string) {
	var e struct {
		RequestID string `json:"requestId"`
		Request   struct {
			URL    string `json:"url"`
			Method string `json:"method"`
		} `json:"request"`
	}
	if json.Unmarshal(params, &e) != nil || e.RequestID == "" {
		return
	}
	d.mockMu.Lock()
	m, ok := d.matchLocked(e.Request.URL, e.Request.Method)
	if ok && m.Delay > 0 {
		key := session + "/" + e.RequestID
		if d.held == nil {
			d.held = map[string]*heldRequest{}
		}
		d.held[key] = &heldRequest{session: session, id: e.RequestID, timer: time.AfterFunc(m.Delay, func() {
			d.mockMu.Lock()
			_, still := d.held[key]
			delete(d.held, key)
			d.mockMu.Unlock()
			if still { // not cleared or closed meanwhile
				d.respond(session, e.RequestID, m)
			}
		})}
		d.mockMu.Unlock()
		return
	}
	d.mockMu.Unlock()
	if !ok {
		go d.answer(session, "Fetch.continueRequest", map[string]any{"requestId": e.RequestID})
		return
	}
	go d.respond(session, e.RequestID, m)
}

// matchLocked is the latest mock for this request.
func (d *Driver) matchLocked(url, method string) (driver.NetworkMock, bool) {
	for i := len(d.mocks) - 1; i >= 0; i-- {
		m := d.mocks[i]
		if (m.Method == "" || strings.EqualFold(m.Method, method)) && globMatch(m.URL, url) {
			return m, true
		}
	}
	return driver.NetworkMock{}, false
}

// respond sends a mock's answer to a paused request.
func (d *Driver) respond(session, id string, m driver.NetworkMock) {
	if m.Fail {
		d.answer(session, "Fetch.failRequest", map[string]any{"requestId": id, "errorReason": "Failed"})
		return
	}
	headers := make([]map[string]string, 0, len(m.Headers))
	for _, h := range m.Headers {
		headers = append(headers, map[string]string{"name": h[0], "value": h[1]})
	}
	d.answer(session, "Fetch.fulfillRequest", map[string]any{"requestId": id, "responseCode": m.Status,
		"responseHeaders": headers, "body": base64.StdEncoding.EncodeToString(m.Body)})
}

func (d *Driver) answer(session, method string, params map[string]any) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	d.page.callIn(ctx, session, method, params, nil) // a request the page dropped meanwhile errors: nothing to do
}

// globMatch matches s against Chrome's URL pattern syntax: * is any run of characters
// (none included), ? exactly one, and \ makes the next character literal.
func globMatch(pat, s string) bool {
	type tok struct {
		kind byte // 'l' literal, '*', '?'
		c    byte
	}
	toks := make([]tok, 0, len(pat))
	for i := 0; i < len(pat); i++ {
		switch c := pat[i]; {
		case c == '\\' && i+1 < len(pat):
			i++
			toks = append(toks, tok{'l', pat[i]})
		case c == '*' || c == '?':
			toks = append(toks, tok{kind: c})
		default:
			toks = append(toks, tok{'l', c})
		}
	}
	ti, si, star, mark := 0, 0, -1, 0
	for si < len(s) {
		switch {
		case ti < len(toks) && (toks[ti].kind == '?' || toks[ti].kind == 'l' && toks[ti].c == s[si]):
			ti++
			si++
		case ti < len(toks) && toks[ti].kind == '*':
			star, mark = ti, si
			ti++
		case star >= 0:
			ti = star + 1
			mark++
			si = mark
		default:
			return false
		}
	}
	for ti < len(toks) && toks[ti].kind == '*' {
		ti++
	}
	return ti == len(toks)
}
