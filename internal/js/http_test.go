package js

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// seen is what the test server received for one request.
type seen struct {
	Method      string
	Path        string
	Body        string
	Header      http.Header
	ContentType string
	Fields      map[string]string
	Files       map[string]string // field -> "filename:content"
}

// echoServer records each request and answers according to the path.
func echoServer(t *testing.T) (*httptest.Server, func() seen) {
	t.Helper()
	var mu sync.Mutex
	var last seen
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s := seen{Method: r.Method, Path: r.URL.Path, Header: r.Header.Clone(), ContentType: r.Header.Get("Content-Type")}
		if strings.HasPrefix(s.ContentType, "multipart/form-data") {
			if err := r.ParseMultipartForm(1 << 20); err == nil {
				s.Fields = map[string]string{}
				for k, v := range r.MultipartForm.Value {
					s.Fields[k] = strings.Join(v, ",")
				}
				s.Files = map[string]string{}
				for k, fhs := range r.MultipartForm.File {
					f, _ := fhs[0].Open()
					b, _ := io.ReadAll(f)
					f.Close()
					s.Files[k] = fhs[0].Filename + ":" + string(b)
				}
			}
		} else {
			b, _ := io.ReadAll(r.Body)
			s.Body = string(b)
		}
		mu.Lock()
		last = s
		mu.Unlock()
		switch r.URL.Path {
		case "/json":
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Custom-Thing", "yes")
			io.WriteString(w, `{"user":{"name":"ann"},"items":[1,2,3]}`)
		case "/multi":
			w.Header().Add("X-Multi", "a")
			w.Header().Add("X-Multi", "b")
			io.WriteString(w, "multi")
		case "/created":
			w.WriteHeader(http.StatusCreated)
			io.WriteString(w, "made")
		case "/nocontent":
			w.WriteHeader(http.StatusNoContent)
		case "/edge":
			w.WriteHeader(299)
		case "/redirect-target":
			io.WriteString(w, "landed")
		case "/redirect":
			http.Redirect(w, r, "/redirect-target", http.StatusFound)
		case "/missing":
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, "not here")
		case "/boom":
			w.WriteHeader(http.StatusInternalServerError)
			io.WriteString(w, "server error")
		default:
			io.WriteString(w, "method="+r.Method)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, func() seen { mu.Lock(); defer mu.Unlock(); return last }
}

func evalHTTP(t *testing.T, e *Engine, src string, srv *httptest.Server) string {
	t.Helper()
	got, err := e.Interpolate(src, scope(map[string]string{"BASE": srv.URL}))
	if err != nil {
		t.Fatalf("Interpolate(%q): %v", src, err)
	}
	return got
}

// Each http method helper sends its HTTP method.
func TestHTTPMethods(t *testing.T) {
	srv, last := echoServer(t)
	e := New("android")
	for _, m := range []string{"get", "post", "put", "delete"} {
		t.Run(m, func(t *testing.T) {
			got := evalHTTP(t, e, "${http."+m+"(BASE + '/echo').body}", srv)
			want := "method=" + strings.ToUpper(m)
			if got != want {
				t.Errorf("body = %q, want %q", got, want)
			}
			if last().Method != strings.ToUpper(m) || last().Path != "/echo" {
				t.Errorf("server saw %s %s", last().Method, last().Path)
			}
		})
	}
}

// http.request defaults to GET and takes the method from params (case-insensitive).
func TestHTTPRequestMethod(t *testing.T) {
	srv, last := echoServer(t)
	e := New("android")
	if got := evalHTTP(t, e, "${http.request(BASE + '/r').body}", srv); got != "method=GET" {
		t.Errorf("request default = %q", got)
	}
	if got := evalHTTP(t, e, "${http.request(BASE + '/r', {method: 'patch', body: 'p'}).body}", srv); got != "method=PATCH" {
		t.Errorf("request patch = %q", got)
	}
	if last().Body != "p" {
		t.Errorf("patch body = %q", last().Body)
	}
	// A method in params overrides the helper's own method too.
	if got := evalHTTP(t, e, "${http.get(BASE + '/r', {method: 'Delete'}).body}", srv); got != "method=DELETE" {
		t.Errorf("get with method override = %q", got)
	}
	// An empty method is ignored.
	if got := evalHTTP(t, e, "${http.post(BASE + '/r', {method: ''}).body}", srv); got != "method=POST" {
		t.Errorf("post with empty method = %q", got)
	}
}

