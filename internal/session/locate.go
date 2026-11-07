package session

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/elforama/testbuddy/internal/daemon"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/look"
	"github.com/elforama/testbuddy/internal/ui"
)

type locatorCandidate struct {
	Ref     int    `json:"ref,omitempty"`
	ID      string `json:"id,omitempty"`
	Label   string `json:"label,omitempty"`
	Bounds  [4]int `json:"bounds"`
	Enabled bool   `json:"enabled"`
	Chosen  bool   `json:"chosen"`
}

func (s *Service) locate(ctx context.Context, req daemon.Request, _ func(daemon.Response)) daemon.Response {
	a, err := decode[struct {
		devArgs
		Selector string `json:"selector"`
	}](req)
	if err != nil {
		return fail(2, "%v", err)
	}
	selector := strings.TrimSpace(a.Selector)
	if selector == "" {
		return fail(2, "a selector is required")
	}
	d, err := s.resolve(ctx, a.devArgs)
	if err != nil {
		return fail(2, "%v", err)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	// Use the tap selector grammar and the same ref expansion as an action.
	if !strings.HasPrefix(selector, "{") && !strings.HasPrefix(selector, "@") {
		quoted, _ := json.Marshal(selector)
		selector = string(quoted)
	}
	cmd := "tapOn: " + selector
	if d.last != nil {
		cmd, err = look.ResolveRefs(cmd, d.last, d.last.Screen)
	} else {
		cmd, err = look.ResolveRefs(cmd, nil, nil)
	}
	if err != nil {
		return fail(2, "%v", err)
	}
	cmds, err := flow.ParseCommands(req.Cwd, []byte(cmd))
	if err != nil {
		return fail(2, "%v", err)
	}
	if len(cmds) != 1 || cmds[0].Name != "tapOn" || !cmds[0].Selector.HasCriteria() {
		return fail(2, "expected one element selector")
	}
	screen, err := d.Drv.Screen(ctx)
	if err != nil {
		return fail(1, "%v", err)
	}
	pick, matches, err := d.runner.Locate(ctx, screen, cmds[0].Selector)
	if err != nil {
		return fail(2, "%v", err)
	}
	list := look.Build(screen, d.refs)
	list.MarkNew(d.last)
	d.last = list
	refs := make(map[*ui.Node]int, len(list.Elements))
	for _, e := range list.Elements {
		refs[e.Node] = e.Ref
	}
	candidates := make([]locatorCandidate, 0, len(matches))
	var out strings.Builder
	fmt.Fprintf(&out, "%d matches\n", len(matches))
	for _, n := range matches {
		b := n.Bounds
		c := locatorCandidate{refs[n], n.ID, n.Label(), [4]int{b.L, b.T, b.R, b.B}, n.Enabled, n == pick}
		candidates = append(candidates, c)
		prefix := "  "
		if c.Chosen {
			prefix = "* "
		}
		if c.Ref != 0 {
			prefix += fmt.Sprintf("@%d ", c.Ref)
		}
		fmt.Fprintf(&out, "%s%s %q %s %s\n", prefix, n.ShortClass(), c.Label, c.ID, b)
	}
	report, _ := json.Marshal(map[string]any{"count": len(matches), "candidates": candidates})
	return daemon.Response{OK: true, Text: out.String(), JSON: report}
}
