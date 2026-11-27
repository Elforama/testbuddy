package ios

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
)

func (d *Driver) AppLog(ctx context.Context, appID string, last time.Duration) (driver.AppLog, error) {
	if err := validID(appID); err != nil {
		return driver.AppLog{}, err
	}
	apps, err := d.simctl(ctx, "listapps", d.info.Serial)
	if err != nil {
		return driver.AppLog{}, err
	}
	process := executableOf(apps, appID)
	if process == "" {
		return driver.AppLog{}, fmt.Errorf("%s is not installed on %s", appID, d.info.Name)
	}
	predicate := fmt.Sprintf(`(process == "%s" AND messageType >= 16) OR `+
		`(process == "SpringBoard" AND eventMessage CONTAINS "[app<%s>:" AND eventMessage CONTAINS "Process exited")`, process, appID)
	out, err := d.simctl(ctx, "spawn", d.info.Serial, "log", "show", "--last", fmt.Sprintf("%ds", driver.AppLogSeconds(last)),
		"--style", "compact", "--predicate", predicate)
	if err != nil {
		return driver.AppLog{}, err
	}
	return parseLogShow(out, appID), nil
}

func executableOf(listApps, appID string) string {
	block := regexp.MustCompile(`(?m)^\s*"?` + regexp.QuoteMeta(appID) + `"?\s*=\s*\{[^}]*?\bCFBundleExecutable = "?([^";\\]+)"?;`)
	if m := block.FindStringSubmatch(listApps); m != nil {
		return m[1]
	}
	return ""
}

var (
	logShowLine  = regexp.MustCompile(`^\d{4}-\d\d-\d\d (\d\d:\d\d:\d\d\.\d+) (\S+)\s+(.+?)\[\d+:[0-9a-f]+\] (.*)$`)
	exitedSignal = regexp.MustCompile(`Process exited: .*domain:signal\(\d+\) code:(\w+)`)
)

func parseLogShow(out, appID string) driver.AppLog {
	var log driver.AppLog
	for _, raw := range strings.Split(out, "\n") {
		m := logShowLine.FindStringSubmatch(strings.TrimRight(raw, "\r"))
		if m == nil {
			continue
		}
		at, level, process, msg := m[1], m[2], m[3], m[4]
		if process == "SpringBoard" && strings.Contains(msg, "[app<"+appID+">:") {
			if sig := exitedSignal.FindStringSubmatch(msg); sig != nil {
				log.Crash = "exited on signal " + sig[1]
			}
			continue
		}
		if level == "E" || level == "F" {
			log.Lines = append(log.Lines, fmt.Sprintf("%s %s %s: %s", at, level, process, msg))
		}
	}
	return log
}
