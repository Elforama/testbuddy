// Package legacy keeps Maestro flows working under tb.
//
// tb runs Maestro's YAML flow format, so existing Maestro flows run unchanged. tb has its own
// names for what flows can reach at run time: the `tb` object in scripts (`tb.copiedText`,
// `tb.platform`), the TB_ variables it passes to every flow (TB_FILENAME, TB_DEVICE_UDID,
// TB_SHARD_ID, TB_SHARD_INDEX), and the `.tb` folder that a workspace may keep its flows in.
// Maestro flows use Maestro's names for the same things (`maestro.copiedText`, MAESTRO_
// variables, a `.maestro` folder); tb accepts those too, as aliases. They are kept here.
package legacy

import "strings"

const name = "maestro"

// ScriptObject is the scripts' `tb` object under Maestro's name: `maestro`.
func ScriptObject() string { return name }

// EnvPrefix is Maestro's prefix for the variables tb passes to flows ("TB_" in tb's terms):
// MAESTRO_. Variables in the environment that start with it are also passed to flows, as
// Maestro does.
func EnvPrefix() string { return strings.ToUpper(name) + "_" }

// FlowsFolder is Maestro's hidden folder for a workspace's flows: `.maestro`.
func FlowsFolder() string { return "." + name }

// AndroidDriverPackages are Maestro's on-device driver packages. A leftover one (an emulator
// snapshot often restores it) holds the device's only UI automation slot.
func AndroidDriverPackages() []string {
	base := "dev.mobile." + name
	return []string{base + ".test", base}
}

// HostProcessPattern matches Maestro's processes on this machine (pgrep -f): while one runs,
// its on-device driver is in use and is left alone.
func HostProcessPattern() string { return name }
