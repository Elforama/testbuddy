package android

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
)

const logcatShellCrash = `1795762600.342894137 +0000
--------- beginning of main
         1795762595.608 17144 17144 E ashmem  : Pinning is deprecated since Android Q. Please use trim or other methods.
--------- beginning of crash
         1795762597.371 17144 17144 E AndroidRuntime: FATAL EXCEPTION: main
         1795762597.371 17144 17144 E AndroidRuntime: Process: dev.testbuddy.bench, PID: 17144
         1795762597.371 17144 17144 E AndroidRuntime: android.app.RemoteServiceException$CrashedByAdbException: shell-induced crash
         1795762597.371 17144 17144 E AndroidRuntime: 	at android.app.ActivityThread.throwRemoteServiceException(ActivityThread.java:2377)
         1795762597.371 17144 17144 E AndroidRuntime: 	at android.app.ActivityThread.-$$Nest$mthrowRemoteServiceException(Unknown Source:0)
         1795762597.371 17144 17144 E AndroidRuntime: 	at android.os.Looper.loop(Looper.java:338)
         1795762597.371 17144 17144 E AndroidRuntime: 	at com.android.internal.os.ZygoteInit.main(ZygoteInit.java:932)
`

const logcatCausedBy = `1795762600.000000000 -0500
--------- beginning of main
         1795762591.100  4321  4350 E Net     : request failed: timeout
         1795762592.250  4321  4321 W Choreographer: Skipped 40 frames!
--------- beginning of crash
         1795762593.500  4321  4321 E AndroidRuntime: FATAL EXCEPTION: main
         1795762593.500  4321  4321 E AndroidRuntime: Process: com.shop, PID: 4321
         1795762593.500  4321  4321 E AndroidRuntime: java.lang.RuntimeException: Unable to start activity ComponentInfo{com.shop/com.shop.CartActivity}: java.lang.IllegalStateException: cart is empty
         1795762593.500  4321  4321 E AndroidRuntime: 	at android.app.ActivityThread.performLaunchActivity(ActivityThread.java:3782)
         1795762593.500  4321  4321 E AndroidRuntime: 	at android.os.Looper.loop(Looper.java:338)
         1795762593.500  4321  4321 E AndroidRuntime: Caused by: java.lang.IllegalStateException: cart is empty
         1795762593.500  4321  4321 E AndroidRuntime: 	at com.shop.CartActivity.onCreate(CartActivity.kt:41)
         1795762593.500  4321  4321 E AndroidRuntime: 	at android.app.Activity.performCreate(Activity.java:8975)
         1795762593.500  4321  4321 E AndroidRuntime: 	... 12 more
--------- beginning of main
         1795762593.620  4321  4321 E Process : sending signal after the crash
`

const logcatNative = `1795762600.000000000 +0000
--------- beginning of crash
         1795762599.040  5555  5570 F libc    : Fatal signal 11 (SIGSEGV), code 1 (SEGV_MAPERR), fault addr 0x0 in tid 5570 (RenderThread), pid 5555 (com.shop)
`

func mustParse(t *testing.T, out string, last time.Duration) driver.AppLog {
	t.Helper()
	log, ok := parseLogcat(out, last)
	if !ok {
		t.Fatalf("no device time read from %q", out)
	}
	return log
}

func TestParseLogcatCrash(t *testing.T) {
	got := mustParse(t, logcatShellCrash, 10*time.Second)
	want := driver.AppLog{
		Crash: "android.app.RemoteServiceException$CrashedByAdbException: shell-induced crash at android.app.ActivityThread.throwRemoteServiceException(ActivityThread.java:2377)",
		Trace: []string{
			"android.app.RemoteServiceException$CrashedByAdbException: shell-induced crash",
			"at android.app.ActivityThread.throwRemoteServiceException(ActivityThread.java:2377)",
			"at android.app.ActivityThread.-$$Nest$mthrowRemoteServiceException(Unknown Source:0)",
			"at android.os.Looper.loop(Looper.java:338)",
			"at com.android.internal.os.ZygoteInit.main(ZygoteInit.java:932)",
		},
		Lines: []string{"06:56:35.608 E ashmem: Pinning is deprecated since Android Q. Please use trim or other methods."},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseLogcat =\n%+v\nwant\n%+v", got, want)
	}
}

