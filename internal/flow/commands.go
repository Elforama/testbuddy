package flow

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

var selectorKeys = []string{"text", "id", "index", "width", "height", "tolerance", "enabled", "selected",
	"checked", "focused", "below", "above", "leftOf", "rightOf", "containsChild", "containsDescendants",
	"childOf", "traits", "css"}

// Command options that live inside a selector map. Accepted (and ignored) in nested selectors.
var tapOptionKeys = []string{"optional", "label", "point", "retryTapIfNoChange", "waitUntilVisible",
	"repeat", "delay", "waitToSettleTimeoutMs"}

// selector parses a selector: a scalar is a text regex; a map has selector keys.
// Command options found in the map are left unread for the caller.
func (p *parser) selector(n *yaml.Node) (*Selector, *mapView, error) {
	if n.Kind == yaml.ScalarNode {
		return &Selector{Text: n.Value}, nil, nil
	}
	if n.Kind != yaml.MappingNode {
		return nil, nil, p.errf(n, "a selector is a text or a map (text, id, index, ...)")
	}
	m := p.mapOf(n)
	s := &Selector{}
	var err error
	s.Text = m.str("text")
	s.ID = m.str("id")
	s.Index = m.str("index")
	s.CSS = m.str("css")
	if s.Width, err = m.intPtr("width"); err != nil {
		return nil, nil, err
	}
	if s.Height, err = m.intPtr("height"); err != nil {
		return nil, nil, err
	}
	if s.Tolerance, err = m.intPtr("tolerance"); err != nil {
		return nil, nil, err
	}
	for _, f := range []struct {
		k string
		p **bool
	}{{"enabled", &s.Enabled}, {"selected", &s.Selected}, {"checked", &s.Checked}, {"focused", &s.Focused}} {
		if *f.p, err = m.boolPtr(f.k); err != nil {
			return nil, nil, err
		}
	}
	if t := m.get("traits"); t != nil && !isNull(t) {
		for _, w := range strings.Fields(t.Value) {
			w = strings.ToLower(strings.ReplaceAll(w, "_", "-"))
			if w != "text" && w != "square" && w != "long-text" {
				return nil, nil, p.errf(t, "unknown trait %q (text, square, long-text)", w)
			}
			s.Traits = append(s.Traits, w)
		}
	}
	for _, rel := range []struct {
		k string
		p **Selector
	}{{"below", &s.Below}, {"above", &s.Above}, {"leftOf", &s.LeftOf}, {"rightOf", &s.RightOf},
		{"containsChild", &s.ContainsChild}, {"childOf", &s.ChildOf}} {
		if v := m.get(rel.k); v != nil && !isNull(v) {
			sub, sm, err := p.selector(v)
			if err != nil {
				return nil, nil, err
			}
			if sm != nil {
				for _, k := range tapOptionKeys {
					sm.get(k)
				}
				if err := sm.strict(rel.k); err != nil {
					return nil, nil, err
				}
			}
			*rel.p = sub
		}
	}
	if v := m.get("containsDescendants"); v != nil && !isNull(v) {
		if v.Kind != yaml.SequenceNode {
			return nil, nil, p.errf(v, "containsDescendants is a list of selectors")
		}
		for _, item := range v.Content {
			sub, sm, err := p.selector(item)
			if err != nil {
				return nil, nil, err
			}
			if sm != nil {
				for _, k := range tapOptionKeys {
					sm.get(k)
				}
				if err := sm.strict("containsDescendants"); err != nil {
					return nil, nil, err
				}
			}
			s.ContainsDescendants = append(s.ContainsDescendants, sub)
		}
	}
	if o := m.keys["optional"]; o != nil && !isNull(o) {
		s.Optional, _ = p.parseBool(o)
	}
	return s, m, nil
}

