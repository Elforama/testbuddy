// Package mark takes events from outside a run, for the run's annotated videos: an app's
// tracking calls, a backend's webhooks, a script (`tb mark`). `tb test --record --annotate`
// serves an endpoint on the computer's loopback address while it runs; each event sent there
// is stamped with tb's own clock as it arrives, the clock the videos' captions are timed by,
// so the sender needs no clock of its own and nothing to do with the video. After the run each
// video shows the events of its stretch in a band of their own (package annotate, events.go).
//
// The endpoint, POST /event (or /events):
//
//   - a text body: each line that is not blank is an event;
//   - a JSON body: an event, or a list of them. An event is a string, or an object with
//     "text"; or, for tracking calls forwarded as they are, "event" or "name" with
//     "properties", "props" or "params", shown as name {key: value, ...};
//   - "device" (in the object, or ?device= in the URL): the device the event is for (serial,
//     UDID, name, or role in a multi-device flow). An event that names none is for every
//     device recording at the time.
//
// Answers allow any origin (CORS), so a web app under test can send its events from the page;
// any page open on the computer could then send events too, which is no worse than a script
// on it, and the endpoint lasts only as long as the run.
package mark

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultPort is the endpoint's port when --events-port does not say: one an app's debug build
// can name without asking tb.
const DefaultPort = 7357

// EnvURL is the variable that holds the endpoint's URL during a run: in tb's environment, so
// the processes it starts inherit it, and in the flows' (${TB_EVENTS_URL}), so a flow can hand
// it to the app (launchApp arguments).
const EnvURL = "TB_EVENTS_URL"

// maxEvents bounds what a run keeps; later events are dropped.
const maxEvents = 100_000

// maxBody bounds a request's body.
const maxBody = 1 << 20

// maxText bounds an event's text, in runes: a row of the video shows far less, and a run keeps
// up to maxEvents of them.
const maxText = 512

// Event is one event: its text, when tb got it, and the device it is for ("" = every device).
type Event struct {
	At     time.Time
	Text   string
	Device string
}

// Hub keeps the events sent during a run, in the order they came.
type Hub struct {
	mu      sync.Mutex
	events  []Event
	dropped int
	now     func() time.Time // tests replace it
}

// NewHub returns an empty hub.
func NewHub() *Hub { return &Hub{now: time.Now} }

// Add keeps texts as events for device, stamped now, and returns how many it kept.
func (h *Hub) Add(device string, texts ...string) int {
	evs := make([]Event, len(texts))
	for i, t := range texts {
		evs[i] = Event{Text: t, Device: device}
	}
	return h.AddEvents(evs)
}

// AddEvents keeps evs (their At is ignored) in their order, all stamped now as one burst, and
// returns how many it kept: those with text, up to maxEvents, each cut to maxText runes.
func (h *Hub) AddEvents(evs []Event) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	at := h.now()
	n := 0
	for _, e := range evs {
		t := strings.TrimSpace(e.Text)
		if t == "" {
			continue
		}
		if r := []rune(t); len(r) > maxText {
			t = string(r[:maxText-1]) + "…"
		}
		if len(h.events) >= maxEvents {
			h.dropped++
			continue
		}
		h.events = append(h.events, Event{At: at, Text: t, Device: e.Device})
		n++
	}
	return n
}

