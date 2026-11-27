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
	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/engine"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/look"
)

type doArgs struct {
	devArgs
	Commands string            `json:"commands"`
	App      string            `json:"app"`
	Env      map[string]string `json:"env"`
	JSON     bool              `json:"json"`
	Replay   *ReplayArgs       `json:"replay,omitempty"`
}

type ReplayArgs struct {
	App     string    `json:"app"`
	Env     []flow.KV `json:"env"`
	FlowEnv []flow.KV `json:"flow_env"`
}

type actionStep struct {
	Description string        `json:"description"`
	Status      engine.Status `json:"status"`
	DurationMS  int64         `json:"duration_ms"`
	Error       string        `json:"error,omitempty"`
	Reason      string        `json:"reason,omitempty"`
	Hints       []string      `json:"hints,omitempty"`
	Notes       []string      `json:"notes,omitempty"`
}

type actionResult struct {
	Exit        int             `json:"exit"`
	Steps       []actionStep    `json:"steps"`
	Error       string          `json:"error,omitempty"`
	Notes       []string        `json:"notes,omitempty"`
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
	if err := s.lockOpen(d, req, send); err != nil {
		resp := fail(1, "%v", err)
		if a.JSON {
			result.Exit, result.Error = resp.Exit, resp.Text
			resp.JSON, _ = json.Marshal(result)
		}
		return resp
	}
	defer d.mu.Unlock()
	var lastScreen = d.last
	text := a.Commands
	if a.Replay != nil {
		a.App = ""
	} else if lastScreen != nil {
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
	runner := d.runner
	var record []string
	if a.Replay != nil {
		leave, err := runner.ScopeEnv(a.Replay.Env, a.Replay.FlowEnv)
		if err != nil {
			return badInput(err)
		}
		defer leave()
		if a.App, err = runner.Eval(a.Replay.App); err != nil {
			return badInput(err)
		}
		if strings.Contains(a.Replay.App, "${") && strings.Contains(a.App, "undefined") {
			return badInput(fmt.Errorf("the flow's app %q does not resolve with this env: it gives %q", a.Replay.App, a.App))
		}
	} else {
		items, _ := splitItems(text)
		fresh := lastScreen != nil && lastScreen.Screen != nil && lastScreen.Screen.Seq == d.Drv.Seq()
		record = stableItems(lastScreen, fresh, cmds, items)
	}
	if a.App != "" {
		d.AppID = a.App
	}
	appID := d.AppID
	if appID == "" {
		if fg, err := d.Drv.Foreground(ctx); err == nil {
			appID = fg
		}
	}
	var out strings.Builder
	exit := 0
	idx := 0
	var env []flow.KV
	for k, v := range a.Env {
		env = append(env, flow.KV{Key: k, Value: v})
	}
	runner.SetEnv(env)
	result.Notes = dialogNotes(d)
	for _, n := range result.Notes {
		fmt.Fprintf(&out, "note: %s\n", n)
	}
	if d.last != nil {
		runner.SetSeen(d.last.Screen)
	}
	runErr := runner.RunCommands(ctx, cmds, appID, func(st engine.StepResult) {
		notes := dialogNotes(d)
		if a.JSON {
			result.Steps = append(result.Steps, actionStep{st.Desc, st.Status, st.Duration.Milliseconds(), st.Err, st.Reason, st.Hints, notes})
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
		for _, n := range notes {
			fmt.Fprintf(&out, "  note: %s\n", n)
		}
		for _, h := range st.Hints {
			fmt.Fprintf(&out, "  hint: %s\n", h)
		}
		if st.Status != engine.Failed && idx < len(record) {
			d.stepsMu.Lock()
			d.steps = append(d.steps, record[idx])
			d.stepsMu.Unlock()
		}
		idx++
	})
	d.Drv.WaitSettled(ctx, 3*time.Second)
	after := dialogNotes(d)
	result.Notes = append(result.Notes, after...)
	for _, n := range after {
		fmt.Fprintf(&out, "note: %s\n", n)
	}
	scr, serr := readNow(ctx, d)
	if serr == nil {
		now := look.Build(scr, d.refs)
		if query := missedQuery(runErr); query != "" {
			sim := now.Similar(query)
			result.Query, result.Similar = query, sim
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

func missedQuery(runErr error) string {
	var nf *engine.NotFound
	if !errors.As(runErr, &nf) {
		return ""
	}
	if nf.Query != "" {
		return nf.Query
	}
	return nf.ID
}

func splitItems(text string) ([]string, error) {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	first := 0
	for first < len(lines) {
		if t := strings.TrimSpace(lines[first]); t != "" && !strings.HasPrefix(t, "#") {
			break
		}
		first++
	}
	if first == len(lines) || !isItem(strings.TrimLeft(lines[first], " ")) {
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

func isItem(l string) bool { return strings.HasPrefix(l, "- ") || l == "-" }

func dialogNotes(d *Dev) []string {
	dl, ok := d.Drv.(driver.DialogLog)
	if !ok {
		return nil
	}
	ds, next := dl.Dialogs(d.dialogMark)
	d.dialogMark = next
	var notes []string
	for _, x := range ds {
		if x.Note != "" {
			notes = append(notes, x.Note)
		} else {
			notes = append(notes, fmt.Sprintf("web: the page opened a JavaScript %s %q: tb answered %s", x.Type, x.Message, x.Answer))
		}
	}
	return notes
}

func markDialogs(d *Dev) {
	if dl, ok := d.Drv.(driver.DialogLog); ok {
		_, d.dialogMark = dl.Dialogs(int(^uint(0) >> 1))
	}
}

func fmtDur(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}
