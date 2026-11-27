package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/driver"
)

type logFake struct {
	*fakeDriver
	lmu   sync.Mutex
	log   driver.AppLog
	err   error
	apps  []string
	lasts []time.Duration
}

func (f *logFake) AppLog(ctx context.Context, appID string, last time.Duration) (driver.AppLog, error) {
	f.lmu.Lock()
	defer f.lmu.Unlock()
	f.apps = append(f.apps, appID)
	f.lasts = append(f.lasts, last)
	return f.log, f.err
}

func (f *logFake) reads() int {
	f.lmu.Lock()
	defer f.lmu.Unlock()
	return len(f.apps)
}

func newLogFake(log driver.AppLog) *logFake {
	f := &logFake{fakeDriver: newFake("emulator-5554", "Pixel 7", device.Android), log: log}
	f.show(button("Sign in", 100), label("Forgot password?", 200))
	return f
}

func errorLines(n int) []string {
	var lines []string
	for i := 1; i <= n; i++ {
		lines = append(lines, fmt.Sprintf("12:00:%02d.000 E Net: request %d failed", i, i))
	}
	return lines
}

const failingStep = "- extendedWaitUntil:\n    visible: Password reset\n    timeout: 200\n"

const boom = "java.lang.IllegalStateException: boom at com.app.Main.onClick(Main.kt:12)"

func TestDoFailedStepSaysTheAppCrashed(t *testing.T) {
	s := New()
	f := newLogFake(driver.AppLog{Crash: boom, Trace: []string{"at com.app.Main.onClick(Main.kt:12)"}, Lines: errorLines(2)})
	openWith(t, s, f.info, f)
	resp, _ := call(t, s.do, map[string]any{"commands": failingStep}, "")
	if resp.Exit != 1 {
		t.Fatalf("resp %+v", resp)
	}
	want := "crashed: com.app: " + boom + "\n"
	if !strings.Contains(resp.Text, want) {
		t.Fatalf("no %q in:\n%s", want, resp.Text)
	}
	if strings.Index(resp.Text, "FAILED ") > strings.Index(resp.Text, "crashed: ") {
		t.Errorf("the crash line should follow the failed step:\n%s", resp.Text)
	}
	if len(f.apps) != 1 || f.apps[0] != "com.app" {
		t.Fatalf("read the log of %q, want one read for com.app", f.apps)
	}
	if f.lasts[0] <= 0 || f.lasts[0] > time.Minute {
		t.Errorf("window %s, want the time this do took", f.lasts[0])
	}
}

func TestDoFailedStepShowsTheLastErrorLines(t *testing.T) {
	s := New()
	lines := append(errorLines(13), "12:00:14.000 E Net: "+strings.Repeat("x", 500))
	f := newLogFake(driver.AppLog{Lines: lines})
	openWith(t, s, f.info, f)
	resp, _ := call(t, s.do, map[string]any{"commands": failingStep}, "")
	if strings.Contains(resp.Text, "crashed:") {
		t.Errorf("no crash, but:\n%s", resp.Text)
	}
	var got []string
	for _, l := range strings.Split(resp.Text, "\n") {
		if strings.HasPrefix(l, "log: ") {
			got = append(got, l)
		}
	}
	if len(got) != 10 || got[0] != "log: 12:00:05.000 E Net: request 5 failed" {
		t.Fatalf("log lines %q, want the last 10", got)
	}
	if last := got[9]; len(last) > 260 || !strings.HasSuffix(last, "…") {
		t.Errorf("long line not clipped: %d chars", len(last))
	}
}

func TestDoFailedStepJSONHasCrashAndLog(t *testing.T) {
	s := New()
	f := newLogFake(driver.AppLog{Crash: boom, Lines: errorLines(2)})
	openWith(t, s, f.info, f)
	resp, _ := call(t, s.do, map[string]any{"commands": failingStep, "json": true}, "")
	var res struct {
		Crash string   `json:"crash"`
		Log   []string `json:"log"`
	}
	if err := json.Unmarshal(resp.JSON, &res); err != nil {
		t.Fatal(err)
	}
	if res.Crash != "com.app: "+boom || len(res.Log) != 2 || res.Log[1] != "12:00:02.000 E Net: request 2 failed" {
		t.Errorf("crash %q log %q in %s", res.Crash, res.Log, resp.JSON)
	}
}

