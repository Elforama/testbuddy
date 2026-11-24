package legacy

import "strings"

const name = "maestro"

func ScriptObject() string { return name }

func EnvPrefix() string { return strings.ToUpper(name) + "_" }

func FlowsFolder() string { return "." + name }

func AndroidDriverPackages() []string {
	base := "dev.mobile." + name
	return []string{base + ".test", base}
}

func HostProcessPattern() string { return name }
