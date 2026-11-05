package device

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"
)

// Info.String names the platform, serial, name and kind of device.
func TestInfoString(t *testing.T) {
	cases := []struct {
		info Info
		want string
	}{
		{Info{Platform: Android, Serial: "emulator-5554", Name: "Pixel 7", Emulator: true}, `android emulator-5554 "Pixel 7" (emulator)`},
		{Info{Platform: Android, Serial: "R58M", Name: "Galaxy"}, `android R58M "Galaxy" (device)`},
		{Info{Platform: IOS, Serial: "UDID", Name: "iPhone 17", Emulator: true}, `ios UDID "iPhone 17" (simulator)`},
		{Info{Platform: IOS, Serial: "UDID", Name: "Real iPhone"}, `ios UDID "Real iPhone" (device)`},
		{WebDevice(1), `web web "Chrome" (browser)`},
		{WebDevice(3), `web web-3 "Chrome 3" (browser)`},
	}
	for _, c := range cases {
		if got := c.info.String(); got != c.want {
			t.Errorf("String() = %s, want %s", got, c.want)
		}
	}
}

// WebDevice numbers browsers: the first is "web"/"Chrome", the n-th "web-n"/"Chrome n".
func TestWebDevice(t *testing.T) {
	cases := []struct {
		n            int
		serial, name string
	}{{0, "web", "Chrome"}, {1, "web", "Chrome"}, {-3, "web", "Chrome"}, {2, "web-2", "Chrome 2"}, {8, "web-8", "Chrome 8"}}
	for _, c := range cases {
		d := WebDevice(c.n)
		if d.Serial != c.serial || d.Name != c.name || d.Platform != Web || d.State != "available" || d.Emulator {
			t.Errorf("WebDevice(%d) = %+v", c.n, d)
		}
	}
}

// IsURL recognizes the schemes a web flow's url: may use.
func TestIsURL(t *testing.T) {
	for s, want := range map[string]bool{
		"https://example.com":    true,
		"http://localhost:3000":  true,
		"file:///tmp/index.html": true,
		"about:blank":            true,
		"data:text/html,<p>hi":   true,
		"com.example.app":        false,
		"":                       false,
		"HTTPS://EXAMPLE.COM":    false, // schemes are matched as written
		"ftp://example.com":      false,
		"example.com":            false,
		"${URL}":                 false,
		" https://x":             false,
	} {
		if got := IsURL(s); got != want {
			t.Errorf("IsURL(%q) = %v, want %v", s, got, want)
		}
	}
}

var (
	pixel  = Info{Platform: Android, Serial: "emulator-5554", Name: "Pixel 7", Emulator: true, State: "device"}
	galaxy = Info{Platform: Android, Serial: "R58M", Name: "Galaxy S22", State: "device"}
	iphone = Info{Platform: IOS, Serial: "UDID-1", Name: "iPhone 17", Emulator: true, State: "booted"}
)

// Pick finds a device by serial or case-insensitive name, filtered by platform, else the
// first candidate; errors say what to do.
func TestPick(t *testing.T) {
	all := []Info{pixel, galaxy, iphone}
	cases := []struct {
		name     string
		devs     []Info
		platform Platform
		want     string
		got      string // serial; "" = error
		err      string
	}{
		{"first device", all, "", "", "emulator-5554", ""},
		{"first of platform", all, IOS, "", "UDID-1", ""},
		{"by serial", all, "", "R58M", "R58M", ""},
		{"by udid", all, "", "UDID-1", "UDID-1", ""},
		{"by name", all, "", "Galaxy S22", "R58M", ""},
		{"by name, any case", all, "", "IPHONE 17", "UDID-1", ""},
		{"platform filters names", all, Android, "iPhone 17", "", `no connected device matches "iPhone 17" (see ` + "`tb devices`)"},
		{"no match", all, "", "Pixel 9", "", `no connected device matches "Pixel 9" (see ` + "`tb devices`)"},
		{"none of platform", []Info{pixel}, IOS, "", "", "no ios device is connected: start an emulator or simulator first"},
		{"none at all", nil, "", "", "", "no device is connected: start an Android emulator or boot an iOS simulator first"},
		{"want with none", nil, "", "x", "", `no connected device matches "x" (see ` + "`tb devices`)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d, err := Pick(c.devs, c.platform, c.want)
			if c.err != "" {
				if err == nil || err.Error() != c.err {
					t.Errorf("err = %v, want %q", err, c.err)
				}
				return
			}
			if err != nil || d.Serial != c.got {
				t.Errorf("Pick = %v, %v; want %s", d.Serial, err, c.got)
			}
		})
	}
}