func TestDoJSONOmitsEmptyCrashAndLog(t *testing.T) {
	s := New()
	f := newLogFake(driver.AppLog{})
	openWith(t, s, f.info, f)
	resp, _ := call(t, s.do, map[string]any{"commands": failingStep, "json": true}, "")
	if f.reads() != 1 {
		t.Fatalf("%d log reads after a failed step, want 1", f.reads())
	}
	if strings.Contains(string(resp.JSON), `"crash"`) || strings.Contains(string(resp.JSON), `"log"`) {
		t.Errorf("empty crash/log not omitted: %s", resp.JSON)
	}
	if strings.Contains(resp.Text, "crashed:") || strings.Contains(resp.Text, "log: ") {
		t.Errorf("text:\n%s", resp.Text)
	}
}

func TestDoReadsNoAppLogWhenEveryStepPassed(t *testing.T) {
	s := New()
	f := newLogFake(driver.AppLog{Crash: boom, Lines: errorLines(3)})
	openWith(t, s, f.info, f)
	resp, _ := call(t, s.do, map[string]any{"commands": "- tapOn: Sign in\n- tapOn: {text: Nope, optional: true}\n", "json": true}, "")
	if !resp.OK {
		t.Fatalf("resp %+v", resp)
	}
	if f.reads() != 0 {
		t.Errorf("%d log reads on a passing do, want none", f.reads())
	}
	if strings.Contains(resp.Text, "crashed:") || strings.Contains(resp.Text, "log: ") || strings.Contains(string(resp.JSON), `"crash"`) {
		t.Errorf("text:\n%s\njson: %s", resp.Text, resp.JSON)
	}
}

func TestDoFailedStepSurvivesAnUnreadableLog(t *testing.T) {
	s := New()
	f := newLogFake(driver.AppLog{})
	f.err = errors.New("adb: device offline")
	openWith(t, s, f.info, f)
	resp, _ := call(t, s.do, map[string]any{"commands": failingStep}, "")
	if f.reads() != 1 {
		t.Fatalf("%d log reads after a failed step, want 1", f.reads())
	}
	if resp.Exit != 1 || !strings.Contains(resp.Text, "FAILED ") || strings.Contains(resp.Text, "log: ") {
		t.Errorf("resp %+v", resp)
	}
}

func TestAppLogCommand(t *testing.T) {
	s := New()
	f := newLogFake(driver.AppLog{Crash: boom, Trace: []string{"at com.app.Main.onClick(Main.kt:12)", "at android.os.Looper.loop(Looper.java:338)"},
		Lines: append(errorLines(12), "12:00:13.000 E Db: locked")})
	openWith(t, s, f.info, f)
	resp, _ := call(t, s.applog, map[string]any{"since": 30}, "")
	if !resp.OK {
		t.Fatalf("resp %+v", resp)
	}
	if len(f.apps) != 1 || f.apps[0] != "com.app" || f.lasts[0] != 30*time.Second {
		t.Errorf("read %q %v", f.apps, f.lasts)
	}
	trace := "\n  at com.app.Main.onClick(Main.kt:12)\n  at android.os.Looper.loop(Looper.java:338)\n"
	want := "crashed: com.app: " + boom + trace + "12:00:01.000 E Net: request 1 failed\n"
	if !strings.HasPrefix(resp.Text, want) || strings.Count(resp.Text, "\n") != 16 {
		t.Errorf("text:\n%s", resp.Text)
	}

	resp, _ = call(t, s.applog, map[string]any{"grep": "Db|request 3 ", "app": "com.other"}, "")
	if resp.Text != "crashed: com.other: "+boom+trace+"12:00:03.000 E Net: request 3 failed\n12:00:13.000 E Db: locked\n" {
		t.Errorf("grep text:\n%s", resp.Text)
	}
	if len(f.apps) != 2 || f.apps[1] != "com.other" || f.lasts[1] != 60*time.Second {
		t.Errorf("read %q %v", f.apps, f.lasts)
	}

	if resp, _ = call(t, s.applog, map[string]any{"grep": "("}, ""); resp.Exit != 2 {
		t.Errorf("bad regex: %+v", resp)
	}

	f.lmu.Lock()
	f.log = driver.AppLog{}
	f.lmu.Unlock()
	if resp, _ = call(t, s.applog, nil, ""); !resp.OK || resp.Text != "no error lines from com.app in the last 60s\n" {
		t.Errorf("empty: %+v", resp)
	}
}

func TestAppLogCommandWithoutSupport(t *testing.T) {
	s := New()
	f := newFake("chrome", "Chrome", device.Web)
	f.show(label("Page", 0))
	openWith(t, s, f.info, f)
	resp, _ := call(t, s.applog, nil, "")
	if resp.Exit != 1 || !strings.Contains(resp.Text, "web") {
		t.Errorf("resp %+v", resp)
	}
}