// Request bodies and headers reach the server; non-string values are stringified.
func TestHTTPBodyAndHeaders(t *testing.T) {
	srv, last := echoServer(t)
	e := New("android")
	src := `${http.post(BASE + '/echo', {
		headers: {'Content-Type': 'application/json', 'X-Token': TOKEN, 'X-Num': 7},
		body: JSON.stringify({name: 'ann', n: 1})
	}).status}`
	got, err := e.Interpolate(src, scope(map[string]string{"BASE": srv.URL, "TOKEN": "secret-token"}))
	if err != nil {
		t.Fatal(err)
	}
	if got != "200" {
		t.Errorf("status = %q", got)
	}
	s := last()
	var body map[string]any
	if err := json.Unmarshal([]byte(s.Body), &body); err != nil || body["name"] != "ann" {
		t.Errorf("body = %q (%v)", s.Body, err)
	}
	if s.ContentType != "application/json" || s.Header.Get("X-Token") != "secret-token" || s.Header.Get("X-Num") != "7" {
		t.Errorf("headers = %v", s.Header)
	}
	evalHTTP(t, e, "${http.put(BASE + '/echo', {body: 12.5}).ok}", srv)
	if last().Body != "12.5" {
		t.Errorf("numeric body = %q", last().Body)
	}
	evalHTTP(t, e, "${http.post(BASE + '/echo', {body: null}).ok}", srv)
	if last().Body != "" {
		t.Errorf("null body = %q", last().Body)
	}
	evalHTTP(t, e, "${http.post(BASE + '/echo', null).ok}", srv)
	if last().Method != "POST" || last().Body != "" {
		t.Errorf("null params: %+v", last())
	}
}

// The response object has ok, status, body and lower-cased headers (repeated ones joined).
func TestHTTPResponseShape(t *testing.T) {
	srv, _ := echoServer(t)
	e := New("android")
	cases := []struct{ src, want string }{
		{"${http.get(BASE + '/json').ok}", "true"},
		{"${http.get(BASE + '/json').status}", "200"},
		{"${json(http.get(BASE + '/json').body).user.name}", "ann"},
		{"${json(http.get(BASE + '/json').body).items.length}", "3"},
		{"${http.get(BASE + '/json').headers['content-type']}", "application/json"},
		{"${http.get(BASE + '/json').headers['x-custom-thing']}", "yes"},
		{"${typeof http.get(BASE + '/json').headers['X-Custom-Thing']}", "undefined"},
		{"${http.get(BASE + '/multi').headers['x-multi']}", "a,b"},
		{"${http.post(BASE + '/created').status}", "201"},
		{"${http.post(BASE + '/created').ok}", "true"},
		{"${http.post(BASE + '/created').body}", "made"},
		{"${http.delete(BASE + '/nocontent').status}", "204"},
		{"[${http.delete(BASE + '/nocontent').body}]", "[]"},
		{"${http.get(BASE + '/edge').ok}", "true"},
		{"${http.get(BASE + '/missing').ok}", "false"},
		{"${http.get(BASE + '/missing').status}", "404"},
		{"${http.get(BASE + '/missing').body}", "not here"},
		{"${http.get(BASE + '/boom').ok}", "false"},
		{"${http.get(BASE + '/boom').status}", "500"},
		{"${http.get(BASE + '/redirect').body}", "landed"},
		{"${http.get(BASE + '/redirect').status}", "200"},
		{"${typeof http.get(BASE + '/json').status}", "number"},
	}
	for _, c := range cases {
		t.Run(c.src, func(t *testing.T) {
			if got := evalHTTP(t, e, c.src, srv); got != c.want {
				t.Errorf("%s = %q, want %q", c.src, got, c.want)
			}
		})
	}
}

