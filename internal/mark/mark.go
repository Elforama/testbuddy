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

const DefaultPort = 7357

const EnvURL = "TB_EVENTS_URL"

const maxEvents = 100_000

const maxBody = 1 << 20

const maxText = 512

type Event struct {
	At     time.Time
	Text   string
	Device string
}

type Hub struct {
	mu      sync.Mutex
	events  []Event
	dropped int
	now     func() time.Time
}

func NewHub() *Hub { return &Hub{now: time.Now} }

func (h *Hub) Add(device string, texts ...string) int {
	evs := make([]Event, len(texts))
	for i, t := range texts {
		evs[i] = Event{Text: t, Device: device}
	}
	return h.AddEvents(evs)
}

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

func (h *Hub) After(t time.Time) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, e := range h.events {
		if e.At.After(t) {
			n++
		}
	}
	return n
}

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

func (h *Hub) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		hd.Set("Access-Control-Allow-Origin", "*")
		hd.Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		hd.Set("Access-Control-Allow-Headers", "Content-Type")
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

type Server struct {
	URL    string
	Port   int
	Shared bool
	srv    *http.Server
	ln     net.Listener
	file   string
}

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
			s.Shared = true
		} else {
			s.file = AddrFile(dir)
			os.MkdirAll(dir, 0o755)
			os.WriteFile(s.file, []byte(s.URL+"\n"), 0o644)
		}
	}
	return s, nil
}

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

func AddrFile(dir string) string { return filepath.Join(dir, "events.url") }

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
