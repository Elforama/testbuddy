package android

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
)

const logcatShellCrash = `--------- beginning of main
11-27 01:56:35.608 17144 17144 E ashmem  : Pinning is deprecated since Android Q. Please use trim or other methods.
--------- beginning of crash
11-27 01:56:37.371 17144 17144 E AndroidRuntime: FATAL EXCEPTION: main
11-27 01:56:37.371 17144 17144 E AndroidRuntime: Process: dev.testbuddy.bench, PID: 17144
11-27 01:56:37.371 17144 17144 E AndroidRuntime: android.app.RemoteServiceException$CrashedByAdbException: shell-induced crash
11-27 01:56:37.371 17144 17144 E AndroidRuntime: 	at android.app.ActivityThread.throwRemoteServiceException(ActivityThread.java:2377)
11-27 01:56:37.371 17144 17144 E AndroidRuntime: 	at android.app.ActivityThread.-$$Nest$mthrowRemoteServiceException(Unknown Source:0)
11-27 01:56:37.371 17144 17144 E AndroidRuntime: 	at android.os.Looper.loop(Looper.java:338)
11-27 01:56:37.371 17144 17144 E AndroidRuntime: 	at com.android.internal.os.ZygoteInit.main(ZygoteInit.java:932)
`

const logcatCausedBy = `--------- beginning of main
11-27 09:10:01.100  4321  4350 E Net     : request failed: timeout
11-27 09:10:02.250  4321  4321 W Choreographer: Skipped 40 frames!
--------- beginning of crash
11-27 09:10:03.500  4321  4321 E AndroidRuntime: FATAL EXCEPTION: main
11-27 09:10:03.500  4321  4321 E AndroidRuntime: Process: com.shop, PID: 4321
11-27 09:10:03.500  4321  4321 E AndroidRuntime: java.lang.RuntimeException: Unable to start activity ComponentInfo{com.shop/com.shop.CartActivity}: java.lang.IllegalStateException: cart is empty
11-27 09:10:03.500  4321  4321 E AndroidRuntime: 	at android.app.ActivityThread.performLaunchActivity(ActivityThread.java:3782)
11-27 09:10:03.500  4321  4321 E AndroidRuntime: 	at android.os.Looper.loop(Looper.java:338)
11-27 09:10:03.500  4321  4321 E AndroidRuntime: Caused by: java.lang.IllegalStateException: cart is empty
11-27 09:10:03.500  4321  4321 E AndroidRuntime: 	at com.shop.CartActivity.onCreate(CartActivity.kt:41)
11-27 09:10:03.500  4321  4321 E AndroidRuntime: 	at android.app.Activity.performCreate(Activity.java:8975)
11-27 09:10:03.500  4321  4321 E AndroidRuntime: 	... 12 more
--------- beginning of main
11-27 09:10:03.620  4321  4321 E Process : sending signal after the crash
`

const logcatNative = `--------- beginning of crash
11-27 09:20:11.040  5555  5570 F libc    : Fatal signal 11 (SIGSEGV), code 1 (SEGV_MAPERR), fault addr 0x0 in tid 5570 (RenderThread), pid 5555 (com.shop)
`

func TestParseLogcatCrash(t *testing.T) {
	got := parseLogcat(logcatShellCrash)
	want := driver.AppLog{
		Crash: "android.app.RemoteServiceException$CrashedByAdbException: shell-induced crash at android.app.ActivityThread.throwRemoteServiceException(ActivityThread.java:2377)",
		Trace: []string{
			"android.app.RemoteServiceException$CrashedByAdbException: shell-induced crash",
			"at android.app.ActivityThread.throwRemoteServiceException(ActivityThread.java:2377)",
			"at android.app.ActivityThread.-$$Nest$mthrowRemoteServiceException(Unknown Source:0)",
			"at android.os.Looper.loop(Looper.java:338)",
			"at com.android.internal.os.ZygoteInit.main(ZygoteInit.java:932)",
		},
		Lines: []string{"01:56:35.608 E ashmem: Pinning is deprecated since Android Q. Please use trim or other methods."},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseLogcat =\n%+v\nwant\n%+v", got, want)
	}
}

func TestParseLogcatNamesTheRootCauseAndItsFrame(t *testing.T) {
	got := parseLogcat(logcatCausedBy)
	if want := "java.lang.IllegalStateException: cart is empty at com.shop.CartActivity.onCreate(CartActivity.kt:41)"; got.Crash != want {
		t.Errorf("crash %q, want %q", got.Crash, want)
	}
	if len(got.Trace) != 7 || got.Trace[3] != "Caused by: java.lang.IllegalStateException: cart is empty" || got.Trace[6] != "... 12 more" {
		t.Errorf("trace %q", got.Trace)
	}
	want := []string{"09:10:01.100 E Net: request failed: timeout", "09:10:03.620 E Process: sending signal after the crash"}
	if !reflect.DeepEqual(got.Lines, want) {
		t.Errorf("lines %q, want the error lines without the warning or the trace", got.Lines)
	}
}

func TestParseLogcatNativeCrash(t *testing.T) {
	got := parseLogcat(logcatNative)
	if !strings.HasPrefix(got.Crash, "Fatal signal 11 (SIGSEGV), code 1 (SEGV_MAPERR)") || len(got.Lines) != 0 {
		t.Errorf("native crash = %+v", got)
	}
}

func TestParseLogcatNoCrash(t *testing.T) {
	got := parseLogcat("--------- beginning of main\n11-27 09:10:01.100  4321  4350 E Net     : request failed: timeout\r\n")
	if got.Crash != "" || got.Trace != nil || !reflect.DeepEqual(got.Lines, []string{"09:10:01.100 E Net: request failed: timeout"}) {
		t.Errorf("parseLogcat = %+v", got)
	}
	if got := parseLogcat(""); got.Crash != "" || got.Lines != nil {
		t.Errorf("empty = %+v", got)
	}
}

func TestAppLogReadsTheAppsErrorsOnce(t *testing.T) {
	s := stubADB(t, []stubRule{{match: "logcat", out: logcatShellCrash}})
	d := s.driver()
	var _ driver.AppLogReader = d
	log, err := d.AppLog(ctxT(t), "dev.testbuddy.bench", 2500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(log.Crash, "android.app.RemoteServiceException$CrashedByAdbException: shell-induced crash at ") || len(log.Lines) != 1 {
		t.Errorf("log = %+v", log)
	}
	calls := s.joined()
	if len(calls) != 1 {
		t.Fatalf("adb calls %q, want one", calls)
	}
	for _, want := range []string{"-s " + testSerial + " shell ", "pm list packages -U dev.testbuddy.bench", "logcat -d -v threadtime", "--uid=", "'*:E'", "-4))"} {
		if !strings.Contains(calls[0], want) {
			t.Errorf("no %q in %q", want, calls[0])
		}
	}
}

func TestAppLogRejectsABadPackage(t *testing.T) {
	s := stubADB(t, nil)
	if _, err := s.driver().AppLog(ctxT(t), "x; reboot", time.Second); err == nil {
		t.Error("no error for a package name with shell syntax")
	}
	if calls := s.joined(); len(calls) != 0 {
		t.Errorf("adb ran: %q", calls)
	}
}
