package flow

import (
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

var textOrMapCommands = map[string]bool{"openLink": true, "takeScreenshot": true, "startRecording": true,
	"assertScreenshot": true, "setClipboard": true, "eraseText": true, "launchApp": true, "stopApp": true,
	"killApp": true, "clearState": true, "evalScript": true, "runFlow": true, "runScript": true,
	"assertWithAI": true, "extractTextWithAI": true, "assertNoDefectsWithAI": true}

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
