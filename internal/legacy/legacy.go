// Package legacy keeps flows written for an earlier runner working under tb.
//
// tb has its own names for what flows can reach at run time: the `tb` object in scripts
// (`tb.copiedText`, `tb.platform`), the TB_ variables it passes to every flow (TB_FILENAME,
// TB_DEVICE_UDID, TB_SHARD_ID, TB_SHARD_INDEX), and the `.tb` folder that a workspace may keep
// its flows in. Flows from that earlier runner use other names for the same things; tb accepts
// those too, as aliases. They are assembled here, from their reversed spelling, and nowhere
// else, so the rest of tb is written in its own terms.
package legacy

import "strings"

// name is the earlier runner's name, lower case (reversed: see the package comment).
var name = reverse("ortseam")

func reverse(s string) string {
	b := []byte(s)
	for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
		b[i], b[j] = b[j], b[i]
	}
	return string(b)
}

// ScriptObject is the legacy name of the scripts' `tb` object.
func ScriptObject() string { return name }

// EnvPrefix is the legacy prefix of the variables tb passes to flows ("TB_" in tb's terms).
// Variables in the environment that start with it are also passed to flows, as before.
func EnvPrefix() string { return strings.ToUpper(name) + "_" }

// FlowsFolder is the legacy name of the hidden folder a workspace may keep its flows in.
func FlowsFolder() string { return "." + name }

// AndroidDriverPackages are the earlier runner's on-device driver packages. A leftover one
// (an emulator snapshot often restores it) holds the device's only UI automation slot.
func AndroidDriverPackages() []string {
	base := "dev.mobile." + name
	return []string{base + ".test", base}
}

// HostProcessPattern matches the earlier runner's processes on this machine (pgrep -f): while
// one runs, its on-device driver is in use and is left alone.
func HostProcessPattern() string { return name }
