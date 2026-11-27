package android

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
)

const appLogScript = `u=$(pm list packages -U %[1]s | sed -n 's/^package:%[1]s uid:\([0-9]*\).*/\1/p' | head -n 1); ` +
	`[ -n "$u" ] || { echo '%[1]s is not installed' >&2; exit 1; }; ` +
	`logcat -d -v threadtime -t $(($(date +%%s)-%[2]d)).000 --uid=$u '*:E'`

func (d *Driver) AppLog(ctx context.Context, appID string, last time.Duration) (driver.AppLog, error) {
	if err := validPkg(appID); err != nil {
		return driver.AppLog{}, err
	}
	out, err := d.adb.Shell(ctx, fmt.Sprintf(appLogScript, appID, driver.AppLogSeconds(last)))
	if err != nil {
		return driver.AppLog{}, err
	}
	return parseLogcat(out), nil
}

var logcatLine = regexp.MustCompile(`^\d\d-\d\d (\d\d:\d\d:\d\d\.\d+)\s+(\d+)\s+\d+ ([A-Z]) (.*?)\s*: (.*)$`)

func parseLogcat(out string) driver.AppLog {
	var log driver.AppLog
	crashPid, cause, frame := "", "", ""
	for _, raw := range strings.Split(out, "\n") {
		m := logcatLine.FindStringSubmatch(strings.TrimRight(raw, "\r"))
		if m == nil || (m[3] != "E" && m[3] != "F") {
			continue
		}
		at, pid, level, tag, msg := m[1], m[2], m[3], m[4], m[5]
		if tag == "AndroidRuntime" {
			text := strings.TrimSpace(msg)
			switch {
			case strings.HasPrefix(text, "FATAL EXCEPTION"):
				crashPid, cause, frame, log.Trace = pid, "", "", nil
				continue
			case pid != crashPid:
			case strings.HasPrefix(text, "Process: "):
				continue
			default:
				log.Trace = append(log.Trace, text)
				if by, ok := strings.CutPrefix(text, "Caused by: "); ok {
					cause, frame = by, ""
				} else if cause == "" {
					cause = text
				} else if frame == "" && strings.HasPrefix(text, "at ") {
					frame = text
				}
				log.Crash = strings.TrimSpace(cause + " " + frame)
				continue
			}
		}
		if tag == "libc" && strings.HasPrefix(msg, "Fatal signal") {
			crashPid, log.Crash, log.Trace = "", msg, nil
			continue
		}
		log.Lines = append(log.Lines, fmt.Sprintf("%s %s %s: %s", at, level, tag, msg))
	}
	return log
}
