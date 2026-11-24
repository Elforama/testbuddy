package web

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	neturl "net/url"
	"sort"
	"strings"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
)

var _ driver.NetworkMocker = (*Driver)(nil)

type heldRequest struct {
	session, id string
	timer       *time.Timer
}

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
		d.page.callIn(ctx, s, "Fetch.enable", params, nil)
	}
	return nil
}

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

func (d *Driver) mockPatterns() []map[string]any {
	d.mockMu.Lock()
	defer d.mockMu.Unlock()
	if !d.fetchOn {
		return nil
	}
	return d.patternsLocked()
}

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

func (d *Driver) onRequestPaused(params json.RawMessage, session string) {
	var e struct {
		RequestID string `json:"requestId"`
		Request   struct {
			URL     string            `json:"url"`
			Method  string            `json:"method"`
			Headers map[string]string `json:"headers"`
		} `json:"request"`
	}
	if json.Unmarshal(params, &e) != nil || e.RequestID == "" {
		return
	}
	req := pausedRequest{url: e.Request.URL, method: e.Request.Method, headers: e.Request.Headers}
	d.mockMu.Lock()
	m, ok := d.matchLocked(e.Request.URL, e.Request.Method)
	if asks, isPre := req.preflight(); isPre && (!ok || m.Method == "") {
		if pm, found := d.matchLocked(e.Request.URL, asks); found {
			d.mockMu.Unlock()
			go d.answerPreflight(session, e.RequestID, req, pm)
			return
		}
	}
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
			if still {
				d.respond(session, e.RequestID, req, m)
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
	go d.respond(session, e.RequestID, req, m)
}

type pausedRequest struct {
	url, method string
	headers     map[string]string
}

func (r pausedRequest) crossOrigin() string {
	origin := r.header("Origin")
	if origin == "" {
		return ""
	}
	u, err := neturl.Parse(r.url)
	if err != nil || u.Host == "" {
		return origin
	}
	host := u.Host
	if p := u.Port(); p == map[string]string{"http": "80", "https": "443"}[u.Scheme] {
		host = u.Hostname()
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
	}
	if strings.EqualFold(origin, u.Scheme+"://"+host) {
		return ""
	}
	return origin
}

func (r pausedRequest) header(name string) string {
	for k, v := range r.headers {
		if strings.EqualFold(k, name) {
			return v
		}
	}
	return ""
}

func (r pausedRequest) preflight() (string, bool) {
	m := r.header("Access-Control-Request-Method")
	return m, strings.EqualFold(r.method, "OPTIONS") && m != "" && r.header("Origin") != ""
}

func (d *Driver) matchLocked(url, method string) (driver.NetworkMock, bool) {
	for i := len(d.mocks) - 1; i >= 0; i-- {
		m := d.mocks[i]
		if (m.Method == "" || strings.EqualFold(m.Method, method)) && globMatch(m.URL, url) {
			return m, true
		}
	}
	return driver.NetworkMock{}, false
}

func (d *Driver) respond(session, id string, req pausedRequest, m driver.NetworkMock) {
	if m.Fail {
		d.answer(session, "Fetch.failRequest", map[string]any{"requestId": id, "errorReason": "Failed"})
		return
	}
	hs := m.Headers
	if origin := req.crossOrigin(); origin != "" && !setsCORS(m.Headers) {
		hs = varyOnOrigin(append(append([][2]string{}, hs...), allowOrigin(origin)...))
		names := make([]string, 0, len(m.Headers))
		for _, h := range m.Headers {
			names = append(names, h[0])
		}
		if len(names) > 0 {
			hs = append(hs, [2]string{"Access-Control-Expose-Headers", strings.Join(names, ", ")})
		}
	}
	d.answer(session, "Fetch.fulfillRequest", map[string]any{"requestId": id, "responseCode": m.Status,
		"responseHeaders": headerParams(hs), "body": base64.StdEncoding.EncodeToString(m.Body)})
}

func (d *Driver) answerPreflight(session, id string, req pausedRequest, m driver.NetworkMock) {
	if m.Fail {
		d.answer(session, "Fetch.failRequest", map[string]any{"requestId": id, "errorReason": "Failed"})
		return
	}
	var hs [][2]string
	if setsCORS(m.Headers) {
		for _, h := range m.Headers {
			if strings.HasPrefix(strings.ToLower(h[0]), "access-control-") {
				hs = append(hs, h)
			}
		}
	} else {
		hs = append(varyOnOrigin(allowOrigin(req.header("Origin"))),
			[2]string{"Access-Control-Allow-Methods", req.header("Access-Control-Request-Method")})
		if rh := req.header("Access-Control-Request-Headers"); rh != "" {
			hs = append(hs, [2]string{"Access-Control-Allow-Headers", rh})
		}
	}
	d.answer(session, "Fetch.fulfillRequest", map[string]any{"requestId": id, "responseCode": 204,
		"responseHeaders": headerParams(hs)})
}

func allowOrigin(origin string) [][2]string {
	return [][2]string{{"Access-Control-Allow-Origin", origin}, {"Access-Control-Allow-Credentials", "true"}}
}

func varyOnOrigin(hs [][2]string) [][2]string {
	for i, h := range hs {
		if !strings.EqualFold(h[0], "Vary") {
			continue
		}
		for _, v := range strings.Split(h[1], ",") {
			if v = strings.TrimSpace(v); v == "*" || strings.EqualFold(v, "Origin") {
				return hs
			}
		}
		if v := strings.TrimSpace(h[1]); v != "" {
			hs[i][1] = v + ", Origin"
		} else {
			hs[i][1] = "Origin"
		}
		return hs
	}
	return append(hs, [2]string{"Vary", "Origin"})
}

func setsCORS(hs [][2]string) bool {
	for _, h := range hs {
		if strings.EqualFold(h[0], "Access-Control-Allow-Origin") {
			return true
		}
	}
	return false
}

func headerParams(hs [][2]string) []map[string]string {
	out := make([]map[string]string, 0, len(hs))
	for _, h := range hs {
		out = append(out, map[string]string{"name": h[0], "value": h[1]})
	}
	return out
}

func (d *Driver) answer(session, method string, params map[string]any) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	d.page.callIn(ctx, session, method, params, nil)
}

func globMatch(pat, s string) bool {
	type tok struct {
		kind byte
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
