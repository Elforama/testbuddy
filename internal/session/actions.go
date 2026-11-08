package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	_ "image/jpeg"
	"strings"
	"time"

	"github.com/elforama/testbuddy/internal/daemon"
	"github.com/elforama/testbuddy/internal/engine"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/look"
)

type doArgs struct {
	devArgs
	Commands string            `json:"commands"` // YAML: one command or a list
	App      string            `json:"app"`
	Env      map[string]string `json:"env"`
	JSON     bool              `json:"json"`
}

type actionStep struct {
	Description string        `json:"description"`
	Status      engine.Status `json:"status"`
	DurationMS  int64         `json:"duration_ms"`
	Error       string        `json:"error,omitempty"`
}

type actionResult struct {
	Exit        int             `json:"exit"`
	Steps       []actionStep    `json:"steps"`
	Error       string          `json:"error,omitempty"`
	Screen      *look.Listing   `json:"screen,omitempty"`
	ScreenError string          `json:"screen_error,omitempty"`
	Query       string          `json:"query,omitempty"`
	Similar     []*look.Element `json:"similar,omitempty"`
}

func (s *Service) do(ctx context.Context, req daemon.Request, send func(daemon.Response)) daemon.Response {
	a, err := decode[doArgs](req)
	if err != nil {
		return fail(2, "%v", err)
	}
	result := actionResult{Steps: []actionStep{}}
	badInput := func(err error) daemon.Response {
		resp := fail(2, "%v", err)
		if a.JSON {
			result.Exit, result.Error = resp.Exit, resp.Text
			resp.JSON, _ = json.Marshal(result)
		}
		return resp
	}
	d, err := s.resolve(ctx, a.devArgs)
	if err != nil {
		return badInput(err)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if a.App != "" {
		d.AppID = a.App
	}
	var lastScreen = d.last
	text := a.Commands
	if lastScreen != nil {
		text, err = look.ResolveRefs(text, lastScreen, lastScreen.Screen)
	} else {
		text, err = look.ResolveRefs(text, nil, nil)
	}
	if err != nil {
		return badInput(err)
	}
	cmds, err := flow.ParseCommands(req.Cwd, []byte(text))
	if err != nil {
		return badInput(err)
	}
	items, _ := splitItems(text)
	appID := d.AppID
	if appID == "" {
		if fg, err := d.Drv.Foreground(ctx); err == nil {
			appID = fg
		}
	}
	var out strings.Builder
	exit := 0
	// Agent think time is not the app's: lookups count from now.
	runner := d.runner
	idx := 0
	var env []flow.KV
	for k, v := range a.Env {
		env = append(env, flow.KV{Key: k, Value: v})
	}
	runner.SetEnv(env)
	runErr := runner.RunCommands(ctx, cmds, appID, func(st engine.StepResult) {
		if a.JSON {
			result.Steps = append(result.Steps, actionStep{st.Desc, st.Status, st.Duration.Milliseconds(), st.Err})
		}
		mark := "ok"
		switch st.Status {
		case engine.Failed:
			mark = "FAILED"
			exit = 1
		case engine.Warned:
			mark = "warned"
		case engine.Skipped:
			mark = "skipped"
		}
		fmt.Fprintf(&out, "%s %s  %s\n", mark, st.Desc, fmtDur(st.Duration))
		if st.Err != "" {
			fmt.Fprintf(&out, "  %s\n", strings.ReplaceAll(st.Err, "\n", "\n  "))
		}
		if st.Status != engine.Failed && idx < len(items) {
			d.steps = append(d.steps, items[idx])
		}
		idx++
	})
	var nf *engine.NotFound
	// Report the screen as it settles, not mid-change (an app that just launched has no
	// content yet). Answers at once when nothing is pending.
	d.Drv.WaitSettled(ctx, 3*time.Second)
	scr, serr := d.Drv.Screen(ctx)
	if serr == nil {
		now := look.Build(scr, d.refs)
		if errors.As(runErr, &nf) && nf.Query != "" {
			sim := now.Similar(nf.Query)
			result.Query, result.Similar = nf.Query, sim
			if len(sim) > 0 {
				var parts []string
				for _, e := range sim {
					parts = append(parts, e.Line())
				}
				fmt.Fprintf(&out, "  similar: %s\n", strings.Join(parts, "; "))
			}
		}
		out.WriteString(look.Diff(d.last, now))
		d.last = now
		d.lastDoEnd = now
		result.Screen = now
	} else {
		result.ScreenError = serr.Error()
	}
	resp := daemon.Response{OK: exit == 0, Exit: exit, Text: out.String()}
	if a.JSON {
		result.Exit = exit
		if runErr != nil {
			result.Error = runErr.Error()
		}
		resp.JSON, _ = json.Marshal(result)
	}
	return resp
}

// splitItems splits command YAML into one YAML snippet per top-level list item. Comments and
// blank lines before the first item are dropped; an indented list is cut at its own indent
// and moved to column 0.
func splitItems(text string) ([]string, error) {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	first := 0 // the first line that is not blank or a comment
	for first < len(lines) {
		if t := strings.TrimSpace(lines[first]); t != "" && !strings.HasPrefix(t, "#") {
			break
		}
		first++
	}
	if first == len(lines) || !isItem(strings.TrimLeft(lines[first], " ")) {
		// a single command without a dash: make it a list item
		body := strings.Join(lines, "\n  ")
		return []string{"- " + body}, nil
	}
	indent := len(lines[first]) - len(strings.TrimLeft(lines[first], " "))
	pad := strings.Repeat(" ", indent)
	var items []string
	var cur []string
	for _, l := range lines[first:] {
		l = strings.TrimPrefix(l, pad)
		if isItem(l) {
			if len(cur) > 0 {
				items = append(items, strings.Join(cur, "\n"))
			}
			cur = []string{l}
			continue
		}
		cur = append(cur, l)
	}
	return append(items, strings.Join(cur, "\n")), nil
}

// isItem reports whether a line starts a list item.
func isItem(l string) bool { return strings.HasPrefix(l, "- ") || l == "-" }

func fmtDur(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}
