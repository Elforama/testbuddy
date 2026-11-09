package web

import (
	"encoding/base64"
	"reflect"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
)

// The URL pattern is Chrome's: * is any run of characters, ? one character, \ escapes.
func TestGlobMatch(t *testing.T) {
	for _, tc := range []struct {
		pat, s string
		want   bool
	}{
		{"*", "https://a.example/x", true},
		{"*", "", true},
		{"*/api/items*", "https://shop.example/api/items?page=2", true},
		{"*/api/items*", "https://shop.example/api/item", false},
		{"https://shop.example/api/*", "https://shop.example/api/", true},
		{"https://shop.example/api/*", "http://shop.example/api/x", false},
		{"*/v?/users", "https://x/v1/users", true},
		{"*/v?/users", "https://x/v12/users", false},
		{"*.png", "https://cdn/x.png", true},
		{"*.png", "https://cdn/x.png?v=1", false},
		{`*\?q=1`, "https://x/search?q=1", true},
		{`*\*`, "https://x/*", true},
		{`*\*`, "https://x/a", false},
		{"https://x/a", "https://x/a", true},
		{"https://x/a", "https://x/ab", false},
		{"*a*b*c*", "xxaxxbxxcxx", true},
		{"*a*b*c*", "xxaxxcxxbxx", false},
	} {
		if got := globMatch(tc.pat, tc.s); got != tc.want {
			t.Errorf("globMatch(%q, %q) = %v, want %v", tc.pat, tc.s, got, tc.want)
		}
	}
}

// patterns lists the Fetch.enable url patterns sent in session ("" = the page).
func fetchPatterns(page *fakeCDP, session string) [][]string {
	var out [][]string
	for _, m := range page.calls("Fetch.enable") {
		if m.SessionID != session {
			continue
		}
		var pats []string
		ps, _ := m.params()["patterns"].([]any)
		for _, p := range ps {
			pm := p.(map[string]any)
			if pm["requestStage"] != "Request" {
				panic("requestStage " + pm["requestStage"].(string))
			}
			pats = append(pats, pm["urlPattern"].(string))
		}
		out = append(out, pats)
	}
	return out
}

// paused pushes a Fetch.requestPaused event, as Chrome does for an intercepted request.
func paused(page *fakeCDP, session, id, method, url string) {
	page.event("Fetch.requestPaused", map[string]any{"requestId": id, "networkId": "n" + id,
		"request": map[string]any{"url": url, "method": method}, "resourceType": "Fetch"}, session)
}

// answer waits for the reply to paused request id (fulfill, fail or continue) and returns it.
func answer(t *testing.T, page *fakeCDP, id string) cdpMsg {
	t.Helper()
	var got cdpMsg
	eventually(t, "the answer to "+id, func() bool {
		for _, m := range page.calls("") {
			if (m.Method == "Fetch.fulfillRequest" || m.Method == "Fetch.failRequest" || m.Method == "Fetch.continueRequest") &&
				m.params()["requestId"] == id {
				got = m
				return true
			}
		}
		return false
	})
	return got
}

func headerList(m cdpMsg) [][2]string {
	var out [][2]string
	hs, _ := m.params()["responseHeaders"].([]any)
	for _, h := range hs {
		hm := h.(map[string]any)
		out = append(out, [2]string{hm["name"].(string), hm["value"].(string)})
	}
	return out
}

