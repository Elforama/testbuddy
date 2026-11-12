package flow

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

func (p *parser) newCmd(name string, at *yaml.Node) *Command {
	c := &Command{Name: name, File: p.file}
	if at != nil {
		c.Line = at.Line
	}
	return c
}

// commandOptions reads the label and optional flag shared by command maps.
func (m *mapView) commandOptions(c *Command) error {
	if m == nil {
		return nil
	}
	c.Label = m.str("label")
	var err error
	c.Optional, err = m.boolean("optional")
	return err
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
	var err error

	// Commands that take a text or a map: a list value is a mistake, not an empty command.
	if v != nil && v.Kind == yaml.SequenceNode && textOrMapCommands[name] {
		return nil, p.errf(v, "%s takes a text or a map, not a list", name)
	}

	switch name {
	case "tapOn", "doubleTapOn", "longPressOn":
		err = p.parseTap(c, v)

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
			if err := m.commandOptions(c); err != nil {
				return nil, err
			}
		} else {
			return nil, p.errf(v, "assertTrue takes a condition")
		}

	case "extendedWaitUntil":
		if !isMap {
			return nil, p.errf(v, "extendedWaitUntil takes a map with visible or notVisible, and timeout")
		}
		if err := m.commandOptions(c); err != nil {
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
			if err := m.commandOptions(c); err != nil {
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
			if err := m.commandOptions(c); err != nil {
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
			if err := m.commandOptions(c); err != nil {
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
		err = p.parseScrollUntilVisible(c, m, v)

	case "scrollUntilVisibleIn":
		err = p.parseScrollUntilVisibleIn(c, m, v, at)

	case "back", "hideKeyboard", "pasteText", "scroll", "clearKeychain", "stopRecording", "toggleAirplaneMode",
		"toggleDarkMode", "assertDarkMode", "assertLightMode":
		if v != nil {
			if !isMap {
				return nil, p.errf(v, "%s takes no value (or {label, optional})", name)
			}
			if err := m.commandOptions(c); err != nil {
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
			if err := m.commandOptions(c); err != nil {
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
			if err := m.commandOptions(c); err != nil {
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
			if err := m.commandOptions(c); err != nil {
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
		if err := m.commandOptions(c); err != nil {
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
			if err := m.commandOptions(c); err != nil {
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
		err = p.parseRunFlow(c, m, v)

	case "runScript":
		err = p.parseRunScript(c, m, v)

	case "evalScript":
		if scalar {
			c.Text = v.Value
		} else if isMap {
			c.Text = m.str("script")
			c.Label = m.str("label")
		}

	case "repeat":
		err = p.parseRepeat(c, m, v)

	case "retry":
		err = p.parseRetry(c, m, v)

	case "takeScreenshot", "startRecording", "assertScreenshot":
		if scalar {
			c.Text = v.Value
		} else if isMap {
			if err := m.commandOptions(c); err != nil {
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
			if err := m.commandOptions(c); err != nil {
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
		if err := m.commandOptions(c); err != nil {
			return nil, err
		}
		c.Latitude, c.Longitude = m.str("latitude"), m.str("longitude")
		if c.Latitude == "" || c.Longitude == "" {
			return nil, p.errf(v, "setLocation needs latitude and longitude")
		}
		for _, k := range []struct {
			key  string
			read func(string) (float64, error)
		}{{"latitude", Latitude}, {"longitude", Longitude}} {
			// A value with ${...} is only known at run time: the engine checks it then.
			if n := m.get(k.key); n.Kind != yaml.ScalarNode || !strings.Contains(n.Value, "${") {
				if _, err := k.read(n.Value); err != nil {
					return nil, p.errf(n, "%v", err)
				}
			}
		}
		if err := m.strict(name); err != nil {
			return nil, err
		}

	case "travel":
		if !isMap {
			return nil, p.errf(v, "travel takes {points, speed}")
		}
		if err := m.commandOptions(c); err != nil {
			return nil, err
		}
		pts := m.get("points")
		if pts == nil || pts.Kind != yaml.SequenceNode {
			return nil, p.errf(v, "travel needs points")
		}
		for _, pt := range pts.Content {
			a, b, ok := strings.Cut(pt.Value, ",")
			if !ok {
				return nil, p.errf(pt, "a travel point is \"latitude,longitude\"")
			}
			lat, err := Latitude(strings.TrimSpace(a))
			if err != nil {
				return nil, p.errf(pt, "a travel point is \"latitude,longitude\": %v", err)
			}
			lon, err := Longitude(strings.TrimSpace(b))
			if err != nil {
				return nil, p.errf(pt, "a travel point is \"latitude,longitude\": %v", err)
			}
			c.Points = append(c.Points, [2]float64{lat, lon})
		}
		if s := m.get("speed"); s != nil && !isNull(s) {
			c.TravelSpeed = s.Value
			// A value with ${...} is only known at run time: the engine checks it then.
			if s.Kind != yaml.ScalarNode || !strings.Contains(s.Value, "${") {
				if _, err := TravelSpeed(s.Value); err != nil {
					return nil, p.errf(s, "%v", err)
				}
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
			if err := m.commandOptions(c); err != nil {
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
			if err := m.commandOptions(c); err != nil {
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
		err = p.parseParallel(c, m, v, at)

	case "mockNetwork":
		err = p.parseMockNetwork(c, m, v, at)

	case "clearNetworkMocks":
		if isMap {
			if err := m.commandOptions(c); err != nil {
				return nil, err
			}
			if err := m.strict(name); err != nil {
				return nil, err
			}
		}

	default:
		return nil, p.errf(at, "Invalid Command: %q", name)
	}
	if err != nil {
		return nil, err
	}
	return c, nil
}

// Latitude reads a setLocation's or a travel point's latitude: a finite number from -90 to
// 90. Anything else (text, empty, NaN, Inf, out of range) is an error: NaN and Inf were sent
// to the device as they were, and a travel's distance and wait became NaN (issue #113).
func Latitude(s string) (float64, error) {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || math.IsNaN(v) || v < -90 || v > 90 {
		return 0, fmt.Errorf("invalid latitude %q: expected a number from -90 to 90", s)
	}
	return v, nil
}

// Longitude reads a setLocation's or a travel point's longitude: a finite number from -180 to
// 180. Anything else (text, empty, NaN, Inf, out of range) is an error (issue #113).
func Longitude(s string) (float64, error) {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || math.IsNaN(v) || v < -180 || v > 180 {
		return 0, fmt.Errorf("invalid longitude %q: expected a number from -180 to 180", s)
	}
	return v, nil
}

// DefaultTravelSpeed is travel's speed, in metres per second, when the flow gives none.
const DefaultTravelSpeed = 4.0

// TravelSpeed reads a travel's speed in metres per second: a finite number above 0. Anything
// else (0, negative, text, empty, NaN, Inf) is an error: at 0 the wait between points was
// endless on arm64 and skipped on amd64, and a negative speed skipped it (issue #44).
func TravelSpeed(s string) (float64, error) {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
		return 0, fmt.Errorf("invalid speed %q: expected metres per second, a number above 0", s)
	}
	return v, nil
}