// Between returns the events from start to end (inclusive) for a device known by any of
// names: those that name it, and those that name no device.
func (h *Hub) Between(start, end time.Time, names ...string) []Event {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []Event
	for _, e := range h.events {
		if e.At.Before(start) || e.At.After(end) || !forDevice(e.Device, names) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// Count is how many events the hub kept, and how many it dropped (over maxEvents).
func (h *Hub) Count() (kept, dropped int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.events), h.dropped
}

func forDevice(device string, names []string) bool {
	if device == "" {
		return true
	}
	for _, n := range names {
		if n != "" && strings.EqualFold(n, device) {
			return true
		}
	}
	return false
}

// Handler serves the endpoint (see the package's comment) into h.
func (h *Hub) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		hd.Set("Access-Control-Allow-Origin", "*")
		hd.Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		hd.Set("Access-Control-Allow-Headers", "Content-Type")
		// Chrome asks before a public page reaches a loopback address
		hd.Set("Access-Control-Allow-Private-Network", "true")
		if r.URL.Path != "/event" && r.URL.Path != "/events" {
			http.Error(w, "tb takes events at POST /event", http.StatusNotFound)
			return
		}
		switch r.Method {
		case http.MethodOptions:
			w.WriteHeader(http.StatusNoContent)
			return
		case http.MethodPost:
		default:
			http.Error(w, "POST events to /event", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if len(body) > maxBody {
			http.Error(w, "body over 1 MB", http.StatusRequestEntityTooLarge)
			return
		}
		evs, err := Parse(body, r.Header.Get("Content-Type"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// one burst: stamped together, in the order they came
		dev := r.URL.Query().Get("device")
		for i := range evs {
			if evs[i].Device == "" {
				evs[i].Device = dev
			}
		}
		n := h.AddEvents(evs)
		hd.Set("Content-Type", "application/json")
		fmt.Fprintf(w, "{\"ok\":true,\"count\":%d}\n", n)
	})
}

// Parse reads a request's events (see the package's comment). Their At is left zero. A body
// is JSON when its type says so; with another type, a body that reads as JSON is taken as JSON
// and anything else as text, a line an event (so "[cart] item_added" is text).
func Parse(body []byte, contentType string) ([]Event, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil, errors.New("no event in the body")
	}
	saysJSON := strings.Contains(contentType, "json")
	if saysJSON || trimmed[0] == '{' || trimmed[0] == '[' || trimmed[0] == '"' {
		evs, err := parseJSON(trimmed)
		if err == nil || saysJSON {
			return evs, err
		}
	}
	var out []Event
	sc := bufio.NewScanner(bytes.NewReader(trimmed))
	sc.Buffer(make([]byte, 0, 64*1024), maxBody)
	for sc.Scan() {
		if t := strings.TrimSpace(sc.Text()); t != "" {
			out = append(out, Event{Text: t})
		}
	}
	return out, nil
}

// parseJSON reads a JSON body: an event, a list of them, or several of either one after the
// other (NDJSON).
func parseJSON(body []byte) ([]Event, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var items []any
	for {
		var v any
		err := dec.Decode(&v)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("the body is not JSON: %v", err)
		}
		if list, ok := v.([]any); ok {
			items = append(items, list...)
		} else {
			items = append(items, v)
		}
	}
	var out []Event
	for i, it := range items {
		e, err := parseItem(it)
		if err != nil {
			if len(items) > 1 {
				return nil, fmt.Errorf("event %d: %v", i+1, err)
			}
			return nil, err
		}
		out = append(out, e)
	}
	if len(out) == 0 {
		return nil, errors.New("no event in the body")
	}
	return out, nil
}

func parseItem(it any) (Event, error) {
	switch x := it.(type) {
	case string:
		if strings.TrimSpace(x) == "" {
			return Event{}, errors.New("an empty event")
		}
		return Event{Text: x}, nil
	case map[string]any:
		e := Event{}
		if d, ok := x["device"].(string); ok {
			e.Device = d
		}
		if t, ok := x["text"].(string); ok && strings.TrimSpace(t) != "" {
			e.Text = t
			return e, nil
		}
		name := ""
		for _, k := range []string{"event", "name"} {
			if s, ok := x[k].(string); ok && s != "" {
				name = s
				break
			}
		}
		if name == "" {
			return Event{}, errors.New(`an event needs "text", or "event" or "name"`)
		}
		for _, k := range []string{"properties", "props", "params"} {
			if p, ok := x[k].(map[string]any); ok && len(p) > 0 {
				name += " " + compact(p)
				break
			}
		}
		e.Text = name
		return e, nil
	default:
		return Event{}, errors.New("an event is a string or an object")
	}
}

// compact writes an object as {key: value, ...}, keys in order, strings unquoted unless they
// hold a space, comma or brace: short enough to read in a row of the video.
func compact(m map[string]any) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("{")
	for i, k := range keys {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(k)
		b.WriteString(": ")
		b.WriteString(value(m[k]))
	}
	b.WriteString("}")
	return b.String()
}

