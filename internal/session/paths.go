package session

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
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
	found, _ := os.ReadDir(filepath.Dir(file))
	var names []string
	for _, f := range found {
		if name, ok := strings.CutSuffix(f.Name(), ".yaml"); ok && !f.IsDir() && pathName.MatchString(name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func landmarkStep(l *look.Listing) (step, text string) {
	pick := func(kind string) *look.Element {
		for _, e := range l.Elements {
			if e.Kind != kind || e.Tappable || e.Covered != "" || e.Offscreen || e.Node == nil || e.Node.Editable {
				continue
			}
			if t := e.Node.Text; t != "" && t == e.Label {
				return e
			}
		}
		return nil
	}
	e := pick("Heading")
	if e == nil {
		e = pick("Text")
	}
	if e == nil {
		return "", ""
	}
	quoted := strconv.Quote(regexp.QuoteMeta(e.Label))
	if e.Node.ID != "" {
		return fmt.Sprintf("- assertVisible: {id: %s, text: %s}", strconv.Quote(regexp.QuoteMeta(look.ShortID(e.Node.ID))), quoted), e.Label
	}
	return "- assertVisible: " + quoted, e.Label
}

func pathLandmark(file string) string {
	f, err := flow.ParseFile(file)
	if err != nil || len(f.Commands) == 0 {
		return ""
	}
	last := f.Commands[len(f.Commands)-1]
	if last.Name != "assertVisible" || last.Selector == nil || last.Optional {
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

const (
	noLandmark     = "this screen shows no heading or text"
	secretLandmark = "the screen's heading or first text shows a secret that no ${NAME} of this session stands for"
)

func (s *Service) arrivalStep(ctx context.Context, d *Dev) (step, text, missing string) {
	scr, err := readNow(ctx, d)
	if err != nil {
		return "", "", noLandmark
	}
	step, text = landmarkStep(look.Build(scr, look.NewRefs()))
	if step == "" {
		return "", "", noLandmark
	}
	if named := replacer(d.secretNames()); named != nil {
		step = named.Replace(step)
	}
	if hidden := d.hider(); hidden != nil && hidden(step) != step {
		return "", "", secretLandmark
	}
	return step, text, ""
}

func pathSteps(steps []string, cwd string) []string {
	var files []string
	for _, st := range steps {
		if rel, ok := strings.CutPrefix(st, "- runFlow: paths/"); ok && !strings.ContainsAny(rel, "\n ") {
			files = append(files, filepath.Join(cwd, "paths", filepath.FromSlash(rel)))
		}
	}
	return files
}

func runsFlow(file, target string, seen map[string]bool) bool {
	if file == target {
		return true
	}
	if seen[file] {
		return false
	}
	seen[file] = true
	f, err := flow.ParseFile(file)
	if err != nil {
		return false
	}
	var within func(cmds []*flow.Command) bool
	within = func(cmds []*flow.Command) bool {
		for _, c := range cmds {
			if c.SubFlow != nil && runsFlow(c.SubFlow.Path, target, seen) {
				return true
			}
			if within(c.Commands) {
				return true
			}
		}
		return false
	}
	return within(f.Commands)
}

func checkPathSave(steps []string, cwd, file, name string) error {
	for _, dep := range pathSteps(steps, cwd) {
		if runsFlow(dep, file, map[string]bool{}) {
			return fmt.Errorf("path %s would run itself: this session got here with tb go %s, which runs %s. Nothing was written. To change the path edit %s; to record it anew, tb clear, then start with launchApp",
				name, strings.TrimSuffix(filepath.Base(dep), ".yaml"), file, file)
		}
	}
	if _, err := os.Stat(file); err != nil {
		return nil
	}
	first := "nothing"
	if len(steps) > 0 {
		first = strings.SplitN(steps[0], "\n", 2)[0]
		if strings.HasPrefix(first, "- launchApp") || strings.HasPrefix(first, "- runFlow: paths/") {
			return nil
		}
	}
	return fmt.Errorf("path %s exists, and this recording does not start with launchApp or another path (it starts with %s): saving it would replace the whole way there with these last steps. Nothing was written. To repair the path edit the failing step in %s; to record it anew, tb clear, then start with launchApp",
		name, first, file)
}

func savedPath(d *Dev, name, file, landmark, missing string, deps []string, steps int, replaced bool, warnings []recordingWarning) daemon.Response {
	var out strings.Builder
	if replaced {
		fmt.Fprintf(&out, "replaced path %s (%d commands) in %s\n", name, steps, file)
	} else {
		fmt.Fprintf(&out, "saved path %s (%d commands) to %s\n", name, steps, file)
	}
	if landmark != "" {
		fmt.Fprintf(&out, "landmark: %q is the last step (assertVisible): edit that line if another text tells this screen apart better\n", landmark)
	} else {
		fmt.Fprintf(&out, "no landmark: %s. Add an assertVisible of something only this screen has as the last step\n", missing)
	}
	if len(deps) > 0 {
		var names []string
		for _, dep := range deps {
			names = append(names, strings.TrimSuffix(filepath.Base(dep), ".yaml"))
		}
		fmt.Fprintf(&out, "depends on: %s\n", strings.Join(names, ", "))
	}
	for _, w := range warnings {
		prefix := "warning: "
		if w.Step != 0 {
			prefix = fmt.Sprintf("warning (step %d): ", w.Step)
		}
		out.WriteString(prefix + w.Message + "\n")
	}
	body, _ := os.ReadFile(file)
	if vars := d.secretVars(string(body)); len(vars) > 0 {
		fmt.Fprintf(&out, "Secret values are not saved. Pass them when you run it: tb go %s%s\n", name, strings.Join(vars, ""))
	}
	fmt.Fprintf(&out, "get back here in any session on this app (it restarts the app when it begins with launchApp): tb go %s\n", name)
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
			lm := pathLandmark(file)
			if lm == "" {
				lm = "(no landmark)"
			}
			fmt.Fprintf(&out, "%s -> %s\n", n, lm)
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
			resp.Text = fmt.Sprintf("ran %s: its last step is not a required assertVisible, so check the screen\n", a.Name) + resp.Text
		}
	case 1:
		if !strings.HasSuffix(resp.Text, "\n") {
			resp.Text += "\n"
		}
		resp.Text += fmt.Sprintf("path %s: the steps before the failure ran. Go on from the screen shown; if the app changed, edit the failing step in %s\n", a.Name, filepath.ToSlash(rel))
	}
	return resp
}