func body(t *testing.T, m cdpMsg) string {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(m.params()["body"].(string))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Each mock adds its pattern to the Fetch interception of the page and of every attached
// cross-origin frame; the same pattern is sent once.
func TestMockNetworkEnablesFetch(t *testing.T) {
	page, d := inputPage(t)
	d.mu.Lock()
	d.frames["S1"] = &frame{session: "S1", target: "T1"}
	d.mu.Unlock()
	ctx := ctxT(t)
	for _, u := range []string{"*/api/*", "*/ads/*", "*/api/*"} {
		if err := d.MockNetwork(ctx, driver.NetworkMock{URL: u, Status: 200}); err != nil {
			t.Fatal(err)
		}
	}
	want := [][]string{{"*/api/*"}, {"*/api/*", "*/ads/*"}, {"*/api/*", "*/ads/*"}}
	if got := fetchPatterns(page, ""); !reflect.DeepEqual(got, want) {
		t.Errorf("page patterns %v\nwant %v", got, want)
	}
	if got := fetchPatterns(page, "S1"); !reflect.DeepEqual(got, want) {
		t.Errorf("frame patterns %v\nwant %v", got, want)
	}
	if err := d.MockNetwork(ctx, driver.NetworkMock{URL: "", Status: 200}); err == nil {
		t.Error("an empty pattern was accepted")
	}
}

// A paused request that matches is answered with the mock: status, headers and body.
func TestMockNetworkFulfills(t *testing.T) {
	page, d := inputPage(t)
	ctx := ctxT(t)
	d.MockNetwork(ctx, driver.NetworkMock{URL: "*/api/items*", Status: 201, Body: []byte(`{"items":[]}`),
		Headers: [][2]string{{"Content-Type", "application/json"}, {"X-Mock", "1"}}})
	paused(page, "", "r1", "GET", "https://shop.example/api/items?page=1")
	m := answer(t, page, "r1")
	if m.Method != "Fetch.fulfillRequest" || m.SessionID != "" {
		t.Fatalf("answer %s in %q", m.Method, m.SessionID)
	}
	if code := m.params()["responseCode"]; code != 201.0 {
		t.Errorf("responseCode %v", code)
	}
	if h := headerList(m); !reflect.DeepEqual(h, [][2]string{{"Content-Type", "application/json"}, {"X-Mock", "1"}}) {
		t.Errorf("headers %v", h)
	}
	if b := body(t, m); b != `{"items":[]}` {
		t.Errorf("body %q", b)
	}
}

// The latest matching mock wins; a mock for another method lets the request through; an
// unmatched request goes on to the network.
func TestMockNetworkPicksTheLatestMatch(t *testing.T) {
	page, d := inputPage(t)
	ctx := ctxT(t)
	d.MockNetwork(ctx, driver.NetworkMock{URL: "*/api/*", Status: 200, Body: []byte("first")})
	d.MockNetwork(ctx, driver.NetworkMock{URL: "*/api/items", Status: 200, Body: []byte("second")})
	d.MockNetwork(ctx, driver.NetworkMock{URL: "*/api/*", Method: "POST", Status: 500, Body: []byte("post")})

	paused(page, "", "a", "GET", "https://x/api/items")
	if m := answer(t, page, "a"); m.Method != "Fetch.fulfillRequest" || body(t, m) != "second" {
		t.Errorf("GET items: %s %v", m.Method, m.params())
	}
	paused(page, "", "b", "GET", "https://x/api/users")
	if m := answer(t, page, "b"); m.Method != "Fetch.fulfillRequest" || body(t, m) != "first" {
		t.Errorf("GET users: %s %v", m.Method, m.params())
	}
	paused(page, "", "c", "post", "https://x/api/items")
	if m := answer(t, page, "c"); m.Method != "Fetch.fulfillRequest" || body(t, m) != "post" || m.params()["responseCode"] != 500.0 {
		t.Errorf("POST items: %s %v", m.Method, m.params())
	}
	paused(page, "", "d", "GET", "https://x/other")
	if m := answer(t, page, "d"); m.Method != "Fetch.continueRequest" {
		t.Errorf("unmatched: %s", m.Method)
	}

	page2, d2 := inputPage(t)
	d2.MockNetwork(ctx, driver.NetworkMock{URL: "*/api/*", Method: "DELETE", Status: 204})
	paused(page2, "", "e", "GET", "https://x/api/items")
	if m := answer(t, page2, "e"); m.Method != "Fetch.continueRequest" {
		t.Errorf("method mismatch: %s", m.Method)
	}
}

// fail: true fails the request as a network error.
func TestMockNetworkFails(t *testing.T) {
	page, d := inputPage(t)
	d.MockNetwork(ctxT(t), driver.NetworkMock{URL: "*/ads/*", Fail: true})
	paused(page, "", "r1", "GET", "https://ads.example/ads/banner.js")
	m := answer(t, page, "r1")
	if m.Method != "Fetch.failRequest" || m.params()["errorReason"] != "Failed" {
		t.Errorf("answer %s %v", m.Method, m.params())
	}
}

// A request paused in a cross-origin frame is answered in that frame's session.
func TestMockNetworkInFrame(t *testing.T) {
	page, d := inputPage(t)
	d.MockNetwork(ctxT(t), driver.NetworkMock{URL: "*", Status: 200, Body: []byte("framed")})
	paused(page, "S1", "f1", "GET", "https://pay.example/api")
	if m := answer(t, page, "f1"); m.Method != "Fetch.fulfillRequest" || m.SessionID != "S1" || body(t, m) != "framed" {
		t.Errorf("answer %s in %q", m.Method, m.SessionID)
	}
}

// A frame attached while mocks are active intercepts too, before it is let run.
func TestMockNetworkFrameAttachedLater(t *testing.T) {
	page, d := inputPage(t)
	d.MockNetwork(ctxT(t), driver.NetworkMock{URL: "*/api/*", Status: 200})
	d.onEvent("Target.attachedToTarget", raw(map[string]any{"sessionId": "S2",
		"targetInfo": map[string]string{"targetId": "T2", "type": "iframe", "url": "https://pay.example/"}}), "")
	eventually(t, "the frame's setup", func() bool { return len(page.calls("Runtime.evaluate")) == 1 })
	var s2 []string
	for _, m := range page.calls("") {
		if m.SessionID == "S2" {
			s2 = append(s2, m.Method)
		}
	}
	fetch, run := -1, -1
	for i, m := range s2 {
		switch m {
		case "Fetch.enable":
			fetch = i
		case "Runtime.runIfWaitingForDebugger":
			run = i
		}
	}
	if fetch < 0 || fetch > run {
		t.Errorf("frame setup %v: Fetch.enable must come before the frame runs", s2)
	}
	if got := fetchPatterns(page, "S2"); !reflect.DeepEqual(got, [][]string{{"*/api/*"}}) {
		t.Errorf("frame patterns %v", got)
	}
}

// delay holds the response back; clearing the mocks lets a held request go on to the network
// at once, stops interception, and the held response is never sent.
func TestMockNetworkDelayAndClear(t *testing.T) {
	page, d := inputPage(t)
	ctx := ctxT(t)
	d.MockNetwork(ctx, driver.NetworkMock{URL: "*", Status: 200, Body: []byte("slow"), Delay: 150 * time.Millisecond})
	start := time.Now()
	paused(page, "", "r1", "GET", "https://x/a")
	m := answer(t, page, "r1")
	if el := time.Since(start); el < 140*time.Millisecond || m.Method != "Fetch.fulfillRequest" {
		t.Errorf("answered %s after %v, want a fulfill after 150 ms", m.Method, el)
	}

	d.MockNetwork(ctx, driver.NetworkMock{URL: "*", Status: 200, Body: []byte("never"), Delay: 10 * time.Second})
	paused(page, "", "r2", "GET", "https://x/b")
	eventually(t, "the request held", func() bool { d.mockMu.Lock(); defer d.mockMu.Unlock(); return len(d.held) == 1 })
	if err := d.ClearNetworkMocks(ctx); err != nil {
		t.Fatal(err)
	}
	if m := answer(t, page, "r2"); m.Method != "Fetch.continueRequest" {
		t.Errorf("held request: %s", m.Method)
	}
	if n := len(page.calls("Fetch.disable")); n != 1 {
		t.Errorf("Fetch.disable sent %d times", n)
	}
	// Nothing is mocked now: a request still paused (sent before the disable) goes on.
	paused(page, "", "r3", "GET", "https://x/c")
	if m := answer(t, page, "r3"); m.Method != "Fetch.continueRequest" {
		t.Errorf("after clear: %s", m.Method)
	}
}

// Clearing with nothing mocked sends nothing.
func TestClearNetworkMocksIdle(t *testing.T) {
	page, d := inputPage(t)
	if err := d.ClearNetworkMocks(ctxT(t)); err != nil {
		t.Fatal(err)
	}
	if calls := page.calls(""); len(calls) != 0 {
		t.Errorf("calls %v", page.methods())
	}
}

// Closing the driver drops held responses without sending them.
func TestMockNetworkCloseDropsHeld(t *testing.T) {
	page, d := inputPage(t)
	d.MockNetwork(ctxT(t), driver.NetworkMock{URL: "*", Status: 200, Delay: 50 * time.Millisecond})
	paused(page, "", "r1", "GET", "https://x/a")
	eventually(t, "the request held", func() bool { d.mockMu.Lock(); defer d.mockMu.Unlock(); return len(d.held) == 1 })
	d.Close()
	time.Sleep(100 * time.Millisecond)
	if n := len(page.calls("Fetch.fulfillRequest")); n != 0 {
		t.Errorf("a held response was sent after Close")
	}
	d.mockMu.Lock()
	defer d.mockMu.Unlock()
	if len(d.held) != 0 {
		t.Errorf("held %d", len(d.held))
	}
}
