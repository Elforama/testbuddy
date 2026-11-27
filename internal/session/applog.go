package session

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/elforama/testbuddy/internal/daemon"
	"github.com/elforama/testbuddy/internal/driver"
)

const (
	failureLogLines = 10
	failureLogWidth = 240
	appLogLines     = 200
	appLogSince     = 60
	appLogTimeout   = 10 * time.Second
)

func failureLog(ctx context.Context, d *Dev, appID string, started time.Time) (crash string, lines []string) {
	r, ok := d.Drv.(driver.AppLogReader)
	if !ok || appID == "" {
		return "", nil
	}
	ctx, cancel := context.WithTimeout(ctx, appLogTimeout)
	defer cancel()
	log, err := r.AppLog(ctx, appID, time.Since(started))
	if err != nil {
		return "", nil
	}
	if log.Crash != "" {
		crash = clip(appID+": "+log.Crash, 2*failureLogWidth)
	}
	for _, l := range lastLines(log.Lines, failureLogLines) {
		lines = append(lines, clip(l, failureLogWidth))
	}
	return crash, lines
}

func lastLines(lines []string, n int) []string {
	if len(lines) > n {
		return lines[len(lines)-n:]
	}
	return lines
}

func clip(s string, width int) string {
	if r := []rune(s); len(r) > width {
		return string(r[:width]) + "…"
	}
	return s
}

type applogArgs struct {
	devArgs
	App   string `json:"app"`
	Since int    `json:"since"`
	Grep  string `json:"grep"`
}

func (s *Service) applog(ctx context.Context, req daemon.Request, send func(daemon.Response)) daemon.Response {
	a, err := decode[applogArgs](req)
	if err != nil {
		return fail(2, "%v", err)
	}
	var keep *regexp.Regexp
	if a.Grep != "" {
		if keep, err = regexp.Compile(a.Grep); err != nil {
			return fail(2, "--grep: %v", err)
		}
	}
	if a.Since <= 0 {
		a.Since = appLogSince
	}
	d, err := s.resolve(ctx, a.devArgs)
	if err != nil {
		return fail(2, "%v", err)
	}
	if err := s.lockOpen(d, req, send); err != nil {
		return fail(1, "%v", err)
	}
	defer d.mu.Unlock()
	r, ok := d.Drv.(driver.AppLogReader)
	if !ok {
		return fail(1, "tb applog reads the app log on Android and iOS simulators: %s has none", d.Drv.Platform())
	}
	appID := a.App
	if appID == "" {
		appID = d.AppID
	}
	if appID == "" {
		appID, _ = d.Drv.Foreground(ctx)
	}
	if appID == "" {
		return fail(1, "no app to read the log of: pass --app APP_ID")
	}
	ctx, cancel := context.WithTimeout(ctx, appLogTimeout)
	defer cancel()
	log, err := r.AppLog(ctx, appID, time.Duration(a.Since)*time.Second)
	if err != nil {
		return fail(1, "%v", err)
	}
	var lines []string
	for _, l := range log.Lines {
		if keep == nil || keep.MatchString(l) {
			lines = append(lines, l)
		}
	}
	log.Lines = lastLines(lines, appLogLines)
	text := log.Text(appID)
	if text == "" {
		text = fmt.Sprintf("no error lines from %s in the last %ds\n", appID, a.Since)
	}
	return daemon.Response{OK: true, Text: text}
}