// ADB prefers ANDROID_HOME, then ANDROID_SDK_ROOT, then PATH, then the default SDK folder.
func TestADBPath(t *testing.T) {
	mk := func(root string) string {
		p := filepath.Join(root, "platform-tools", "adb")
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755)
		return p
	}
	t.Run("ANDROID_HOME", func(t *testing.T) {
		home, root := t.TempDir(), t.TempDir()
		want := mk(home)
		mk(root)
		t.Setenv("ANDROID_HOME", home)
		t.Setenv("ANDROID_SDK_ROOT", root)
		if got := ADB(); got != want {
			t.Errorf("ADB() = %s, want %s", got, want)
		}
	})
	t.Run("ANDROID_SDK_ROOT when ANDROID_HOME has no adb", func(t *testing.T) {
		root := t.TempDir()
		want := mk(root)
		t.Setenv("ANDROID_HOME", t.TempDir())
		t.Setenv("ANDROID_SDK_ROOT", root)
		if got := ADB(); got != want {
			t.Errorf("ADB() = %s, want %s", got, want)
		}
	})
	t.Run("PATH", func(t *testing.T) {
		bin := t.TempDir()
		want := filepath.Join(bin, "adb")
		os.WriteFile(want, []byte("#!/bin/sh\n"), 0o755)
		t.Setenv("ANDROID_HOME", "")
		t.Setenv("ANDROID_SDK_ROOT", "")
		t.Setenv("PATH", bin)
		if got := ADB(); got != want {
			t.Errorf("ADB() = %s, want %s", got, want)
		}
	})
	t.Run("default SDK folder", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("ANDROID_HOME", "")
		t.Setenv("ANDROID_SDK_ROOT", "")
		t.Setenv("HOME", home)
		want := filepath.Join(home, "Android", "Sdk", "platform-tools", "adb")
		if runtime.GOOS == "darwin" {
			want = filepath.Join(home, "Library", "Android", "sdk", "platform-tools", "adb")
		}
		if got := ADB(); got != want {
			t.Errorf("ADB() = %s, want %s", got, want)
		}
	})
}

const adbDevicesL = `List of devices attached
* daemon not running; starting now at tcp:5037
* daemon started successfully
emulator-5554          device product:sdk_gphone64_arm64 model:sdk_gphone64_arm64 device:emu64a transport_id:1
R58M123ABC             device usb:1-1 product:b0qxxx model:SM_S901B device:b0q transport_id:2
0123456789ABCDEF       unauthorized usb:1-2 transport_id:3
emulator-5556          offline transport_id:4
192.168.1.20:5555      device product:lynx device:lynx transport_id:5
lonely

`

// `adb devices -l` is parsed into ready devices only, with names from model (underscores
// as spaces) or else device, and emulators recognized by serial.
func TestListAndroid(t *testing.T) {
	log := fakeADB(t, heredoc(adbDevicesL))
	got := listAndroid(context.Background())
	want := []Info{
		{Platform: Android, Serial: "emulator-5554", Name: "sdk gphone64 arm64", Model: "sdk_gphone64_arm64", Emulator: true, State: "device"},
		{Platform: Android, Serial: "R58M123ABC", Name: "SM S901B", Model: "SM_S901B", State: "device"},
		{Platform: Android, Serial: "192.168.1.20:5555", Name: "lynx", State: "device"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("listAndroid =\n%+v\nwant\n%+v", got, want)
	}
	if c := calls(t, log); !reflect.DeepEqual(c, []string{"devices -l"}) {
		t.Errorf("adb calls %q", c)
	}
}

// A device: field before model: still yields the model name.
func TestListAndroidModelWins(t *testing.T) {
	fakeADB(t, heredoc("List of devices attached\nSERIAL device device:first model:Second_Name\n"))
	got := listAndroid(context.Background())
	if len(got) != 1 || got[0].Name != "Second Name" || got[0].Model != "Second_Name" {
		t.Errorf("got %+v", got)
	}
}

// No devices, or adb failing or missing, yields none.
func TestListAndroidNone(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		fakeADB(t, heredoc("List of devices attached\n"))
		if got := listAndroid(context.Background()); got != nil {
			t.Errorf("got %+v", got)
		}
	})
	t.Run("adb fails", func(t *testing.T) {
		fakeADB(t, heredoc(adbDevicesL)+"\nexit 1")
		if got := listAndroid(context.Background()); got != nil {
			t.Errorf("got %+v", got)
		}
	})
	t.Run("adb missing", func(t *testing.T) {
		// TestMain's environment: no adb anywhere
		if got := listAndroid(context.Background()); got != nil {
			t.Errorf("got %+v", got)
		}
	})
}

