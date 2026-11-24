package js

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"

	"github.com/elforama/testbuddy/internal/legacy"
)

type Engine struct {
	mu                sync.Mutex
	rt                *goja.Runtime
	output            *goja.Object
	tb                *goja.Object
	permanent         map[string]bool
	Log               func(string)
	log               func(string)
	ScriptDir         string
	timers            map[int64]*jsTimer
	timerID, timerSeq int64
}

type Error struct {
	Msg    string
	Syntax bool
}

func (e *Error) Error() string { return "JavaScript error: " + e.Msg }

const prelude = `
(function(g){
  var proxy = new Proxy({}, {
    has: function(t, k) { return typeof k === 'string'; },
    get: function(t, k) { return undefined; }
  });
  Object.setPrototypeOf(g, proxy);
})(this);
`

func New(platform string) *Engine {
	rt := goja.New()
	rt.SetFieldNameMapper(goja.UncapFieldNameMapper())
	e := &Engine{rt: rt, permanent: map[string]bool{}}
	for _, k := range rt.GlobalObject().Keys() {
		e.permanent[k] = true
	}
	for _, k := range []string{"Object", "Function", "Array", "String", "Number", "Boolean", "Symbol", "Math",
		"JSON", "Date", "RegExp", "Error", "TypeError", "RangeError", "SyntaxError", "ReferenceError", "EvalError",
		"URIError", "Promise", "Proxy", "Reflect", "Map", "Set", "WeakMap", "WeakSet", "ArrayBuffer", "DataView",
		"Int8Array", "Uint8Array", "Uint8ClampedArray", "Int16Array", "Uint16Array", "Int32Array", "Uint32Array",
		"Float32Array", "Float64Array", "BigInt", "BigInt64Array", "BigUint64Array", "parseInt", "parseFloat",
		"isNaN", "isFinite", "encodeURI", "encodeURIComponent", "decodeURI", "decodeURIComponent", "escape",
		"unescape", "eval", "globalThis", "undefined", "NaN", "Infinity", "AggregateError", "WeakRef"} {
		e.permanent[k] = true
	}
	e.output = rt.NewObject()
	e.tb = rt.NewObject()
	e.tb.Set("platform", platform)
	rt.Set("output", e.output)
	rt.Set("tb", e.tb)
	rt.Set(legacy.ScriptObject(), e.tb)
	rt.Set("json", func(s string) (goja.Value, error) {
		v, err := rt.RunString("JSON.parse")
		if err != nil {
			return nil, err
		}
		fn, _ := goja.AssertFunction(v)
		return fn(goja.Undefined(), rt.ToValue(s))
	})
	rt.Set("relativePoint", func(x, y float64) string {
		return fmt.Sprintf("%d%%,%d%%", ceil(x*100), ceil(y*100))
	})
	console := rt.NewObject()
	console.Set("log", func(call goja.FunctionCall) goja.Value {
		var parts []string
		for _, a := range call.Arguments {
			parts = append(parts, Display(a))
		}
		if out := e.log; out != nil {
			out(strings.Join(parts, " "))
		} else if e.Log != nil {
			e.Log(strings.Join(parts, " "))
		}
		return goja.Undefined()
	})
	rt.Set("console", console)
	rt.Set("http", e.httpObject())
	rt.Set("faker", newFaker(rt))
	e.installTimers()
	for _, k := range []string{"output", "tb", legacy.ScriptObject(), "json", "relativePoint", "console", "http", "faker", "__tb_src"} {
		e.permanent[k] = true
	}
	if _, err := rt.RunString(prelude); err != nil {
		panic(err)
	}
	return e
}

func ceil(f float64) int {
	i := int(f)
	if float64(i) < f {
		i++
	}
	return i
}

type Scope struct {
	Env        map[string]string
	Platform   string
	CopiedText *string
	Log        func(string)
}

func (e *Engine) Output() map[string]any {
	e.mu.Lock()
	defer e.mu.Unlock()
	if m, ok := e.output.Export().(map[string]any); ok {
		return m
	}
	return nil
}

func (e *Engine) prepare(sc *Scope) {
	var env map[string]string
	if sc != nil {
		env = sc.Env
		if sc.Platform != "" {
			e.tb.Set("platform", sc.Platform)
		}
		if sc.CopiedText != nil {
			e.tb.Set("copiedText", *sc.CopiedText)
		} else {
			e.tb.Delete("copiedText")
		}
	}
	g := e.rt.GlobalObject()
	for _, k := range g.Keys() {
		if !e.permanent[k] {
			g.Delete(k)
		}
	}
	for k, v := range env {
		if e.permanent[k] || !validIdent(k) {
			continue
		}
		g.Set(k, v)
	}
}

func validIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if r == '_' || r == '$' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || (i > 0 && r >= '0' && r <= '9') || r > 127 {
			continue
		}
		return false
	}
	return true
}

func (e *Engine) Eval(src string, sc *Scope) (goja.Value, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.evalLocked(src, sc)
}

func (e *Engine) evalLocked(src string, sc *Scope) (v goja.Value, err error) {
	e.prepare(sc)
	e.log = nil
	if sc != nil {
		e.log = sc.Log
	}
	defer func() { e.log = nil }()
	e.rt.Set("__tb_src", src)
	e.timers = nil
	defer func() { e.timers = nil }()
	defer func() {
		if p := recover(); p != nil {
			v, err = nil, &Error{Msg: fmt.Sprint(p)}
		}
	}()
	v, err = e.rt.RunString("'use strict'; (function(){ return eval(__tb_src) })()")
	if err != nil {
		msg := err.Error()
		if ex, ok := err.(*goja.Exception); ok {
			msg = ex.Value().String()
		}
		_, cerr := goja.Compile("", src, true)
		return nil, &Error{Msg: msg, Syntax: cerr != nil}
	}
	if err := e.drainTimers(); err != nil {
		return nil, err
	}
	return v, nil
}

