package session

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/elforama/testbuddy/internal/daemon"
	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/look"
)

type readArgs struct {
	devArgs
	Label  string  `json:"label"`
	Equals *string `json:"equals"`
	JSON   bool    `json:"json"`
}

type readResult struct {
	Exit  int    `json:"exit"`
	Query string `json:"query"`
	*look.Reading
	Equals  *string         `json:"equals,omitempty"`
	Error   string          `json:"error,omitempty"`
	Similar []*look.Element `json:"similar,omitempty"`
}

func (s *Service) listNow(ctx context.Context, d *Dev) (*look.Listing, error) {
	scr, err := readNow(ctx, d)
	if err != nil {
		return nil, err
	}
	l := look.Build(scr, d.refs)
	d.last = l
	return l, nil
}

func (s *Service) read(ctx context.Context, req daemon.Request, send func(daemon.Response)) daemon.Response {
	a, err := decode[readArgs](req)
	if err != nil {
		return fail(2, "%v", err)
	}
	query := strings.TrimSpace(a.Label)
	if query == "" {
		return fail(2, "a label is required")
	}
	d, err := s.resolve(ctx, a.devArgs)
	if err != nil {
		return fail(2, "%v", err)
	}
	if err := s.lockOpen(d, req, send); err != nil {
		return fail(1, "%v", err)
	}
	defer d.mu.Unlock()
	l, err := s.listNow(ctx, d)
	if err != nil {
		return fail(1, "%v", err)
	}
	result := readResult{Query: query, Equals: a.Equals}
	var text string
	reading, onScreen := l.Read(query)
	if reading == nil {
		d.Drv.WaitSettled(ctx, 3*time.Second)
		if l, err = s.listNow(ctx, d); err != nil {
			return fail(1, "%v", err)
		}
		reading, onScreen = l.Read(query)
	}
	switch {
	case reading == nil && !onScreen:
		result.Error = fmt.Sprintf("%q is not on screen", query)
		text = result.Error + "\n"
		if result.Similar = l.Similar(query); len(result.Similar) > 0 {
			parts := make([]string, len(result.Similar))
			for i, e := range result.Similar {
				parts[i] = e.Line()
			}
			text += "  similar: " + strings.Join(parts, "; ") + "\n"
		}
	case reading == nil:
		result.Error = fmt.Sprintf("%q is on screen but tb cannot tell its value: use tb look", query)
		text = result.Error + "\n"
	case a.Equals != nil && strings.TrimSpace(*a.Equals) != strings.TrimSpace(reading.Value):
		result.Reading = reading
		result.Error = fmt.Sprintf("%q shows %q, not %q", query, reading.Value, *a.Equals)
		text = result.Error + "\n"
	default:
		result.Reading = reading
		text = fmt.Sprintf("ok %q = %q", query, reading.Value)
		var notes []string
		if where := map[string]string{"inside": "inside it", "right": "to its right", "below": "under it"}[reading.From]; where != "" {
			notes = append(notes, strings.ToLower(reading.Of)+" "+where)
		}
		if reading.Matches > 1 {
			notes = append(notes, fmt.Sprintf("%d elements match %q", reading.Matches, query))
		}
		if len(notes) > 0 {
			text += " (" + strings.Join(notes, "; ") + ")"
		}
		text += "\n"
	}
	if result.Error != "" {
		result.Exit = 1
	}
	resp := daemon.Response{OK: result.Exit == 0, Exit: result.Exit, Text: text}
	if a.JSON {
		resp.JSON, _ = json.Marshal(result)
	}
	return resp
}

type collectArgs struct {
	devArgs
	Direction  string `json:"direction"`
	Rows       int    `json:"rows"`
	MaxScrolls int    `json:"max_scrolls"`
	JSON       bool   `json:"json"`
}

type collectResult struct {
	Rows    []string      `json:"rows"`
	Scrolls int           `json:"scrolls"`
	Stopped string        `json:"stopped"`
	Area    string        `json:"area,omitempty"`
	Gaps    int           `json:"gaps,omitempty"`
	Screen  *look.Listing `json:"screen"`
}

const (
	collectRows    = 100
	collectScrolls = 30
	mostScrolls    = 200
	pageDrag       = 600 * time.Millisecond
)

func sameRows(a, b []look.Row) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Text != b[i].Text {
			return false
		}
	}
	return true
}

