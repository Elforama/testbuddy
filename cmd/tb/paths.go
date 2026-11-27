package main

import (
	"fmt"
	"os"
	"strings"
)

const goUsage = `usage: tb go <name> [-d device] [-e KEY=value] [--json]   run the path saved as <name> and show the screen
       tb go [-d device]                                 list the paths saved for the session's app
A path is the flow paths/<app>/<name>.yaml under the directory tb runs in: tb save --path <name> writes it.`

func cmdGo(argv []string) int {
	a, err := parseArgs(argv, flagSpec{[]string{"device", "d"}, textFlag}, flagSpec{[]string{"platform", "p"}, textFlag},
		flagSpec{[]string{"env", "e"}, textFlag}, flagSpec{[]string{"json"}, boolFlag}, flagSpec{[]string{"help", "h"}, boolFlag}, noWaitFlag)
	if err != nil || len(a.pos) > 1 {
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		fmt.Fprintln(os.Stderr, goUsage)
		return 2
	}
	if a.flag("help") {
		fmt.Println(goUsage)
		return 0
	}
	env := map[string]string{}
	for _, kv := range a.vals["env"] {
		k, v, _ := strings.Cut(kv, "=")
		env[k] = v
	}
	req := map[string]any{"device": a.str("device"), "name": strings.Join(a.pos, ""), "env": env}
	if p := a.str("platform"); p != "" {
		req["platform"] = p
	}
	if a.flag("json") {
		req["json"] = true
	}
	noWait(a, req)
	return callDaemon("go", req)
}
