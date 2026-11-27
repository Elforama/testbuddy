package session

import (
	"context"
	"encoding/json"
	"fmt"
	_ "image/jpeg"
	"os"
	"path/filepath"
	"strings"

	"github.com/elforama/testbuddy/internal/daemon"
	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/lint"
)

func (s *Service) log(ctx context.Context, req daemon.Request, send func(daemon.Response)) daemon.Response {
	a, _ := decode[devArgs](req)
	d, err := s.resolve(ctx, a)
	if err != nil {
		return fail(2, "%v", err)
	}
	d.stepsMu.Lock()
	text := strings.Join(d.steps, "\n")
	n := len(d.steps)
	d.stepsMu.Unlock()
	if n == 0 {
		return daemon.Response{OK: true, Text: "no commands recorded yet\n"}
	}
	return daemon.Response{OK: true, Text: text + "\n"}
}

type saveArgs struct {
	devArgs
	Path    string `json:"path"`
	Name    string `json:"name"`
	App     string `json:"app"`
	Preview bool   `json:"preview"`
}

func (s *Service) save(ctx context.Context, req daemon.Request, send func(daemon.Response)) daemon.Response {
	a, err := decode[saveArgs](req)
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
	defer d.mu.Unlock()
	app := a.App
	if app == "" {
		app = d.AppID
	}
	if app == "" {
		if fg, err := d.Drv.Foreground(ctx); err == nil {
			app = fg
		}
	}
	path := a.Path
	if a.Name != "" {
		if path, err = pathFile(req.Cwd, app, a.Name); err != nil {
			return fail(2, "%v", err)
		}
	}
	if path == "" {
		if !a.Preview {
			return fail(2, "a path is required to save a flow")
		}
		path = "preview.yaml"
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(req.Cwd, path)
	}
	header := "appId: " + app
	if d.Info.Platform == device.Web {
		if !device.IsURL(app) {
			return fail(2, "set the web flow's URL with --app before saving")
		}
		quoted, _ := json.Marshal(app)
		header = "url: " + string(quoted)
	}
	steps := rebasePathSteps(d.steps, req.Cwd, filepath.Dir(path))
	landmark := ""
	if a.Name != "" {
		var arrival string
		if arrival, landmark = s.arrivalStep(ctx, d); arrival != "" {
			steps = append(steps, arrival)
		}
	}
	body := fmt.Sprintf("%s\n---\n%s\n", header, strings.Join(steps, "\n"))
	needs := d.replayNeeds(path)
	if a.Preview {
		return previewRecording(path, body, len(steps), needs)
	}
	_, statErr := os.Stat(path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fail(1, "%v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return fail(1, "%v", err)
	}
	if a.Name != "" {
		return savedPath(a.Name, path, landmark, len(steps), statErr == nil, recordingWarnings(path, body))
	}
	text := fmt.Sprintf("saved %d commands to %s\n", len(steps), path)
	if needs != "" {
		text += needs + "\n"
	}
	return daemon.Response{OK: true, Text: text}
}

type recordingWarning struct {
	Code    string `json:"code"`
	Step    int    `json:"step,omitempty"`
	Message string `json:"message"`
}

func recordingWarnings(path, body string) []recordingWarning {
	warnings := []recordingWarning{}
	f, err := flow.ParseBytes(path, []byte(body))
	if err != nil {
		warnings = append(warnings, recordingWarning{Code: "invalid_flow", Message: err.Error()})
	} else {
		if f.FreshLaunch() == nil {
			warnings = append(warnings, recordingWarning{Code: "missing_fresh_launch",
				Message: "Add a required launchApp step at the start before replaying."})
		}
		for _, w := range lint.Flow(f) {
			warnings = append(warnings, recordingWarning{w.Code, w.Step, w.Message})
		}
	}
	return warnings
}

func previewRecording(path, body string, steps int, needs string) daemon.Response {
	warnings := []recordingWarning{}
	if needs != "" {
		warnings = append(warnings, recordingWarning{Code: "secret_variables", Message: needs})
	}
	warnings = append(warnings, recordingWarnings(path, body)...)
	var out strings.Builder
	for _, w := range warnings {
		prefix := "# Warning: "
		if w.Step != 0 {
			prefix = fmt.Sprintf("# Warning (step %d): ", w.Step)
		}
		out.WriteString(prefix + strings.ReplaceAll(w.Message, "\n", "\n# ") + "\n")
	}
	out.WriteString(body)
	data, _ := json.Marshal(struct {
		YAML     string             `json:"yaml"`
		Steps    int                `json:"steps"`
		Warnings []recordingWarning `json:"warnings"`
	}{body, steps, warnings})
	return daemon.Response{OK: true, Text: out.String(), JSON: data}
}

func (s *Service) clear(ctx context.Context, req daemon.Request, send func(daemon.Response)) daemon.Response {
	a, _ := decode[devArgs](req)
	d, err := s.resolve(ctx, a)
	if err != nil {
		return fail(2, "%v", err)
	}
	if err := s.lockOpen(d, req, send); err != nil {
		return fail(1, "%v", err)
	}
	defer d.mu.Unlock()
	d.stepsMu.Lock()
	d.steps = nil
	d.stepsMu.Unlock()
	return daemon.Response{OK: true, Text: "recording cleared\n"}
}