// Responses can be stored in output from a runScript and read back later.
func TestHTTPFromRunScript(t *testing.T) {
	srv, _ := echoServer(t)
	e := New("android")
	src := `
var r = http.get(BASE + '/json');
output.name = json(r.body).user.name;
output.status = r.status;
`
	if err := e.RunScript(src, t.TempDir(), scope(map[string]string{"BASE": srv.URL})); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.Interpolate("${output.name}:${output.status}", nil); got != "ann:200" {
		t.Errorf("got %q", got)
	}
}

// multipartForm sends fields and files; relative file paths resolve against the script's dir.
func TestHTTPMultipartForm(t *testing.T) {
	srv, last := echoServer(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "photo.txt"), []byte("relative bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	abs := filepath.Join(t.TempDir(), "abs.bin")
	if err := os.WriteFile(abs, []byte("absolute bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	e := New("android")
	src := `output.st = http.post(BASE + '/upload', {
		body: 'ignored when multipart',
		multipartForm: {
			title: 'hello',
			count: 3,
			rel: {filePath: 'photo.txt'},
			abs: {filePath: ABS}
		}
	}).status`
	if err := e.RunScript(src, dir, scope(map[string]string{"BASE": srv.URL, "ABS": abs})); err != nil {
		t.Fatal(err)
	}
	s := last()
	if !strings.HasPrefix(s.ContentType, "multipart/form-data; boundary=") {
		t.Fatalf("content type = %q", s.ContentType)
	}
	if s.Fields["title"] != "hello" || s.Fields["count"] != "3" {
		t.Errorf("fields = %v", s.Fields)
	}
	if s.Files["rel"] != "photo.txt:relative bytes" || s.Files["abs"] != "abs.bin:absolute bytes" {
		t.Errorf("files = %v", s.Files)
	}
	if got, _ := e.Interpolate("${output.st}", nil); got != "200" {
		t.Errorf("status = %q", got)
	}
}

// A missing multipart file is a JavaScript error (and catchable inside the script).
func TestHTTPMultipartMissingFile(t *testing.T) {
	srv, _ := echoServer(t)
	e := New("android")
	sc := scope(map[string]string{"BASE": srv.URL})
	err := e.RunScript("http.post(BASE + '/u', {multipartForm: {f: {filePath: 'nope.txt'}}})", t.TempDir(), sc)
	var je *Error
	if !errors.As(err, &je) || !strings.Contains(je.Msg, "nope.txt") {
		t.Fatalf("err = %v, want a JavaScript error naming the file", err)
	}
	got, err := e.Interpolate("${(function(){ try { http.post(BASE + '/u', {multipartForm: {f: {filePath: '/no/such/file'}}}); return 'sent' } catch (e) { return 'caught' } })()}", sc)
	if err != nil || got != "caught" {
		t.Errorf("try/catch = %q, %v", got, err)
	}
}

// Network failures and bad URLs surface as JavaScript errors that scripts can catch.
func TestHTTPErrors(t *testing.T) {
	closed := httptest.NewServer(http.NotFoundHandler())
	url := closed.URL
	closed.Close()
	e := New("android")
	sc := scope(map[string]string{"DEAD": url})
	for _, src := range []string{
		"http.get(DEAD + '/x')",
		"http.get('::not a url')",
		"http.request('http://bad host/')",
	} {
		t.Run(src, func(t *testing.T) {
			_, err := e.Eval(src, sc)
			var je *Error
			if !errors.As(err, &je) {
				t.Fatalf("err = %v, want *Error", err)
			}
		})
	}
	got, err := e.Interpolate("${(function(){ try { http.get(DEAD); return 'ok' } catch (e) { return 'caught' } })()}", sc)
	if err != nil || got != "caught" {
		t.Errorf("try/catch = %q, %v", got, err)
	}
}

// The http object exposes exactly the documented helpers.
func TestHTTPObjectKeys(t *testing.T) {
	e := New("android")
	if got, _ := e.Interpolate("${Object.keys(http).sort().join(',')}", nil); got != "delete,get,post,put,request" {
		t.Errorf("http keys = %q", got)
	}
}
