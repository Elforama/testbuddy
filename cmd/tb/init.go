package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const initUsage = `tb init: offer tb to the coding agents on this machine, then check the setup

  tb init [--dry-run]

For each client it finds on PATH, tb init registers tb's MCP server (tb mcp) with the
client's own command, for every project of this user:

  Claude Code   claude mcp add --scope user tb -- <tb> mcp
  Codex         codex mcp add tb -- <tb> mcp
  Gemini CLI    gemini mcp add --scope user tb <tb> mcp

One line per client: added, skipped (not installed, or it already has tb: an MCP server
named tb, or Claude Code's test-buddy plugin) or failed. It writes no file itself. It ends
with the checks of tb doctor. Other clients: see "Use with AI coding agents" in the README.

  --dry-run   print the commands it would run, and change nothing

Exit status: 0 ok, 1 a client's command failed, 2 bad input. tb doctor's status says
whether the setup itself is ready.
`

type initEnv struct {
	home     string
	tb       string
	lookPath func(string) (string, error)
	run      func(name string, args ...string) (string, error)
	doctor   doctorEnv
}

type agentClient struct {
	name string
	exe  string
	has  func(home string) string
	add  func(tb string) []string
}

var agentClients = []agentClient{
	{"Claude Code", "claude", claudeHasTB, func(tb string) []string {
		return []string{"mcp", "add", "--scope", "user", "tb", "--", tb, "mcp"}
	}},
	{"Codex", "codex", codexHasTB, func(tb string) []string {
		return []string{"mcp", "add", "tb", "--", tb, "mcp"}
	}},
	{"Gemini CLI", "gemini", geminiHasTB, func(tb string) []string {
		return []string{"mcp", "add", "--scope", "user", "tb", tb, "mcp"}
	}},
}

func jsonHasKey(path string, keys ...string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var v any
	if json.Unmarshal(b, &v) != nil {
		return false
	}
	for _, k := range keys {
		m, _ := v.(map[string]any)
		if v = m[k]; v == nil {
			return false
		}
	}
	return true
}

func claudeHasTB(home string) string {
	if jsonHasKey(filepath.Join(home, ".claude.json"), "mcpServers", "tb") {
		return "MCP server tb in ~/.claude.json"
	}
	if jsonHasKey(filepath.Join(home, ".claude", "plugins", "installed_plugins.json"), "plugins", "test-buddy@test-buddy") {
		return "the test-buddy plugin: the tb CLI and skill"
	}
	return ""
}

func codexHasTB(home string) string {
	b, _ := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) == "[mcp_servers.tb]" {
			return "MCP server tb in ~/.codex/config.toml"
		}
	}
	return ""
}

func geminiHasTB(home string) string {
	if jsonHasKey(filepath.Join(home, ".gemini", "settings.json"), "mcpServers", "tb") {
		return "MCP server tb in ~/.gemini/settings.json"
	}
	return ""
}

func shellLine(exe string, args []string) string {
	words := []string{exe}
	for _, a := range args {
		if strings.ContainsAny(a, " \t\"'") {
			a = fmt.Sprintf("%q", a)
		}
		words = append(words, a)
	}
	return strings.Join(words, " ")
}

func runInit(e initEnv, dryRun bool, w io.Writer) int {
	code, found := 0, false
	for _, c := range agentClients {
		exe, err := e.lookPath(c.exe)
		if err != nil {
			fmt.Fprintf(w, "skipped %s: not installed (no %s on PATH)\n", c.name, c.exe)
			continue
		}
		found = true
		if how := c.has(e.home); how != "" {
			fmt.Fprintf(w, "skipped %s: already has tb (%s)\n", c.name, how)
			continue
		}
		args := c.add(e.tb)
		line := shellLine(c.exe, args)
		if dryRun {
			fmt.Fprintf(w, "would run for %s: %s\n", c.name, line)
			continue
		}
		if out, err := e.run(exe, args...); err != nil {
			said := strings.TrimSpace(out)
			if said == "" {
				said = err.Error()
			}
			fmt.Fprintf(w, "failed %s: %s: %s\n", c.name, line, said)
			code = 1
			continue
		}
		fmt.Fprintf(w, "added %s: %s\n", c.name, line)
	}
	if !found {
		fmt.Fprintln(w, "no supported coding agent found: tb agent-guide prints instructions any agent can use")
	}
	if dryRun {
		fmt.Fprintln(w, "dry run: nothing was changed (tb init writes no files itself; each client's own command records the server)")
	}
	doctor(e.doctor, false, w)
	return code
}

func cmdInit(argv []string) int {
	a, err := parseArgs(argv, flagSpec{[]string{"dry-run"}, boolFlag}, flagSpec{[]string{"help", "h"}, boolFlag})
	if err == nil {
		err = a.noPos()
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n\n%s", err, initUsage)
		return 2
	}
	if a.flag("help") {
		fmt.Print(initUsage)
		return 0
	}
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	tb, err := os.Executable()
	if err != nil {
		tb = "tb"
	}
	return runInit(initEnv{home: home, tb: tb, lookPath: exec.LookPath, run: runCommand, doctor: systemDoctorEnv()},
		a.flag("dry-run"), os.Stdout)
}