func wholeRows(rows []look.Row, up bool) []look.Row {
	for len(rows) > 0 {
		if edge := len(rows) - 1; !up && rows[edge].Cut {
			rows = rows[:edge]
		} else if up && rows[0].Cut {
			rows = rows[1:]
		} else {
			break
		}
	}
	return rows
}

func plural(n int, what string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", what)
	}
	return fmt.Sprintf("%d %ss", n, what)
}

func (s *Service) collect(ctx context.Context, req daemon.Request, send func(daemon.Response)) daemon.Response {
	a, err := decode[collectArgs](req)
	if err != nil {
		return fail(2, "%v", err)
	}
	direction := strings.ToLower(a.Direction)
	if direction == "" {
		direction = "down"
	}
	if direction != "down" && direction != "up" {
		return fail(2, "--direction: want down or up, not %q", a.Direction)
	}
	if a.Rows < 0 || a.MaxScrolls < 0 {
		return fail(2, "--rows and --max-scrolls take a number above 0")
	}
	if a.MaxScrolls > mostScrolls {
		return fail(2, "--max-scrolls: at most %d", mostScrolls)
	}
	up := direction == "up"
	maxRows, maxScrolls := a.Rows, a.MaxScrolls
	if maxRows == 0 {
		maxRows = collectRows
	}
	if maxScrolls == 0 {
		maxScrolls = collectScrolls
	}
	d, err := s.resolve(ctx, a.devArgs)
	if err != nil {
		return fail(2, "%v", err)
	}
	if err := s.lockOpen(d, req, send); err != nil {
		return fail(1, "%v", err)
	}
	defer d.mu.Unlock()
	l, err := s.listNow(ctx, d)
	if err != nil {
		return fail(1, "%v", err)
	}
	page, view, area := l.Rows()
	all := page
	result := collectResult{Area: area}
	still := 0
	for result.Stopped == "" {
		switch {
		case area == "":
			result.Stopped = "no-scroll-area"
			continue
		case len(wholeRows(all, up)) >= maxRows:
			result.Stopped = "rows"
			continue
		case result.Scrolls >= maxScrolls:
			result.Stopped = "max-scrolls"
			continue
		}
		from := view.T + view.H()*3/4
		to := from - view.H()*6/10
		if up {
			from = view.T + view.H()/4
			to = from + view.H()*6/10
		}
		if _, err := d.Drv.Swipe(ctx, view.CX(), from, view.CX(), to, pageDrag, driver.ActionOpts{Drag: true}); err != nil {
			return fail(1, "%v", err)
		}
		result.Scrolls++
		d.Drv.WaitSettled(ctx, 3*time.Second)
		if l, err = s.listNow(ctx, d); err != nil {
			return fail(1, "%v", err)
		}
		before := page
		page, view, _ = l.Rows()
		if sameRows(page, before) {
			if still++; still > 1 {
				result.Stopped = "end"
			}
			continue
		}
		still = 0
		joined := true
		if up {
			all, joined = look.JoinRows(page, all)
		} else {
			all, joined = look.JoinRows(all, page)
		}
		if !joined {
			result.Gaps++
		}
	}
	if result.Stopped != "end" && result.Stopped != "no-scroll-area" {
		all = wholeRows(all, up)
	}
	if len(all) > maxRows {
		result.Stopped = "rows"
		if up {
			all = all[len(all)-maxRows:]
		} else {
			all = all[:maxRows]
		}
	}
	why := map[string]string{"end": "the page stopped changing", "no-scroll-area": "no scroll area on this screen", "rows": fmt.Sprintf("reached --rows %d", maxRows),
		"max-scrolls": fmt.Sprintf("stopped at --max-scrolls %d", maxScrolls)}[result.Stopped]
	if result.Gaps > 0 {
		why += fmt.Sprintf(" (%s showed no row of the page before it: rows may be missing or repeated)", plural(result.Gaps, "scroll"))
	}
	var out strings.Builder
	rows := plural(len(all), "row")
	if area != "" {
		rows += " of " + area
	}
	fmt.Fprintf(&out, "collected %s in %s: %s\n", rows, plural(result.Scrolls, "scroll"), why)
	result.Rows = make([]string, len(all))
	for i, r := range all {
		result.Rows[i] = r.Text
		out.WriteString("  " + r.Text + "\n")
	}
	out.WriteString("screen now:\n" + l.Text())
	result.Screen = l
	resp := daemon.Response{OK: true, Text: out.String()}
	if a.JSON {
		resp.JSON, _ = json.Marshal(result)
	}
	return resp
}
