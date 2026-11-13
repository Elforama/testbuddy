package flow

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"iter"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Error is a flow syntax error with its location.
type Error struct {
	File string
	Line int
	Msg  string
}

func (e *Error) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("%s:%d: %s", e.File, e.Line, e.Msg)
	}
	return fmt.Sprintf("%s: %s", e.File, e.Msg)
}

type parser struct {
	file  string
	dir   string
	stack []string // files being parsed, for cycle detection
	multi bool     // the top-level file declares devices
	roles map[string]bool
}

func (p *parser) errf(n *yaml.Node, format string, args ...any) error {
	line := 0
	if n != nil {
		line = n.Line
	}
	return &Error{File: p.file, Line: line, Msg: fmt.Sprintf(format, args...)}
}

// ParseFile parses a flow file and every sub-flow it references.
func ParseFile(path string) (*Flow, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	return parseFile(abs, nil)
}

func parseFile(abs string, stack []string) (*Flow, error) {
	for _, s := range stack {
		if s == abs {
			return nil, &Error{File: abs, Msg: "runFlow cycle: " + strings.Join(append(stack, abs), " -> ")}
		}
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}
	p := &parser{file: abs, dir: filepath.Dir(abs), stack: append(stack, abs)}
	return p.parse(data)
}

// ParseBytes parses flow text as if it were the file at path (used for relative paths).
func ParseBytes(path string, data []byte) (*Flow, error) {
	abs, _ := filepath.Abs(path)
	p := &parser{file: abs, dir: filepath.Dir(abs), stack: []string{abs}}
	return p.parse(data)
}

// ParseCommands parses a YAML list of commands (or a single command) without a config
// header, as agents send them to `tb do`. dir resolves relative paths.
func ParseCommands(dir string, data []byte) ([]*Command, error) {
	p := &parser{file: filepath.Join(dir, "<commands>"), dir: dir}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, &Error{File: "<commands>", Msg: cleanYAMLErr(err)}
	}
	if len(doc.Content) == 0 {
		return nil, nil
	}
	n := doc.Content[0]
	if n.Kind != yaml.SequenceNode {
		// a single command, written without the leading dash
		n = &yaml.Node{Kind: yaml.SequenceNode, Content: []*yaml.Node{n}, Line: n.Line}
	}
	return p.commands(n)
}

func cleanYAMLErr(err error) string {
	return strings.TrimPrefix(err.Error(), "yaml: ")
}

func (p *parser) parse(data []byte) (*Flow, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var docs []*yaml.Node
	for len(docs) < 2 {
		var d yaml.Node
		err := dec.Decode(&d)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, &Error{File: p.file, Msg: cleanYAMLErr(err)}
		}
		if len(d.Content) == 0 {
			continue
		}
		docs = append(docs, d.Content[0])
	}
	if len(docs) == 0 || docs[0].Kind != yaml.MappingNode {
		var at *yaml.Node
		if len(docs) > 0 {
			at = docs[0]
		}
		return nil, p.errf(at, "Config Section Required: a flow starts with a config mapping (appId: ...), then '---', then the list of commands")
	}
	f := &Flow{Path: p.file, Dir: p.dir}
	for _, d := range docs {
		f.DuplicateKeys = duplicateKeys(d, f.DuplicateKeys)
	}
	if err := p.config(docs[0], &f.Config); err != nil {
		return nil, err
	}
	if len(docs) < 2 || docs[1].Kind != yaml.SequenceNode {
		var at *yaml.Node
		if len(docs) > 1 {
			at = docs[1]
		}
		return nil, p.errf(at, "Commands Section Required: after the config and '---', a flow needs a list of commands")
	}
	cmds, err := p.commands(docs[1])
	if err != nil {
		return nil, err
	}
	f.Commands = cmds
	return f, nil
}

// ---- config ----

