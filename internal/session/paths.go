package session

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/elforama/testbuddy/internal/daemon"
	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/look"
)

var pathName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

var escapedPunct = regexp.MustCompile(`\\([^A-Za-z0-9])`)

func sessionApp(ctx context.Context, d *Dev) string {
	if d.AppID != "" {
		return d.AppID
	}
	fg, _ := d.Drv.Foreground(ctx)
	return fg
}

func appDir(app string) string {
	if device.IsURL(app) {
		if u, err := url.Parse(app); err == nil && u.Host != "" {
			app = u.Host
		}
	}
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			return r
		}
		return '_'
	}, app)
}

func pathFile(cwd, app, name string) (string, error) {
	if !pathName.MatchString(name) {
		return "", fmt.Errorf("a path name is letters, digits, - and _ (checkout, edit-profile), not %q: it becomes paths/<app>/<name>.yaml", name)
	}
	dir := strings.Trim(appDir(app), ".")
	if dir == "" {
		return "", fmt.Errorf("no app to keep the path under: open the session with --app")
	}
	return filepath.Join(cwd, "paths", dir, name+".yaml"), nil
}

func pathNames(cwd, app string) []string {
	file, err := pathFile(cwd, app, "x")
	if err != nil {
		return nil
	}
	found, _ := filepath.Glob(filepath.Join(filepath.Dir(file), "*.yaml"))
	var names []string
	for _, f := range found {
		if name := strings.TrimSuffix(filepath.Base(f), ".yaml"); pathName.MatchString(name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func landmarkStep(l *look.Listing) (step, text string) {
	pick := func(kind string) string {
		for _, e := range l.Elements {
			if e.Kind != kind || e.Tappable || e.Covered != "" || e.Offscreen || e.Node == nil || e.Node.Editable {
				continue
			}
			if t := e.Node.Text; t != "" && t == e.Label {
				return t
			}
		}
		return ""
	}
	text = pick("Heading")
	if text == "" {
		text = pick("Text")
	}
	if text == "" {
		return "", ""
	}
	return "- assertVisible: " + strconv.Quote(regexp.QuoteMeta(text)), text
}

func pathLandmark(file string) string {
	f, err := flow.ParseFile(file)
	if err != nil || len(f.Commands) == 0 {
		return ""
	}
	last := f.Commands[len(f.Commands)-1]
	if last.Name != "assertVisible" || last.Selector == nil {
		return ""
	}
	if last.Selector.Text != "" {
		return strconv.Quote(escapedPunct.ReplaceAllString(last.Selector.Text, "$1"))
	}
	if last.Selector.ID != "" {
		return "#" + last.Selector.ID
	}
	return ""
}

func rebasePathSteps(steps []string, cwd, dir string) []string {
	out := append([]string(nil), steps...)
	for i, st := range out {
		rel, ok := strings.CutPrefix(st, "- runFlow: paths/")
		if !ok || strings.ContainsAny(rel, "\n ") {
			continue
		}
		if to, err := filepath.Rel(dir, filepath.Join(cwd, "paths", filepath.FromSlash(rel))); err == nil {
			out[i] = "- runFlow: " + filepath.ToSlash(to)
		}
	}
	return out
}

func (s *Service) arrivalStep(ctx context.Context, d *Dev) (step, text string) {
	scr, err := readNow(ctx, d)
	if err != nil {
		return "", ""
	}
	return landmarkStep(look.Build(scr, look.NewRefs()))
}

func savedPath(name, file, landmark string, steps int, replaced bool, warnings []recordingWarning) daemon.Response {
	var out strings.Builder
	if replaced {
		fmt.Fprintf(&out, "replaced path %s (%d commands) in %s\n", name, steps, file)
	} else {
		fmt.Fprintf(&out, "saved path %s (%d commands) to %s\n", name, steps, file)
	}
	if landmark != "" {
		fmt.Fprintf(&out, "landmark: %q is the last step (assertVisible): edit that line if another text tells this screen apart better\n", landmark)
	} else {
		out.WriteString("no landmark: this screen shows no heading or text. Add an assertVisible of something only this screen has as the last step\n")
	}
	for _, w := range warnings {
		prefix := "warning: "
		if w.Step != 0 {
			prefix = fmt.Sprintf("warning (step %d): ", w.Step)
		}
		out.WriteString(prefix + w.Message + "\n")
	}
	fmt.Fprintf(&out, "get back here in any session on this app: tb go %s\n", name)
	return daemon.Response{OK: true, Text: out.String()}
}

func (s *Service) pathsLine(ctx context.Context, d *Dev, cwd string) string {
	if !d.mu.TryLock() {
		return ""
	}
	app := sessionApp(ctx, d)
	d.mu.Unlock()
	names := pathNames(cwd, app)
	if len(names) == 0 {
		return ""
	}
	return fmt.Sprintf("paths: %s (tb go <name>)\n", strings.Join(names, ", "))
}

func (s *Service) goPath(ctx context.Context, req daemon.Request, send func(daemon.Response)) daemon.Response {
	a, err := decode[struct {
		devArgs
		Name string `json:"name"`
	}](req)
	if err != nil {
		return fail(2, "%v", err)
	}
	d, err := s.resolve(ctx, a.devArgs)
	if err != nil {
		return fail(2, "%v", err)
	}
	if err := s.lockOpen(d, req, send); err != nil {
		return fail(1, "%v", err)
	}
	app := sessionApp(ctx, d)
	d.mu.Unlock()
	names := pathNames(req.Cwd, app)
	if a.Name == "" {
		if len(names) == 0 {
			return daemon.Response{OK: true, Text: fmt.Sprintf("no paths saved for %s here: save one with tb save --path <name>\n", app)}
		}
		var out strings.Builder
		for _, n := range names {
			file, _ := pathFile(req.Cwd, app, n)
			fmt.Fprintf(&out, "%s -> %s\n", n, pathLandmark(file))
		}
		return daemon.Response{OK: true, Text: out.String()}
	}
	known := false
	for _, n := range names {
		known = known || n == a.Name
	}
	if !known {
		if len(names) == 0 {
			return fail(2, "no path %q for %s: none saved here (tb save --path %s)", a.Name, app, a.Name)
		}
		return fail(2, "no path %q for %s: have %s", a.Name, app, strings.Join(names, ", "))
	}
	file, _ := pathFile(req.Cwd, app, a.Name)
	rel, _ := filepath.Rel(req.Cwd, file)
	args := map[string]any{}
	json.Unmarshal(req.Args, &args)
	delete(args, "name")
	args["commands"] = "- runFlow: " + filepath.ToSlash(rel) + "\n"
	req.Args, _ = json.Marshal(args)
	resp := s.do(ctx, req, send)
	switch resp.Exit {
	case 0:
		if lm := pathLandmark(file); lm != "" {
			resp.Text = "arrived at " + lm + "\n" + resp.Text
		} else {
			resp.Text = "arrived (the path ends without an assertVisible)\n" + resp.Text
		}
	case 1:
		if !strings.HasSuffix(resp.Text, "\n") {
			resp.Text += "\n"
		}
		resp.Text += fmt.Sprintf("path %s: the steps before the failure ran. Go on from the screen shown; if the app changed, save the path again (tb save --path %s)\n", a.Name, a.Name)
	}
	return resp
}