const simctlJSON = `{
  "devices" : {
    "com.apple.CoreSimulator.SimRuntime.iOS-26-0" : [
      {"udid" : "BBBB", "name" : "iPhone 17", "state" : "Booted", "deviceTypeIdentifier" : "com.apple.CoreSimulator.SimDeviceType.iPhone-17"},
      {"udid" : "CCCC", "name" : "iPad Air", "state" : "Shutdown", "deviceTypeIdentifier" : "com.apple.CoreSimulator.SimDeviceType.iPad-Air"}
    ],
    "com.apple.CoreSimulator.SimRuntime.iOS-18-0" : [
      {"udid" : "AAAA", "name" : "iPhone 16", "state" : "Booted", "deviceTypeIdentifier" : "com.apple.CoreSimulator.SimDeviceType.iPhone-16"}
    ],
    "com.apple.CoreSimulator.SimRuntime.watchOS-11-0" : []
  }
}`

// simctl's JSON is parsed into booted simulators, runtimes in sorted order, models
// without Apple's prefix.
func TestListIOS(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("iOS simulators are listed on macOS only")
	}
	log := fakeXcrun(t, heredoc(simctlJSON))
	got := listIOS(context.Background())
	want := []Info{
		{Platform: IOS, Serial: "AAAA", Name: "iPhone 16", Model: "iPhone-16", Emulator: true, State: "booted"},
		{Platform: IOS, Serial: "BBBB", Name: "iPhone 17", Model: "iPhone-17", Emulator: true, State: "booted"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("listIOS =\n%+v\nwant\n%+v", got, want)
	}
	if c := calls(t, log); !reflect.DeepEqual(c, []string{"simctl list devices booted -j"}) {
		t.Errorf("xcrun calls %q", c)
	}
}

// In CI, listing the simulators may take longer: on a busy GitHub Actions runner simctl took
// over 10 s right after a run, and tb found no simulator ("no connected device matches",
// bench/flows/scenarios on iOS). Outside CI the usual limit stands.
func TestListIOSWaitsLongerInCI(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("iOS simulators are listed on macOS only")
	}
	defer func(l, c time.Duration) { listTimeout, ciListTimeout = l, c }(listTimeout, ciListTimeout)
	listTimeout, ciListTimeout = 300*time.Millisecond, 3*time.Second
	for _, inCI := range []bool{true, false} {
		t.Run(map[bool]string{true: "CI", false: "not CI"}[inCI], func(t *testing.T) {
			t.Setenv("TB_CI", map[bool]string{true: "1", false: "0"}[inCI])
			fakeXcrun(t, "/bin/sleep 1\n"+heredoc(simctlJSON)) // slower than the usual limit
			if got := listIOS(context.Background()); (len(got) == 2) != inCI {
				t.Errorf("in CI %v: %d simulators", inCI, len(got))
			}
		})
	}
}

// Bad JSON, a failing xcrun, or no xcrun yields no simulators.
func TestListIOSNone(t *testing.T) {
	if runtime.GOOS != "darwin" {
		if got := listIOS(context.Background()); got != nil {
			t.Errorf("non-macOS: got %+v", got)
		}
		return
	}
	t.Run("bad json", func(t *testing.T) {
		fakeXcrun(t, heredoc("not json"))
		if got := listIOS(context.Background()); got != nil {
			t.Errorf("got %+v", got)
		}
	})
	t.Run("xcrun fails", func(t *testing.T) {
		fakeXcrun(t, heredoc(simctlJSON)+"\nexit 3")
		if got := listIOS(context.Background()); got != nil {
			t.Errorf("got %+v", got)
		}
	})
	t.Run("xcrun missing", func(t *testing.T) {
		if got := listIOS(context.Background()); got != nil {
			t.Errorf("got %+v", got)
		}
	})
}