func TestParseLogcatDropsWhatCameBeforeTheWindow(t *testing.T) {
	justInside := mustParse(t, logcatShellCrash, 3*time.Second)
	if !strings.HasPrefix(justInside.Crash, "android.app.RemoteServiceException$CrashedByAdbException") || len(justInside.Trace) != 5 {
		t.Errorf("a crash 2.97 s before the read is in a 3 s window: %+v", justInside)
	}
	if len(justInside.Lines) != 0 {
		t.Errorf("a line 4.7 s before the read is not in a 3 s window: %q", justInside.Lines)
	}
	justBefore := mustParse(t, logcatShellCrash, 2900*time.Millisecond)
	if justBefore.Crash != "" || justBefore.Trace != nil || justBefore.Lines != nil {
		t.Errorf("a crash 2.97 s before the read reported for a 2.9 s window: %+v", justBefore)
	}
}

func TestParseLogcatNamesTheRootCauseAndItsFrame(t *testing.T) {
	got := mustParse(t, logcatCausedBy, 10*time.Second)
	if want := "java.lang.IllegalStateException: cart is empty at com.shop.CartActivity.onCreate(CartActivity.kt:41)"; got.Crash != want {
		t.Errorf("crash %q, want %q", got.Crash, want)
	}
	if len(got.Trace) != 7 || got.Trace[3] != "Caused by: java.lang.IllegalStateException: cart is empty" || got.Trace[6] != "... 12 more" {
		t.Errorf("trace %q", got.Trace)
	}
	want := []string{"01:56:31.100 E Net: request failed: timeout", "01:56:33.620 E Process: sending signal after the crash"}
	if !reflect.DeepEqual(got.Lines, want) {
		t.Errorf("lines %q, want the error lines in the device's time zone, without the warning or the trace", got.Lines)
	}
}

func TestParseLogcatNativeCrash(t *testing.T) {
	got := mustParse(t, logcatNative, 5*time.Second)
	if !strings.HasPrefix(got.Crash, "Fatal signal 11 (SIGSEGV), code 1 (SEGV_MAPERR)") || len(got.Lines) != 0 {
		t.Errorf("native crash = %+v", got)
	}
}

func TestParseLogcatNoCrash(t *testing.T) {
	got := mustParse(t, "1795762600.000000000 +0000\r\n--------- beginning of main\n         1795762591.100  4321  4350 E Net     : request failed: timeout\r\n", time.Minute)
	if got.Crash != "" || got.Trace != nil || !reflect.DeepEqual(got.Lines, []string{"06:56:31.100 E Net: request failed: timeout"}) {
		t.Errorf("parseLogcat = %+v", got)
	}
	if _, ok := parseLogcat("", time.Minute); ok {
		t.Error("empty output read as a log")
	}
}

func TestAppLogReadsTheAppsErrorsOnce(t *testing.T) {
	s := stubADB(t, []stubRule{{match: "logcat", out: logcatShellCrash}})
	d := s.driver()
	var _ driver.AppLogReader = d
	log, err := d.AppLog(ctxT(t), "dev.testbuddy.bench", 3500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(log.Crash, "android.app.RemoteServiceException$CrashedByAdbException: shell-induced crash at ") || len(log.Lines) != 0 {
		t.Errorf("log = %+v", log)
	}
	calls := s.joined()
	if len(calls) != 1 {
		t.Fatalf("adb calls %q, want one", calls)
	}
	for _, want := range []string{"-s " + testSerial + " shell ", "pm list packages -U dev.testbuddy.bench", "date '+%s.%N %z'", "logcat -d -v epoch", "--uid=", "'*:E'", "-5))"} {
		if !strings.Contains(calls[0], want) {
			t.Errorf("no %q in %q", want, calls[0])
		}
	}
}

func TestAppLogOfAnAppThatIsNotInstalled(t *testing.T) {
	s := stubADB(t, nil)
	_, err := s.driver().AppLog(ctxT(t), "com.none", time.Second)
	if err == nil || err.Error() != "com.none is not installed on Pixel Test" {
		t.Errorf("err = %v, want only that the app is not installed", err)
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