func value(v any) string {
	switch x := v.(type) {
	case string:
		if strings.ContainsAny(x, " ,{}:") || x == "" {
			return strconv.Quote(x)
		}
		return x
	case map[string]any:
		return compact(x)
	case nil:
		return "null"
	default:
		b, err := json.Marshal(x)
		if err != nil {
			return fmt.Sprint(x)
		}
		return string(b)
	}
}

// Server is the endpoint while a run lasts.
type Server struct {
	URL  string // http://127.0.0.1:PORT/event
	Port int
	// Shared says another run's endpoint was already in the address file and still answers:
	// the file is left to it, so tb mark without --url sends there, not here.
	Shared bool
	srv    *http.Server
	ln     net.Listener
	file   string // the address file, removed at Close
}

// Serve serves h's endpoint on the loopback address at port (0: any free port), and writes its
// URL to the address file in dir (so `tb mark` finds it). It fails when the port is taken.
func Serve(h *Hub, port int, dir string) (*Server, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		return nil, err
	}
	p := ln.Addr().(*net.TCPAddr).Port
	s := &Server{URL: fmt.Sprintf("http://127.0.0.1:%d/event", p), Port: p, ln: ln,
		srv: &http.Server{Handler: h.Handler(), ReadHeaderTimeout: 10 * time.Second}}
	go s.srv.Serve(ln)
	if dir != "" {
		if b, err := os.ReadFile(AddrFile(dir)); err == nil && answers(strings.TrimSpace(string(b))) {
			s.Shared = true // another run is taking events: its file stays
		} else {
			s.file = AddrFile(dir)
			os.MkdirAll(dir, 0o755)
			os.WriteFile(s.file, []byte(s.URL+"\n"), 0o644)
		}
	}
	return s, nil
}

// answers says an endpoint is taking events at u: a run still going, not a file left by one
// that died.
func answers(u string) bool {
	if u == "" {
		return false
	}
	c := http.Client{Timeout: 500 * time.Millisecond}
	req, err := http.NewRequest(http.MethodOptions, u, nil)
	if err != nil {
		return false
	}
	resp, err := c.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusNoContent && resp.Header.Get("Access-Control-Allow-Private-Network") == "true"
}

// Close stops the endpoint and removes its address file (when it is still this server's).
func (s *Server) Close() error {
	if s.file != "" {
		if b, err := os.ReadFile(s.file); err == nil && strings.TrimSpace(string(b)) == s.URL {
			os.Remove(s.file)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return s.srv.Shutdown(ctx)
}

// AddrFile is where a running endpoint's URL is written, in tb's folder dir.
func AddrFile(dir string) string { return filepath.Join(dir, "events.url") }

// Find is the endpoint `tb mark` sends to: explicit when given, else TB_EVENTS_URL, else the
// address file in dir. An address without a path gets /event.
func Find(explicit, dir string) (string, error) {
	u := explicit
	if u == "" {
		u = os.Getenv(EnvURL)
	}
	if u == "" {
		b, err := os.ReadFile(AddrFile(dir))
		if err != nil {
			return "", errors.New("no tb test --record --annotate is running here to take events (or pass --url)")
		}
		u = strings.TrimSpace(string(b))
	}
	if !strings.Contains(u, "://") {
		u = "http://" + u
	}
	p, err := url.Parse(u)
	if err != nil || p.Host == "" {
		return "", fmt.Errorf("not an endpoint URL: %q", u)
	}
	if p.Path == "" || p.Path == "/" {
		p.Path = "/event"
	}
	return p.String(), nil
}

// Send posts texts to the endpoint as one burst, for device ("" = every device).
func Send(ctx context.Context, endpoint, device string, texts []string) (int, error) {
	type item struct {
		Text   string `json:"text"`
		Device string `json:"device,omitempty"`
	}
	var items []item
	for _, t := range texts {
		if strings.TrimSpace(t) != "" {
			items = append(items, item{Text: t, Device: device})
		}
	}
	if len(items) == 0 {
		return 0, errors.New("no event to send")
	}
	body, _ := json.Marshal(items)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("no tb run takes events at %s: %v", endpoint, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode/100 != 2 {
		return 0, fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	var r struct {
		Count int `json:"count"`
	}
	json.Unmarshal(b, &r)
	return r.Count, nil
}