func (p *parser) config(n *yaml.Node, c *Config) error {
	c.Ext = map[string]any{}
	c.ExtLines = map[string]int{}
	c.Properties = map[string]string{}
	var appID string
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		switch k.Value {
		case "appId":
			appID = scalarOr(v, "")
		case "url":
			c.URL = scalarOr(v, "")
		case "name":
			c.Name = scalarOr(v, "")
		case "tags":
			if v.Kind != yaml.SequenceNode {
				if isNull(v) {
					continue
				}
				return p.errf(v, "tags must be a list")
			}
			for _, t := range v.Content {
				c.Tags = append(c.Tags, t.Value)
			}
		case "env":
			env, err := p.env(v)
			if err != nil {
				return err
			}
			c.Env = env
		case "onFlowStart", "onFlowComplete":
			if isNull(v) {
				continue
			}
			if v.Kind != yaml.SequenceNode {
				return p.errf(v, "%s must be a list of commands", k.Value)
			}
			cmds, err := p.commands(v)
			if err != nil {
				return err
			}
			if k.Value == "onFlowStart" {
				c.OnFlowStart = cmds
			} else {
				c.OnFlowComplete = cmds
			}
		case "properties":
			if v.Kind == yaml.MappingNode {
				for j := 0; j+1 < len(v.Content); j += 2 {
					c.Properties[v.Content[j].Value] = v.Content[j+1].Value
					c.PropertyOrder = append(c.PropertyOrder, v.Content[j].Value)
				}
			}
		case "jsEngine":
			if strings.EqualFold(v.Value, "rhino") {
				return p.errf(v, "the Rhino JS engine has been removed; delete jsEngine: rhino")
			}
			c.Ext[k.Value] = v.Value
			c.ExtLines[k.Value] = k.Line
		case "devices":
			if err := p.devices(v, c); err != nil {
				return err
			}
		default:
			var x any
			v.Decode(&x)
			c.Ext[k.Value] = x
			c.ExtLines[k.Value] = k.Line
		}
	}
	c.AppID = appID
	if c.URL != "" {
		c.AppID = c.URL
	}
	if c.AppID == "" && len(c.Devices) == 0 {
		return p.errf(n, "Either 'url' or 'appId' must be specified in the flow config")
	}
	return nil
}

func (p *parser) devices(v *yaml.Node, c *Config) error {
	if v.Kind != yaml.MappingNode {
		return p.errf(v, "devices must map role names to device settings, e.g. devices: {alice: {platform: ios}, bob: {platform: android}}")
	}
	p.multi = true
	p.roles = map[string]bool{}
	for i := 0; i+1 < len(v.Content); i += 2 {
		name, spec := v.Content[i].Value, v.Content[i+1]
		if _, isCmd := commandKeys[name]; isCmd || name == "parallel" || name == "signal" || name == "waitForSignal" {
			return p.errf(v.Content[i], "device role %q has the name of a command; pick another name", name)
		}
		r := &DeviceRole{Name: name}
		switch spec.Kind {
		case yaml.ScalarNode:
			if !isNull(spec) {
				r.Platform = strings.ToLower(spec.Value)
			}
		case yaml.MappingNode:
			for j := 0; j+1 < len(spec.Content); j += 2 {
				k, val := spec.Content[j], spec.Content[j+1]
				switch k.Value {
				case "platform":
					r.Platform = strings.ToLower(val.Value)
				case "device":
					r.Device = val.Value
				case "appId":
					r.AppID = val.Value
				case "env":
					env, err := p.env(val)
					if err != nil {
						return err
					}
					r.Env = env
				default:
					return p.errf(k, "unknown device setting %q (use platform, device, appId, env)", k.Value)
				}
			}
		default:
			return p.errf(spec, "device %q: expected a platform name or a map", name)
		}
		if r.Platform != "" && r.Platform != "android" && r.Platform != "ios" && r.Platform != "web" {
			return p.errf(spec, "device %q: platform must be android, ios or web", name)
		}
		c.Devices = append(c.Devices, r)
		p.roles[name] = true
	}
	return nil
}

func (p *parser) env(v *yaml.Node) ([]KV, error) {
	if isNull(v) {
		return nil, nil
	}
	if v.Kind != yaml.MappingNode {
		return nil, p.errf(v, "env must be a map of names to values")
	}
	var out []KV
	for i := 0; i+1 < len(v.Content); i += 2 {
		k, val := v.Content[i], v.Content[i+1]
		if isNull(val) {
			return nil, p.errf(k, "Environment variable %s has no value", k.Value)
		}
		if val.Kind != yaml.ScalarNode {
			return nil, p.errf(val, "env value for %s must be a string", k.Value)
		}
		out = append(out, KV{k.Value, val.Value})
	}
	return out, nil
}

// ---- commands ----

// Bare commands accepted as a plain string list item.
var bareCommands = map[string]bool{
	"launchApp": true, "stopApp": true, "killApp": true, "clearState": true, "clearKeychain": true,
	"eraseText": true, "inputRandomText": true, "inputRandomNumber": true, "inputRandomEmail": true,
	"inputRandomPersonName": true, "inputRandomCityName": true, "inputRandomCountryName": true,
	"inputRandomColorName": true, "back": true, "hideKeyboard": true, "hide keyboard": true, "pasteText": true,
	"scroll": true, "waitForAnimationToEnd": true, "stopRecording": true, "toggleAirplaneMode": true,
	"toggleDarkMode": true, "assertDarkMode": true, "assertLightMode": true, "assertNoDefectsWithAI": true,
	"clearNetworkMocks": true,
}

