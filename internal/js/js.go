// Package js runs the flow format's JavaScript: ${...} expressions, runScript files and
// evalScript, with these semantics:
//   - each evaluation runs as (function(){ return eval(src) })() in strict mode;
//   - before each evaluation, globals other than the permanent ones are deleted and the
//     current env variables are injected as string globals;
//   - undeclared names read as undefined (so ${X || 'default'} works);
//   - output, tb (also under its legacy name, see internal/legacy), json, http, faker,
//     relativePoint and console persist.
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

// Engine is one flow run's JavaScript context. It is safe for concurrent use: multi-device
// flows share one engine (and so one `output`) across devices.
type Engine struct {
	mu        sync.Mutex
	rt        *goja.Runtime
	output    *goja.Object
	tb        *goja.Object // the scripts' `tb` object (platform, copiedText)
	permanent map[string]bool
	// Log receives console.log lines.
	Log func(string)
	// ScriptDir is where http multipartForm filePath resolves; set by runScript.
	ScriptDir string
}

// Error is a JavaScript error. It is fatal even for optional commands.
type Error struct{ Msg string }

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
	// Builtins are non-enumerable; record them too.
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
	rt.Set(legacy.ScriptObject(), e.tb) // the same object under its legacy name
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
		if e.Log != nil {
			e.Log(strings.Join(parts, " "))
		}
		return goja.Undefined()
	})
	rt.Set("console", console)
	rt.Set("http", e.httpObject())
	rt.Set("faker", newFaker(rt))
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

// Scope is what one evaluation sees: the device's env variables, its platform and its
// clipboard. Multi-device flows share one Engine (one `output`) with a Scope per device.
type Scope struct {
	Env        map[string]string
	Platform   string
	CopiedText *string
}

// Output returns the `output` object exported to Go values (for reports and agents).
func (e *Engine) Output() map[string]any {
	e.mu.Lock()
	defer e.mu.Unlock()
	if m, ok := e.output.Export().(map[string]any); ok {
		return m
	}
	return nil
}

// prepare deletes leftover globals and injects env as string globals. Caller holds mu.
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

// Eval evaluates one expression or script and returns its value.
func (e *Engine) Eval(src string, sc *Scope) (goja.Value, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.evalLocked(src, sc)
}

func (e *Engine) evalLocked(src string, sc *Scope) (v goja.Value, err error) {
	e.prepare(sc)
	e.rt.Set("__tb_src", src)
	defer func() {
		if p := recover(); p != nil {
			err = &Error{Msg: fmt.Sprint(p)}
		}
	}()
	v, err = e.rt.RunString("'use strict'; (function(){ return eval(__tb_src) })()")
	if err != nil {
		if ex, ok := err.(*goja.Exception); ok {
			return nil, &Error{Msg: ex.Value().String()}
		}
		return nil, &Error{Msg: err.Error()}
	}
	return v, nil
}

// RunScript runs a runScript file's source (its result is ignored).
func (e *Engine) RunScript(src, dir string, sc *Scope) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	prev := e.ScriptDir
	e.ScriptDir = dir
	defer func() { e.ScriptDir = prev }()
	_, err := e.evalLocked(src, sc)
	return err
}

// Interpolate replaces every ${...} in s with its evaluated value, using the format's rule:
// the expression runs to the last "}" before the next "$" (it cannot contain "$"), and
// \${...} is kept literally (without the backslash).
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
		// the expression: [^$]* greedy up to the last '}' before the next '$'
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

// Truthy is the format's rule for a condition's `true:` value after interpolation.
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

// Display renders a value the way the reference JavaScript engine's toString does: strings raw, objects in
// display form ({a: 1}, [1, 2]), not JSON.
func Display(v goja.Value) string {
	if v == nil || goja.IsUndefined(v) {
		return "undefined"
	}
	if goja.IsNull(v) {
		return "null"
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

// ---- http ----

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