// List gives Android devices, then iOS simulators, then a browser when one is available.
func TestList(t *testing.T) {
	fakeADB(t, heredoc("List of devices attached\nemulator-5554 device model:Pixel_7\n"))
	fakeXcrun(t, heredoc(simctlJSON))
	old := WebAvailable
	defer func() { WebAvailable = old }()

	WebAvailable = func() bool { return true }
	var serials []string
	for _, d := range List(context.Background()) {
		serials = append(serials, d.Serial)
	}
	want := []string{"emulator-5554"}
	if runtime.GOOS == "darwin" {
		want = append(want, "AAAA", "BBBB")
	}
	want = append(want, "web")
	if !reflect.DeepEqual(serials, want) {
		t.Errorf("List serials %v, want %v", serials, want)
	}

	WebAvailable = func() bool { return false }
	if got := List(context.Background()); len(got) != len(want)-1 {
		t.Errorf("without a browser: %d devices, want %d", len(got), len(want)-1)
	}
}

// By default (no web driver linked in) no browser is offered.
func TestWebAvailableDefault(t *testing.T) {
	if WebAvailable() {
		t.Error("WebAvailable should default to false")
	}
}

// AppInstalled: a browser "has" any URL and nothing else; a phone never has a URL (and is not asked).
func TestAppInstalledWithoutTools(t *testing.T) {
	log := fakeADB(t, "exit 1")
	ctx := context.Background()
	web := WebDevice(1)
	cases := []struct {
		name string
		d    Info
		app  string
		want bool
	}{
		{"browser and url", web, "https://example.com", true},
		{"browser and package", web, "com.example.app", false},
		{"android and url", pixel, "https://example.com", false},
		{"ios and url", iphone, "about:blank", false},
		{"unknown platform", Info{Platform: "symbian", Serial: "x"}, "com.example.app", true},
	}
	for _, c := range cases {
		if got := AppInstalled(ctx, c.d, c.app); got != c.want {
			t.Errorf("%s: AppInstalled = %v, want %v", c.name, got, c.want)
		}
	}
	if c := calls(t, log); c != nil {
		t.Errorf("adb was called: %q", c)
	}
}

// On Android, `pm path` decides: "package:" means installed, nothing means not; a failure
// with output, or a cancelled check, counts as installed (never hide a device on doubt).
func TestAppInstalledAndroid(t *testing.T) {
	log := fakeADB(t, `case "$*" in
  "-s emulator-5554 shell pm path com.yes") echo "package:/data/app/~~x/com.yes/base.apk" ;;
  "-s emulator-5554 shell pm path com.none") exit 0 ;;
  "-s emulator-5554 shell pm path com.err") echo "Error: no such package" 1>&2; exit 1 ;;
  "-s emulator-5554 shell pm path com.errout") echo "something odd"; exit 1 ;;
  *) exit 9 ;;
esac`)
	ctx := context.Background()
	for app, want := range map[string]bool{"com.yes": true, "com.none": false, "com.err": false, "com.errout": true} {
		if got := AppInstalled(ctx, pixel, app); got != want {
			t.Errorf("%s: AppInstalled = %v, want %v", app, got, want)
		}
	}
	if n := len(calls(t, log)); n != 4 {
		t.Errorf("%d adb calls, want 4", n)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if !AppInstalled(cancelled, pixel, "com.none") {
		t.Error("a check that could not run should count as installed")
	}
}

// On iOS, simctl get_app_container decides: success means installed, a non-zero exit
// means not, and an xcrun that cannot run counts as installed.
func TestAppInstalledIOS(t *testing.T) {
	ctx := context.Background()
	t.Run("missing xcrun", func(t *testing.T) {
		if !AppInstalled(ctx, iphone, "com.yes") {
			t.Error("cannot tell: should count as installed")
		}
	})
	log := fakeXcrun(t, `case "$*" in
  "simctl get_app_container UDID-1 com.yes") echo /path/to/app ;;
  *) exit 2 ;;
esac`)
	if !AppInstalled(ctx, iphone, "com.yes") {
		t.Error("com.yes should be installed")
	}
	if AppInstalled(ctx, iphone, "com.no") {
		t.Error("com.no should not be installed")
	}
	want := []string{"simctl get_app_container UDID-1 com.yes", "simctl get_app_container UDID-1 com.no"}
	if c := calls(t, log); !reflect.DeepEqual(c, want) {
		t.Errorf("xcrun calls %q", c)
	}
}
