package mark

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	for _, c := range []struct {
		name, body, ctype string
		want              []Event
	}{
		{"text lines", "screen_view\n\n  button_tap  \n", "text/plain", []Event{{Text: "screen_view"}, {Text: "button_tap"}}},
		{"a JSON string", `"purchase"`, "application/json", []Event{{Text: "purchase"}}},
		{"an object with text", `{"text":"purchase","device":"emulator-5554"}`, "", []Event{{Text: "purchase", Device: "emulator-5554"}}},
		{"a list", `["a",{"text":"b"}]`, "application/json", []Event{{Text: "a"}, {Text: "b"}}},
		{"a tracking call", `{"event":"purchase","properties":{"sku":"42","price":9.99,"title":"Blue mug"}}`, "",
			[]Event{{Text: `purchase {price: 9.99, sku: 42, title: "Blue mug"}`}}},
		{"name and params", `{"name":"login","params":{"method":"email"}}`, "", []Event{{Text: "login {method: email}"}}},
		{"name alone", `{"name":"logout"}`, "", []Event{{Text: "logout"}}},
		{"text that looks like JSON", "[cart] item_added\n{retry} sync", "text/plain", []Event{{Text: "[cart] item_added"}, {Text: "{retry} sync"}}},
		{"form-encoded text from curl -d", "[cart] item_added", "application/x-www-form-urlencoded", []Event{{Text: "[cart] item_added"}}},
		{"NDJSON", "{\"text\":\"a\"}\n{\"text\":\"b\"}\n", "application/x-ndjson", []Event{{Text: "a"}, {Text: "b"}}},
	} {
		got, err := Parse([]byte(c.body), c.ctype)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if len(got) != len(c.want) {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: %+v, want %+v", c.name, got[i], c.want[i])
			}
		}
	}
	for _, bad := range []string{"", "  ", `{"properties":{}}`, `[1]`, `{"text":`} {
		if _, err := Parse([]byte(bad), "application/json"); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
}

func TestHandlerTakesABurst(t *testing.T) {
	h := NewHub()
	at := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	h.now = func() time.Time { return at }
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/event?device=pixel", "application/json",
		strings.NewReader(`["a","b",{"text":"c","device":"iphone"}]`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("status %d, CORS %q", resp.StatusCode, resp.Header.Get("Access-Control-Allow-Origin"))
	}
	got := h.Between(at, at, "pixel")
	if len(got) != 2 || got[0].Text != "a" || got[1].Text != "b" || !got[0].At.Equal(at) {
		t.Errorf("for pixel %+v", got)
	}
	if got := h.Between(at, at, "iPhone"); len(got) != 1 || got[0].Text != "c" {
		t.Errorf("for iphone %+v", got)
	}
}

func TestHandlerKeepsARequestsOrder(t *testing.T) {
	h := NewHub()
	n := 0
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	h.now = func() time.Time { n++; return base.Add(time.Duration(n) * time.Millisecond) }
	srv := httptest.NewServer(h.Handler())
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/event", "application/json",
		strings.NewReader(`["cart_opened",{"text":"push_shown","device":"pixel"},"checkout"]`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	got := h.Between(base, base.Add(time.Hour), "pixel")
	var texts []string
	for _, e := range got {
		texts = append(texts, e.Text)
		if !e.At.Equal(got[0].At) {
			t.Errorf("%s at %s, not with the rest at %s", e.Text, e.At, got[0].At)
		}
	}
	if strings.Join(texts, ",") != "cart_opened,push_shown,checkout" {
		t.Errorf("order %v", texts)
	}
}

func TestHandlerAnswersThePreflight(t *testing.T) {
	srv := httptest.NewServer(NewHub().Handler())
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodOptions, srv.URL+"/event", nil)
	req.Header.Set("Access-Control-Request-Private-Network", "true")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent || resp.Header.Get("Access-Control-Allow-Private-Network") != "true" ||
		!strings.Contains(resp.Header.Get("Access-Control-Allow-Methods"), "POST") {
		t.Errorf("status %d, headers %v", resp.StatusCode, resp.Header)
	}
}

