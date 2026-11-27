package android

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
)

const appLogScript = `u=$(pm list packages -U %[1]s | sed -n 's/^package:%[1]s uid:\([0-9]*\).*/\1/p' | head -n 1); ` +
	`[ -n "$u" ] || exit 0; date '+%%s.%%N %%z'; ` +
	`logcat -d -v epoch -t $(($(date +%%s)-%[2]d)).000 --uid=$u '*:E'`

func (d *Driver) AppLog(ctx context.Context, appID string, last time.Duration) (driver.AppLog, error) {
	if err := validPkg(appID); err != nil {
		return driver.AppLog{}, err
	}
	out, err := d.adb.Shell(ctx, fmt.Sprintf(appLogScript, appID, driver.AppLogSeconds(last)))
	if err != nil {
		return driver.AppLog{}, err
	}
	log, ok := parseLogcat(out, last)
	if !ok {
		return driver.AppLog{}, fmt.Errorf("%s is not installed on %s", appID, d.info.Name)
	}
	return log, nil
}

var (
	deviceTime = regexp.MustCompile(`^(\d+)\.(\d{3})\d* ([+-]\d{4})$`)
	logcatLine = regexp.MustCompile(`^\s*(\d+)\.(\d{3})\s+(\d+)\s+\d+ ([A-Z]) (.*?)\s*: (.*)$`)
)

func stamp(sec, ms string) time.Time {
	s, _ := strconv.ParseInt(sec, 10, 64)
	m, _ := strconv.ParseInt(ms, 10, 64)
	return time.Unix(s, m*int64(time.Millisecond))
}

func parseLogcat(out string, last time.Duration) (driver.AppLog, bool) {
	var log driver.AppLog
	lines := strings.Split(out, "\n")
	head := deviceTime.FindStringSubmatch(strings.TrimSpace(lines[0]))
	if head == nil {
		return log, false
	}
	zone := time.UTC
	if z, err := time.Parse("-0700", head[3]); err == nil {
		zone = z.Location()
	}
	from := stamp(head[1], head[2]).Add(-last)
	crashPid, cause, frame := "", "", ""
	for _, raw := range lines[1:] {
		m := logcatLine.FindStringSubmatch(strings.TrimRight(raw, "\r"))
		if m == nil || (m[4] != "E" && m[4] != "F") {
			continue
		}
		at := stamp(m[1], m[2])
		if at.Before(from) {
			continue
		}
		pid, level, tag, msg := m[3], m[4], m[5], m[6]
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
		log.Lines = append(log.Lines, fmt.Sprintf("%s %s %s: %s", at.In(zone).Format("15:04:05.000"), level, tag, msg))
	}
	return log, true
}