// commandKeys lists every command key; the value is unused.
var commandKeys = map[string]bool{
	"tapOn": true, "doubleTapOn": true, "longPressOn": true, "assertVisible": true, "assertNotVisible": true,
	"assertTrue": true, "extendedWaitUntil": true, "assertScreenshot": true, "assertWithAI": true,
	"assertNoDefectsWithAI": true, "extractTextWithAI": true, "inputText": true, "inputRandomText": true,
	"inputRandomNumber": true, "inputRandomEmail": true, "inputRandomPersonName": true,
	"inputRandomCityName": true, "inputRandomCountryName": true, "inputRandomColorName": true,
	"eraseText": true, "pasteText": true, "copyTextFrom": true, "setClipboard": true, "swipe": true,
	"scroll": true, "scrollUntilVisible": true, "back": true, "hideKeyboard": true, "pressKey": true,
	"launchApp": true, "stopApp": true, "killApp": true, "clearState": true, "clearKeychain": true,
	"setPermissions": true, "openLink": true, "runFlow": true, "runScript": true, "evalScript": true,
	"repeat": true, "retry": true, "takeScreenshot": true, "startRecording": true, "stopRecording": true,
	"waitForAnimationToEnd": true, "setLocation": true, "travel": true, "setOrientation": true,
	"addMedia": true, "setAirplaneMode": true, "toggleAirplaneMode": true, "setDarkMode": true,
	"toggleDarkMode": true, "assertDarkMode": true, "assertLightMode": true, "action": true,
	// Test Buddy extensions
	"signal": true, "waitForSignal": true, "parallel": true, "scrollUntilVisibleIn": true,
	"mockNetwork": true, "clearNetworkMocks": true, "answerDialogs": true,
}

func (p *parser) commands(seq *yaml.Node) ([]*Command, error) {
	var out []*Command
	for _, item := range seq.Content {
		c, err := p.item(item)
		if err != nil {
			return nil, err
		}
		if c != nil {
			out = append(out, c)
		}
	}
	return out, nil
}

func (p *parser) item(item *yaml.Node) (*Command, error) {
	switch item.Kind {
	case yaml.ScalarNode:
		name := item.Value
		if bareCommands[name] {
			if name == "hide keyboard" {
				name = "hideKeyboard"
			}
			return p.command(name, nil, item)
		}
		if commandKeys[name] {
			return nil, p.errf(item, "Missing Command Options: %s needs a value, e.g. - %s: ...", name, name)
		}
		return nil, p.errf(item, "Invalid Command: %q%s", name, suggest(name))
	case yaml.MappingNode:
		if len(item.Content) == 0 {
			return nil, p.errf(item, "Invalid command format: empty map")
		}
		if len(item.Content) > 2 {
			return nil, p.errf(item.Content[2], "Found unexpected top-level field %q next to %q. Missing an indent or dash? Options go inside the command's map", item.Content[2].Value, item.Content[0].Value)
		}
		k, v := item.Content[0], item.Content[1]
		name := k.Value
		if p.roles[name] {
			return p.roleBlock(name, v, k)
		}
		if !commandKeys[name] {
			return nil, p.errf(k, "Invalid Command: %q%s", name, suggest(name))
		}
		if isNull(v) {
			return nil, p.errf(k, "Incorrect Command Format: %s requires additional options", name)
		}
		return p.command(name, v, k)
	default:
		return nil, p.errf(item, "Invalid command format: each command is a string or a single-key map")
	}
}

// roleBlock parses `- alice: [commands]` (or a single command map) in a multi-device flow.
func (p *parser) roleBlock(role string, v *yaml.Node, at *yaml.Node) (*Command, error) {
	c := &Command{Name: "on", On: role, File: p.file, Line: at.Line}
	var cmds []*Command
	var err error
	switch v.Kind {
	case yaml.SequenceNode:
		cmds, err = p.commands(v)
	case yaml.MappingNode, yaml.ScalarNode:
		var one *Command
		one, err = p.item(v)
		if one != nil {
			cmds = []*Command{one}
		}
	default:
		err = p.errf(v, "expected commands for device %q", role)
	}
	if err != nil {
		return nil, err
	}
	c.Commands = cmds
	return c, nil
}

func suggest(name string) string {
	if best := nearest(name, maps.Keys(commandKeys), 3); best != "" {
		return ". Did you mean " + best + "?"
	}
	return ""
}