func TestHandlerRefusesWhatIsNotAnEvent(t *testing.T) {
	srv := httptest.NewServer(NewHub().Handler())
	defer srv.Close()
	for _, c := range []struct {
		method, path, body string
		want               int
	}{
		{"GET", "/event", "", http.StatusMethodNotAllowed},
		{"POST", "/other", "a", http.StatusNotFound},
		{"POST", "/event", "", http.StatusBadRequest},
		{"POST", "/event", strings.Repeat("a", maxBody+10), http.StatusRequestEntityTooLarge},
	} {
		req, _ := http.NewRequest(c.method, srv.URL+c.path, strings.NewReader(c.body))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != c.want {
			t.Errorf("%s %s: %d, want %d", c.method, c.path, resp.StatusCode, c.want)
		}
	}
}

func TestHubBetween(t *testing.T) {
	h := NewHub()
	t0 := time.Now()
	for i, d := range []string{"", "a", "b", ""} {
		at := t0.Add(time.Duration(i) * time.Second)
		h.now = func() time.Time { return at }
		h.Add(d, "e"+string(rune('0'+i)))
	}
	got := h.Between(t0.Add(500*time.Millisecond), t0.Add(3*time.Second), "a", "")
	if len(got) != 2 || got[0].Text != "e1" || got[1].Text != "e3" {
		t.Errorf("%+v", got)
	}
}

func TestServeSendFind(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvURL, "")
	h := NewHub()
	s, err := Serve(h, 0, dir)
	if err != nil {
		t.Fatal(err)
	}
	u, err := Find("", dir)
	if err != nil || u != s.URL {
		t.Fatalf("found %q %v, want %q", u, err, s.URL)
	}
	n, err := Send(context.Background(), u, "pixel", []string{"a", " ", "b"})
	if err != nil || n != 2 {
		t.Fatalf("sent %d %v", n, err)
	}
	if got := h.Between(time.Now().Add(-time.Minute), time.Now(), "pixel"); len(got) != 2 {
		t.Errorf("%+v", got)
	}
	if _, err := Serve(NewHub(), s.Port, ""); err == nil {
		t.Error("served twice on one port")
	}
	s.Close()
	if _, err := os.Stat(AddrFile(dir)); !os.IsNotExist(err) {
		t.Errorf("address file left: %v", err)
	}
	if _, err := Find("", dir); err == nil {
		t.Error("found an endpoint after Close")
	}
}

func TestFindPrefersTheFlagThenTheEnvironment(t *testing.T) {
	t.Setenv(EnvURL, "http://127.0.0.1:9000/event")
	if u, _ := Find("127.0.0.1:8000", t.TempDir()); u != "http://127.0.0.1:8000/event" {
		t.Errorf("flag: %q", u)
	}
	if u, _ := Find("", t.TempDir()); u != "http://127.0.0.1:9000/event" {
		t.Errorf("environment: %q", u)
	}
	if u, _ := Find("http://127.0.0.1:7357?device=pixel", ""); u != "http://127.0.0.1:7357/event?device=pixel" {
		t.Errorf("with a query: %q", u)
	}
}

func TestHubCutsLongText(t *testing.T) {
	h := NewHub()
	h.Add("", strings.Repeat("x", 10_000))
	got := h.Between(time.Time{}, time.Now().Add(time.Minute))
	if len(got) != 1 || len([]rune(got[0].Text)) != maxText || !strings.HasSuffix(got[0].Text, "…") {
		t.Errorf("kept %d runes", len([]rune(got[0].Text)))
	}
}

func TestServeLeavesALiveRunsAddress(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvURL, "")
	a, err := Serve(NewHub(), 0, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := Serve(NewHub(), 0, dir)
	if err != nil {
		t.Fatal(err)
	}
	if u, _ := Find("", dir); u != a.URL {
		t.Errorf("tb mark finds %q, want the first run's %q", u, a.URL)
	}
	if !b.Shared {
		t.Error("the second run does not know tb mark finds the first")
	}
	b.Close()
	if u, _ := Find("", dir); u != a.URL {
		t.Errorf("after the second ended, tb mark finds %q, want %q", u, a.URL)
	}
	a.Close()
	os.WriteFile(AddrFile(dir), []byte("http://127.0.0.1:1/event\n"), 0o644)
	c, err := Serve(NewHub(), 0, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if u, _ := Find("", dir); u != c.URL || c.Shared {
		t.Errorf("tb mark finds %q, want the new run's %q", u, c.URL)
	}
}