func (e *Engine) RunScript(src, dir string, sc *Scope) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	prev := e.ScriptDir
	e.ScriptDir = dir
	defer func() { e.ScriptDir = prev }()
	_, err := e.evalLocked(src, sc)
	return err
}

func (e *Engine) Interpolate(s string, sc *Scope) (string, error) {
	if !strings.Contains(s, "${") {
		return s, nil
	}
	var b strings.Builder
	i := 0
	for i < len(s) {
		j := strings.Index(s[i:], "${")
		if j < 0 {
			b.WriteString(s[i:])
			break
		}
		j += i
		start := j + 2
		next := strings.IndexByte(s[start:], '$')
		limit := len(s)
		if next >= 0 {
			limit = start + next
		}
		close := strings.LastIndexByte(s[start:limit], '}')
		if close < 0 {
			b.WriteString(s[i : j+2])
			i = j + 2
			continue
		}
		close += start
		expr := s[start:close]
		escaped := j > 0 && s[j-1] == '\\'
		if escaped {
			b.WriteString(s[i : j-1])
			b.WriteString(s[j : close+1])
			i = close + 1
			continue
		}
		b.WriteString(s[i:j])
		if strings.TrimSpace(expr) != "" {
			v, err := e.Eval(expr, sc)
			if err != nil {
				return "", err
			}
			b.WriteString(Display(v))
		}
		i = close + 1
	}
	return b.String(), nil
}

func Truthy(s string) bool {
	t := strings.TrimSpace(s)
	if t == "" || strings.EqualFold(t, "false") || t == "undefined" || t == "null" {
		return false
	}
	if f, err := strconv.ParseFloat(t, 64); err == nil && f == 0 {
		return false
	}
	return true
}

func Display(v goja.Value) string {
	if v == nil || goja.IsUndefined(v) {
		return "undefined"
	}
	if goja.IsNull(v) {
		return "null"
	}
	if _, ok := goja.AssertFunction(v); ok {
		return v.String()
	}
	switch x := v.Export().(type) {
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return v.String()
	}
	return display(v.Export(), 0)
}

func display(x any, depth int) string {
	if depth > 4 {
		return "..."
	}
	switch t := x.(type) {
	case nil:
		return "null"
	case string:
		if depth == 0 {
			return t
		}
		return strconv.Quote(t)
	case []any:
		parts := make([]string, len(t))
		for i, el := range t {
			parts[i] = display(el, depth+1)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = k + ": " + display(t[k], depth+1)
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	}
	return fmt.Sprint(x)
}

func (e *Engine) httpObject() *goja.Object {
	rt := e.rt
	h := rt.NewObject()
	client := &http.Client{Timeout: 5 * time.Minute}
	do := func(method, url string, params goja.Value) goja.Value {
		var opts map[string]any
		if params != nil && !goja.IsUndefined(params) && !goja.IsNull(params) {
			opts, _ = params.Export().(map[string]any)
		}
		if m, ok := opts["method"].(string); ok && m != "" {
			method = strings.ToUpper(m)
		}
		var body io.Reader
		contentType := ""
		if b, ok := opts["body"]; ok && b != nil {
			body = strings.NewReader(fmt.Sprint(b))
		}
		if form, ok := opts["multipartForm"].(map[string]any); ok {
			var buf bytes.Buffer
			w := multipart.NewWriter(&buf)
			for k, v := range form {
				switch f := v.(type) {
				case map[string]any:
					path := fmt.Sprint(f["filePath"])
					if !filepath.IsAbs(path) && e.ScriptDir != "" {
						path = filepath.Join(e.ScriptDir, path)
					}
					data, err := os.ReadFile(path)
					if err != nil {
						panic(rt.NewGoError(err))
					}
					fw, _ := w.CreateFormFile(k, filepath.Base(path))
					fw.Write(data)
				default:
					w.WriteField(k, fmt.Sprint(f))
				}
			}
			w.Close()
			body = &buf
			contentType = w.FormDataContentType()
		}
		req, err := http.NewRequest(method, url, body)
		if err != nil {
			panic(rt.NewGoError(err))
		}
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		if hdrs, ok := opts["headers"].(map[string]any); ok {
			for k, v := range hdrs {
				req.Header.Set(k, fmt.Sprint(v))
			}
		}
		resp, err := client.Do(req)
		if err != nil {
			panic(rt.NewGoError(err))
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		out := rt.NewObject()
		out.Set("ok", resp.StatusCode >= 200 && resp.StatusCode < 300)
		out.Set("status", resp.StatusCode)
		out.Set("body", string(data))
		hs := rt.NewObject()
		for k, v := range resp.Header {
			hs.Set(strings.ToLower(k), strings.Join(v, ","))
		}
		out.Set("headers", hs)
		return out
	}
	for _, m := range []string{"get", "post", "put", "delete"} {
		method := strings.ToUpper(m)
		h.Set(m, func(url string, params goja.Value) goja.Value { return do(method, url, params) })
	}
	h.Set("request", func(url string, params goja.Value) goja.Value { return do("GET", url, params) })
	return h
}