// nestedSelector parses a selector used as a value (element:, from:, cropOn:, visible:).
func (p *parser) nestedSelector(n *yaml.Node, where string) (*Selector, error) {
	s, m, err := p.selector(n)
	if err != nil {
		return nil, err
	}
	if m != nil {
		for _, k := range tapOptionKeys {
			m.get(k)
		}
		if err := m.strict(where); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (p *parser) condition(n *yaml.Node) (*Condition, error) {
	if n.Kind != yaml.MappingNode {
		return nil, p.errf(n, "a condition is a map (visible, notVisible, true, platform)")
	}
	m := p.mapOf(n)
	c := &Condition{Label: m.str("label")}
	m.get("optional")
	c.Timeout = m.str("timeout") // Test Buddy extension: the condition's own wait
	if v := m.get("platform"); v != nil && !isNull(v) {
		pl := strings.ToLower(v.Value)
		if pl != "android" && pl != "ios" && pl != "web" {
			return nil, p.errf(v, "platform must be Android, iOS or Web")
		}
		c.Platform = pl
	}
	var err error
	if v := m.get("visible"); v != nil && !isNull(v) {
		if c.Visible, err = p.nestedSelector(v, "visible"); err != nil {
			return nil, err
		}
	}
	if v := m.get("notVisible"); v != nil && !isNull(v) {
		if c.NotVisible, err = p.nestedSelector(v, "notVisible"); err != nil {
			return nil, err
		}
	}
	if v := m.get("true"); v != nil {
		s := scalarOr(v, "")
		c.True = &s
	}
	return c, m.strict("condition")
}

func (p *parser) newCmd(name string, at *yaml.Node) *Command {
	c := &Command{Name: name, File: p.file}
	if at != nil {
		c.Line = at.Line
	}
	return c
}

// command parses one command. v is nil for bare commands.
func (p *parser) command(name string, v *yaml.Node, at *yaml.Node) (*Command, error) {
	c := p.newCmd(name, at)
	var m *mapView
	isMap := v != nil && v.Kind == yaml.MappingNode
	if isMap {
		m = p.mapOf(v)
	}
	scalar := v != nil && v.Kind == yaml.ScalarNode
	// common label/optional for map forms
	common := func() error {
		if m == nil {
			return nil
		}
		c.Label = m.str("label")
		var err error
		c.Optional, err = m.boolean("optional")
		return err
	}
	var err error

	switch name {
	case "tapOn", "doubleTapOn", "longPressOn":
		if v.Kind == yaml.ScalarNode {
			c.Selector = &Selector{Text: v.Value}
		} else {
			s, sm, err := p.selector(v)
			if err != nil {
				return nil, err
			}
			c.Selector = s
			c.Label = sm.str("label")
			if c.Optional, err = sm.boolean("optional"); err != nil {
				return nil, err
			}
			c.Point = sm.str("point")
			if c.RetryTapIfNoChange, err = sm.boolean("retryTapIfNoChange"); err != nil {
				return nil, err
			}
			if c.WaitUntilVisible, err = sm.boolean("waitUntilVisible"); err != nil {
				return nil, err
			}
			if c.Repeat, err = sm.integer("repeat", 0); err != nil {
				return nil, err
			}
			if c.Delay, err = sm.integer("delay", 100); err != nil {
				return nil, err
			}
			if c.WaitToSettleTimeoutMs, err = sm.integer("waitToSettleTimeoutMs", 0); err != nil {
				return nil, err
			}
			if err := sm.strict(name); err != nil {
				return nil, err
			}
			if !s.HasCriteria() && c.Point == "" {
				// {optional: true} alone: matches every node (the first clickable is picked)
			}
			if !s.HasCriteria() && c.Point != "" {
				c.Selector = nil // a screen point tap
			}
		}
		if c.Delay < 0 {
			c.Delay = 100
		}
		if c.WaitToSettleTimeoutMs > 30000 {
			c.WaitToSettleTimeoutMs = 30000
		}
		switch name {
		case "doubleTapOn":
			c.Repeat = 2
		case "longPressOn":
			c.LongPress = true
			c.Repeat = 0
		}
		if c.Selector != nil && c.Selector.Optional {
			c.Optional = true
		}

	case "assertVisible", "assertNotVisible", "copyTextFrom":
		if scalar {
			c.Selector = &Selector{Text: v.Value}
		} else {
			s, sm, err := p.selector(v)
			if err != nil {
				return nil, err
			}
			c.Selector = s
			c.Label = sm.str("label")
			if c.Optional, err = sm.boolean("optional"); err != nil {
				return nil, err
			}
			if err := sm.strict(name, tapOptionKeys...); err != nil {
				return nil, err
			}
			if s.Optional {
				c.Optional = true
			}
		}

	case "assertTrue":
		if scalar {
			t := v.Value
			c.Cond = &Condition{True: &t}
		} else if isMap {
			t := m.str("condition")
			c.Cond = &Condition{True: &t}
			if err := common(); err != nil {
				return nil, err
			}
		} else {
			return nil, p.errf(v, "assertTrue takes a condition")
		}

	case "extendedWaitUntil":
		if !isMap {
			return nil, p.errf(v, "extendedWaitUntil takes a map with visible or notVisible, and timeout")
		}
		if err := common(); err != nil {
			return nil, err
		}
		cond := &Condition{}
		if x := m.get("visible"); x != nil && !isNull(x) {
			if cond.Visible, err = p.nestedSelector(x, "visible"); err != nil {
				return nil, err
			}
		}
		if x := m.get("notVisible"); x != nil && !isNull(x) {
			if cond.NotVisible, err = p.nestedSelector(x, "notVisible"); err != nil {
				return nil, err
			}
		}
		if cond.Visible == nil && cond.NotVisible == nil {
			return nil, p.errf(v, "extendedWaitUntil expects either `visible` or `notVisible`")
		}
		c.Cond = cond
		c.Timeout = m.str("timeout")
		if err := m.strict(name); err != nil {
			return nil, err
		}

	case "inputText":
		if scalar {
			c.Text = v.Value
		} else if isMap {
			c.Text = m.str("text")
			c.Label = m.str("label") // optional and unknown keys are ignored
		} else {
			return nil, p.errf(v, "inputText takes a text")
		}
		c.LabelRaw = true

	case "inputRandomText", "inputRandomNumber", "inputRandomEmail", "inputRandomPersonName",
		"inputRandomCityName", "inputRandomCountryName", "inputRandomColorName":
		c.Length = 8
		if v != nil {
			if !isMap {
				return nil, p.errf(v, "%s takes no value, or a map (length, label, optional)", name)
			}
			if err := common(); err != nil {
				return nil, err
			}
			if name == "inputRandomText" || name == "inputRandomNumber" {
				if c.Length, err = m.integer("length", 8); err != nil {
					return nil, err
				}
				if c.Length <= 0 {
					c.Length = 8
				}
			}
			if err := m.strict(name); err != nil {
				return nil, err
			}
		}
		c.RandomKind = strings.TrimPrefix(name, "inputRandom")

	case "eraseText":
		c.Count = 50
		if scalar {
			if c.Count, err = p.parseInt(v); err != nil {
				return nil, err
			}
		} else if isMap {
			if err := common(); err != nil {
				return nil, err
			}
			if c.Count, err = m.integer("charactersToErase", 50); err != nil {
				return nil, err
			}
			if err := m.strict(name); err != nil {
				return nil, err
			}
		}

	case "setClipboard":
		if scalar {
			c.Text = v.Value
		} else if isMap {
			c.Text = m.str("text")
			if err := common(); err != nil {
				return nil, err
			}
			if err := m.strict(name); err != nil {
				return nil, err
			}
		}

	case "swipe":
		if !isMap {
			return nil, p.errf(v, "swipe takes a map: direction, start/end, or from + direction")
		}
		if err := p.swipe(c, m, v); err != nil {
			return nil, err
		}

	case "scrollUntilVisible":
		if !isMap {
			return nil, p.errf(v, "scrollUntilVisible takes a map with element")
		}
		if err := common(); err != nil {
			return nil, err
		}
		el := m.get("element")
		if el == nil || isNull(el) {
			return nil, p.errf(v, "scrollUntilVisible needs element")
		}
		if c.Selector, err = p.nestedSelector(el, "element"); err != nil {
			return nil, err
		}
		if c.Selector.Optional {
			c.Optional = true
		}
		c.Direction = strings.ToUpper(m.str("direction"))
		if c.Direction == "" {
			c.Direction = "DOWN"
		}
		if c.Direction != "UP" && c.Direction != "DOWN" && c.Direction != "LEFT" && c.Direction != "RIGHT" {
			return nil, p.errf(m.keys["direction"], "direction must be UP, DOWN, LEFT or RIGHT")
		}
		c.Timeout = m.str("timeout")
		c.Speed = m.str("speed")
		if c.VisibilityPct, err = m.integer("visibilityPercentage", 100); err != nil {
			return nil, err
		}
		c.VisibilityPct = max(0, min(100, c.VisibilityPct))
		if c.CenterElement, err = m.boolean("centerElement"); err != nil {
			return nil, err
		}
		if c.WaitToSettleTimeoutMs, err = m.integer("waitToSettleTimeoutMs", 0); err != nil {
			return nil, err
		}
		if err := m.strict(name); err != nil {
			return nil, err
		}

	case "back", "hideKeyboard", "pasteText", "scroll", "clearKeychain", "stopRecording", "toggleAirplaneMode",
		"toggleDarkMode", "assertDarkMode", "assertLightMode":
		if v != nil {
			if !isMap {
				return nil, p.errf(v, "%s takes no value (or {label, optional})", name)
			}
			if err := common(); err != nil {
				return nil, err
			}
			if err := m.strict(name); err != nil {
				return nil, err
			}
		}

	case "pressKey":
		if scalar {
			c.Text = v.Value
		} else if isMap {
			c.Text = m.str("key")
			if err := common(); err != nil {
				return nil, err
			}
			if err := m.strict(name); err != nil {
				return nil, err
			}
		}
		if !validKey(c.Text) {
			return nil, p.errf(v, "Unknown key name %q", c.Text)
		}

	case "launchApp":
		if scalar {
			c.AppID = v.Value
		} else if isMap {
			if err := common(); err != nil {
				return nil, err
			}
			c.AppID = m.str("appId")
			if u := m.str("url"); u != "" {
				c.AppID = u
			}
			if c.ClearState, err = m.boolean("clearState"); err != nil {
				return nil, err
			}
			if c.ClearKeychain, err = m.boolean("clearKeychain"); err != nil {
				return nil, err
			}
			if c.StopApp, err = m.boolPtr("stopApp"); err != nil {
				return nil, err
			}
			if c.Permissions, err = p.kvs(m.get("permissions")); err != nil {
				return nil, err
			}
			if a := m.get("arguments"); a != nil && !isNull(a) {
				if a.Kind != yaml.MappingNode {
					return nil, p.errf(a, "arguments must be a map")
				}
				for i := 0; i+1 < len(a.Content); i += 2 {
					c.Arguments = append(c.Arguments, ArgKV{a.Content[i].Value, typedScalar(a.Content[i+1])})
				}
			}
			if err := m.strict(name); err != nil {
				return nil, err
			}
		}

	case "stopApp", "killApp", "clearState":
		if scalar {
			c.AppID = v.Value
		} else if isMap {
			if err := common(); err != nil {
				return nil, err
			}
			c.AppID = m.str("appId")
			if err := m.strict(name); err != nil {
				return nil, err
			}
		}

	case "setPermissions":
		if !isMap {
			return nil, p.errf(v, "setPermissions takes a map with permissions")
		}
		if err := common(); err != nil {
			return nil, err
		}
		c.AppID = m.str("appId")
		if u := m.str("url"); u != "" {
			c.AppID = u
		}
		if c.Permissions, err = p.kvs(m.get("permissions")); err != nil {
			return nil, err
		}
		if err := m.strict(name); err != nil {
			return nil, err
		}

	case "openLink":
		if scalar {
			c.Text = v.Value
		} else if isMap {
			if err := common(); err != nil {
				return nil, err
			}
			c.Text = m.str("link")
			if c.AutoVerify, err = m.boolean("autoVerify"); err != nil {
				return nil, err
			}
			if c.Browser, err = m.boolean("browser"); err != nil {
				return nil, err
			}
			if err := m.strict(name); err != nil {
				return nil, err
			}
		}

	case "runFlow":
		if scalar {
			if err := p.subFile(c, v, v.Value); err != nil {
				return nil, err
			}
		} else if isMap {
			if err := common(); err != nil {
				return nil, err
			}
			file, cmds := m.get("file"), m.get("commands")
			if (file == nil) == (cmds == nil) {
				return nil, p.errf(v, "runFlow needs exactly one of file or commands")
			}
			if file != nil {
				if err := p.subFile(c, file, file.Value); err != nil {
					return nil, err
				}
			} else {
				if cmds.Kind != yaml.SequenceNode {
					return nil, p.errf(cmds, "commands must be a list")
				}
				if c.Commands, err = p.commands(cmds); err != nil {
					return nil, err
				}
			}
			if c.Env, err = p.env(m.get("env")); err != nil {
				return nil, err
			}
			if w := m.get("when"); w != nil && !isNull(w) {
				if c.When, err = p.condition(w); err != nil {
					return nil, err
				}
			}
			if err := m.strict(name); err != nil {
				return nil, err
			}
		}

	case "runScript":
		var file *yaml.Node
		if scalar {
			file = v
		} else if isMap {
			if err := common(); err != nil {
				return nil, err
			}
			file = m.get("file")
			if file == nil {
				return nil, p.errf(v, "runScript needs file")
			}
			if c.Env, err = p.env(m.get("env")); err != nil {
				return nil, err
			}
			if w := m.get("when"); w != nil && !isNull(w) {
				if c.When, err = p.condition(w); err != nil {
					return nil, err
				}
			}
			if err := m.strict(name); err != nil {
				return nil, err
			}
		}
		c.SubFile = p.resolve(file.Value)
		src, err := os.ReadFile(c.SubFile)
		if err != nil {
			return nil, p.errf(file, "script file not found: %s", c.SubFile)
		}
		c.Script = string(src)

	case "evalScript":
		if scalar {
			c.Text = v.Value
		} else if isMap {
			c.Text = m.str("script")
			c.Label = m.str("label")
		}

	case "repeat":
		if !isMap {
			return nil, p.errf(v, "repeat takes a map with commands, and times and/or while")
		}
		if err := common(); err != nil {
			return nil, err
		}
		c.Times = m.str("times")
		if w := m.get("while"); w != nil && !isNull(w) {
			if c.While, err = p.condition(w); err != nil {
				return nil, err
			}
		}
		cmds := m.get("commands")
		if cmds == nil || cmds.Kind != yaml.SequenceNode {
			return nil, p.errf(v, "repeat needs commands")
		}
		if c.Commands, err = p.commands(cmds); err != nil {
			return nil, err
		}
		if err := m.strict(name); err != nil {
			return nil, err
		}

	case "retry":
		if !isMap {
			return nil, p.errf(v, "retry takes a map with commands or file")
		}
		if err := common(); err != nil {
			return nil, err
		}
		c.MaxRetries = m.str("maxRetries")
		file, cmds := m.get("file"), m.get("commands")
		if (file == nil) == (cmds == nil) {
			return nil, p.errf(v, "retry needs exactly one of file or commands")
		}
		if file != nil {
			if err := p.subFile(c, file, file.Value); err != nil {
				return nil, err
			}
		} else {
			if c.Commands, err = p.commands(cmds); err != nil {
				return nil, err
			}
		}
		if c.Env, err = p.env(m.get("env")); err != nil {
			return nil, err
		}
		if err := m.strict(name); err != nil {
			return nil, err
		}

	case "takeScreenshot", "startRecording", "assertScreenshot":
		if scalar {
			c.Text = v.Value
		} else if isMap {
			if err := common(); err != nil {
				return nil, err
			}
			c.Text = m.str("path")
			if name != "startRecording" {
				if cr := m.get("cropOn"); cr != nil && !isNull(cr) {
					if c.CropOn, err = p.nestedSelector(cr, "cropOn"); err != nil {
						return nil, err
					}
				}
			}
			if name == "assertScreenshot" {
				c.Threshold = m.str("thresholdPercentage")
			}
			if err := m.strict(name); err != nil {
				return nil, err
			}
		}
		if name == "assertScreenshot" && c.Threshold == "" {
			c.Threshold = "95"
		}

	case "waitForAnimationToEnd":
		if v != nil {
			if !isMap {
				return nil, p.errf(v, "waitForAnimationToEnd takes no value or {timeout}")
			}
			if err := common(); err != nil {
				return nil, err
			}
			c.Timeout = m.str("timeout")
			if err := m.strict(name); err != nil {
				return nil, err
			}
		}

	case "setLocation":
		if !isMap {
			return nil, p.errf(v, "setLocation takes {latitude, longitude}")
		}
		if err := common(); err != nil {
			return nil, err
		}
		c.Latitude, c.Longitude = m.str("latitude"), m.str("longitude")
		if c.Latitude == "" || c.Longitude == "" {
			return nil, p.errf(v, "setLocation needs latitude and longitude")
		}
		if err := m.strict(name); err != nil {
			return nil, err
		}

	case "travel":
		if !isMap {
			return nil, p.errf(v, "travel takes {points, speed}")
		}
		if err := common(); err != nil {
			return nil, err
		}
		pts := m.get("points")
		if pts == nil || pts.Kind != yaml.SequenceNode {
			return nil, p.errf(v, "travel needs points")
		}
		for _, pt := range pts.Content {
			a, b, ok := strings.Cut(pt.Value, ",")
			lat, e1 := strconv.ParseFloat(strings.TrimSpace(a), 64)
			lon, e2 := strconv.ParseFloat(strings.TrimSpace(b), 64)
			if !ok || e1 != nil || e2 != nil {
				return nil, p.errf(pt, "a travel point is \"latitude,longitude\"")
			}
			c.Points = append(c.Points, [2]float64{lat, lon})
		}
		c.TravelSpeed = 4.0
		if s := m.str("speed"); s != "" {
			if c.TravelSpeed, err = strconv.ParseFloat(s, 64); err != nil {
				return nil, p.errf(m.keys["speed"], "speed is in metres per second")
			}
		}
		if err := m.strict(name); err != nil {
			return nil, err
		}

	case "setOrientation":
		if scalar {
			c.Text = v.Value
		} else if isMap {
			c.Text = m.str("orientation")
			c.Label = m.str("label")
			c.Optional = m.str("optional") == "true"
		}
		if !strings.Contains(c.Text, "${") && normalizeOrientation(c.Text) == "" {
			return nil, p.errf(v, "orientation must be PORTRAIT, LANDSCAPE_LEFT, LANDSCAPE_RIGHT or UPSIDE_DOWN")
		}

	case "addMedia":
		var list *yaml.Node
		if v.Kind == yaml.SequenceNode {
			list = v
		} else if isMap {
			if err := common(); err != nil {
				return nil, err
			}
			list = m.get("files")
			if err := m.strict(name); err != nil {
				return nil, err
			}
		}
		if list == nil || list.Kind != yaml.SequenceNode {
			return nil, p.errf(v, "addMedia takes a list of files")
		}
		for _, f := range list.Content {
			path := p.resolve(f.Value)
			if _, err := os.Stat(path); err != nil {
				return nil, p.errf(f, "media file not found: %s", path)
			}
			c.Files = append(c.Files, path)
		}

	case "setAirplaneMode", "setDarkMode":
		val := ""
		if scalar {
			val = v.Value
		} else if isMap {
			val = m.str("value")
			c.Label = m.str("label")
			c.Optional = m.str("optional") == "true"
		}
		switch val {
		case "enabled":
			c.Enabled = true
		case "disabled":
		default:
			return nil, p.errf(v, "%s takes enabled or disabled", name)
		}

	case "assertWithAI", "extractTextWithAI", "assertNoDefectsWithAI":
		c.Optional = true
		if scalar {
			c.Text = v.Value
		} else if isMap {
			c.Label = m.str("label")
			if o := m.get("optional"); o != nil {
				c.Optional, _ = p.parseBool(o)
			}
			c.Text = m.str("assertion") + m.str("query")
			c.OutputVariable = m.str("outputVariable")
		}

	case "action":
		switch scalarOr(v, "") {
		case "back", "hideKeyboard", "scroll", "clearKeychain", "pasteText":
			c.Name = v.Value
		default:
			return nil, p.errf(v, "Unknown navigation target %q", scalarOr(v, ""))
		}

	case "signal":
		if !scalar {
			return nil, p.errf(v, "signal takes a name")
		}
		c.Signal = v.Value

	case "waitForSignal":
		if scalar {
			c.Signal = v.Value
		} else if isMap {
			if err := common(); err != nil {
				return nil, err
			}
			c.Signal = m.str("name")
			c.Timeout = m.str("timeout")
			if err := m.strict(name); err != nil {
				return nil, err
			}
		}
		if c.Signal == "" {
			return nil, p.errf(v, "waitForSignal needs a signal name")
		}

	case "parallel":
		if !p.multi {
			return nil, p.errf(at, "parallel is only for multi-device flows (declare devices: in the config)")
		}
		if !isMap {
			return nil, p.errf(v, "parallel maps device roles to their commands")
		}
		c.Parallel = map[string][]*Command{}
		for i := 0; i+1 < len(v.Content); i += 2 {
			role, body := v.Content[i], v.Content[i+1]
			if !p.roles[role.Value] {
				return nil, p.errf(role, "unknown device role %q", role.Value)
			}
			blk, err := p.roleBlock(role.Value, body, role)
			if err != nil {
				return nil, err
			}
			c.Parallel[role.Value] = blk.Commands
			c.ParallelOrder = append(c.ParallelOrder, role.Value)
		}

	default:
		return nil, p.errf(at, "Invalid Command: %q", name)
	}
	return c, nil
}

func (p *parser) swipe(c *Command, m *mapView, v *yaml.Node) error {
	c.Label = strings.ReplaceAll(m.str("label"), `"`, "")
	if o := m.get("optional"); o != nil {
		c.Optional = strings.EqualFold(o.Value, "true")
	}
	c.Duration = 400
	if d := m.get("duration"); d != nil && !isNull(d) {
		n, err := p.parseInt(d)
		if err != nil {
			return err
		}
		c.Duration = n
	}
	if w := m.get("waitToSettleTimeoutMs"); w != nil {
		c.WaitToSettleTimeoutMs, _ = strconv.Atoi(strings.TrimSpace(w.Value))
	}
	start, end, dir, from := m.get("start"), m.get("end"), m.get("direction"), m.get("from")
	switch {
	case start != nil || end != nil:
		if dir != nil {
			return p.errf(v, "swipe takes either direction or start/end, not both")
		}
		if start == nil || end == nil {
			return p.errf(v, "swipe needs both start and end")
		}
		c.Start, c.End = start.Value, end.Value
		if strings.Contains(c.Start, "%") != strings.Contains(c.End, "%") {
			return p.errf(v, "swipe start and end must both be percentages or both be coordinates")
		}
	case dir != nil && from == nil:
		if dir.Value != strings.ToUpper(dir.Value) && dir.Value != strings.ToLower(dir.Value) {
			return p.errf(dir, "swipe direction must be UP, DOWN, LEFT or RIGHT (all upper or all lower case)")
		}
		c.Direction = strings.ToUpper(dir.Value)
	case dir != nil && from != nil:
		c.Direction = strings.ToUpper(dir.Value)
		s, sm, err := p.selector(from)
		if err != nil {
			return err
		}
		if sm != nil {
			c.Point = sm.str("point")
			for _, k := range tapOptionKeys {
				sm.get(k)
			}
			if err := sm.strict("from"); err != nil {
				return err
			}
		}
		c.From = s
		if s.Optional {
			c.Optional = true
		}
		if err := m.strict("swipe"); err != nil {
			return err
		}
	default:
		return p.errf(v, "Swipe command takes either: direction, start and end, or from and direction")
	}
	switch c.Direction {
	case "", "UP", "DOWN", "LEFT", "RIGHT":
	default:
		return p.errf(dir, "swipe direction must be UP, DOWN, LEFT or RIGHT")
	}
	return nil
}

func (p *parser) subFile(c *Command, at *yaml.Node, path string) error {
	abs := p.resolve(path)
	st, err := os.Stat(abs)
	if err != nil {
		return p.errf(at, "flow file not found: %s", abs)
	}
	if st.IsDir() {
		return p.errf(at, "%s is a directory, not a flow file", abs)
	}
	if abs == p.file {
		return p.errf(at, "a flow cannot run itself")
	}
	sub, err := parseFile(abs, p.stack)
	if err != nil {
		return err
	}
	c.SubFile = abs
	c.SubFlow = sub
	return nil
}

func (p *parser) kvs(n *yaml.Node) ([]KV, error) {
	if n == nil || isNull(n) {
		return nil, nil
	}
	if n.Kind != yaml.MappingNode {
		return nil, p.errf(n, "expected a map")
	}
	var out []KV
	for i := 0; i+1 < len(n.Content); i += 2 {
		out = append(out, KV{n.Content[i].Value, n.Content[i+1].Value})
	}
	return out, nil
}

// typedScalar keeps a YAML scalar's type (launch arguments are typed intent extras).
func typedScalar(n *yaml.Node) any {
	if n.Kind != yaml.ScalarNode {
		var x any
		n.Decode(&x)
		return x
	}
	switch n.Tag {
	case "!!bool":
		return strings.EqualFold(n.Value, "true")
	case "!!int":
		if i, err := strconv.ParseInt(n.Value, 0, 64); err == nil {
			return i
		}
	case "!!float":
		if f, err := strconv.ParseFloat(n.Value, 64); err == nil {
			return f
		}
	}
	return n.Value
}

var keyNames = []string{"Enter", "Backspace", "Back", "Home", "Lock", "Volume Up", "Volume Down",
	"Remote Dpad Up", "Remote Dpad Down", "Remote Dpad Left", "Remote Dpad Right", "Remote Dpad Center",
	"Remote Media Play Pause", "Remote Media Stop", "Remote Media Next", "Remote Media Previous",
	"Remote Media Rewind", "Remote Media Fast Forward", "Escape", "Power", "Tab",
	"Remote System Navigation Up", "Remote System Navigation Down", "Remote Button A", "Remote Button B",
	"Remote Menu", "TV Input", "TV Input HDMI 1", "TV Input HDMI 2", "TV Input HDMI 3"}

func validKey(k string) bool {
	for _, n := range keyNames {
		if strings.EqualFold(n, strings.TrimSpace(k)) {
			return true
		}
	}
	return false
}

// normalizeOrientation maps any spelling (landscapeLeft, LANDSCAPE_LEFT) to the enum name, or "".
func normalizeOrientation(s string) string {
	k := strings.ToUpper(strings.ReplaceAll(s, "_", ""))
	switch k {
	case "PORTRAIT":
		return "PORTRAIT"
	case "LANDSCAPELEFT":
		return "LANDSCAPE_LEFT"
	case "LANDSCAPERIGHT":
		return "LANDSCAPE_RIGHT"
	case "UPSIDEDOWN":
		return "UPSIDE_DOWN"
	}
	return ""
}

// NormalizeOrientation is exported for the engine (run-time values).
func NormalizeOrientation(s string) string { return normalizeOrientation(s) }

var _ = filepath.Join