// NearestHeaderKey is the header key (HeaderKeys) name is a near miss of, ignoring case:
// "tags" for "tgas", "tag" or "Tags". "" when none is close.
func NearestHeaderKey(name string) string {
	return nearest(name, slices.Values(HeaderKeys), 2)
}

// nearest is the key closest to name, ignoring case, at most maxD edits away; "" if none.
func nearest(name string, keys iter.Seq[string], maxD int) string {
	best, bestD := "", maxD+1
	for k := range keys {
		if d := levenshtein(strings.ToLower(name), strings.ToLower(k)); d < bestD {
			best, bestD = k, d
		}
	}
	return best
}

// duplicateKeys appends the keys written twice in one map, in n and the nodes under it:
// YAML keeps the last value and says nothing.
func duplicateKeys(n *yaml.Node, out []DuplicateKey) []DuplicateKey {
	if n == nil || n.Kind == yaml.AliasNode {
		return out
	}
	if n.Kind == yaml.MappingNode {
		seen := map[string]int{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i]
			if k.Kind != yaml.ScalarNode || k.Value == "<<" { // merge keys may repeat
				continue
			}
			if first, ok := seen[k.Value]; ok {
				out = append(out, DuplicateKey{Key: k.Value, Line: k.Line, First: first})
			} else {
				seen[k.Value] = k.Line
			}
		}
	}
	for _, c := range n.Content {
		out = duplicateKeys(c, out)
	}
	return out
}

func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(rb)]
}

// ---- node helpers ----

func isNull(n *yaml.Node) bool {
	return n == nil || (n.Kind == yaml.ScalarNode && n.Tag == "!!null")
}

func scalarOr(n *yaml.Node, def string) string {
	if n == nil || n.Kind != yaml.ScalarNode || isNull(n) {
		return def
	}
	return n.Value
}

type mapView struct {
	p    *parser
	node *yaml.Node
	keys map[string]*yaml.Node
	knod map[string]*yaml.Node
	used map[string]bool
}

func (p *parser) mapOf(n *yaml.Node) *mapView {
	m := &mapView{p: p, node: n, keys: map[string]*yaml.Node{}, knod: map[string]*yaml.Node{}, used: map[string]bool{}}
	for i := 0; i+1 < len(n.Content); i += 2 {
		m.keys[n.Content[i].Value] = n.Content[i+1]
		m.knod[n.Content[i].Value] = n.Content[i]
	}
	return m
}

func (m *mapView) has(k string) bool { _, ok := m.keys[k]; return ok }

func (m *mapView) get(k string) *yaml.Node {
	m.used[k] = true
	return m.keys[k]
}

func (m *mapView) str(k string) string { return scalarOr(m.get(k), "") }

func (m *mapView) boolean(k string) (bool, error) {
	v := m.get(k)
	if v == nil || isNull(v) {
		return false, nil
	}
	return m.p.parseBool(v)
}

func (m *mapView) boolPtr(k string) (*bool, error) {
	v := m.get(k)
	if v == nil || isNull(v) {
		return nil, nil
	}
	b, err := m.p.parseBool(v)
	return &b, err
}

func (m *mapView) integer(k string, def int) (int, error) {
	v := m.get(k)
	if v == nil || isNull(v) {
		return def, nil
	}
	return m.p.parseInt(v)
}

func (m *mapView) intPtr(k string) (*int, error) {
	v := m.get(k)
	if v == nil || isNull(v) {
		return nil, nil
	}
	i, err := m.p.parseInt(v)
	return &i, err
}

// strict rejects keys that were not read.
func (m *mapView) strict(cmd string, ignore ...string) error {
	ig := map[string]bool{}
	for _, k := range ignore {
		ig[k] = true
	}
	var unknown []string
	for k := range m.keys {
		if !m.used[k] && !ig[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	return m.p.errf(m.knod[unknown[0]], "Unknown Property: %q is not an option of %s", unknown[0], cmd)
}

func (p *parser) parseBool(n *yaml.Node) (bool, error) {
	switch strings.ToLower(n.Value) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	return false, p.errf(n, "expected true or false, got %q", n.Value)
}

func (p *parser) parseInt(n *yaml.Node) (int, error) {
	s := strings.ReplaceAll(strings.TrimSpace(n.Value), "_", "")
	if i, err := strconv.Atoi(s); err == nil {
		return i, nil
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return int(f), nil
	}
	return 0, p.errf(n, "expected a number, got %q", n.Value)
}

func (p *parser) resolve(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(p.dir, path)
}

func baseNoExt(path string) string {
	b := filepath.Base(path)
	return strings.TrimSuffix(b, filepath.Ext(b))
}
