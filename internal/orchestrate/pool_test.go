package orchestrate

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/flow"
)

var (
	pixel  = device.Info{Platform: device.Android, Serial: "emulator-5554", Name: "Pixel 7", State: "device"}
	galaxy = device.Info{Platform: device.Android, Serial: "R58M", Name: "Galaxy S22", State: "device"}
	iphone = device.Info{Platform: device.IOS, Serial: "UDID-1", Name: "iPhone 17", State: "booted"}
	chrome = device.WebDevice(1)
)

func TestNeedFits(t *testing.T) {
	cases := []struct {
		name string
		need Need
		dev  device.Info
		want bool
	}{
		{"empty need fits any device", Need{}, pixel, true},
		{"empty need fits a browser", Need{}, chrome, true},
		{"platform match", Need{Platform: "android"}, pixel, true},
		{"platform mismatch", Need{Platform: "ios"}, pixel, false},
		{"web platform", Need{Platform: "web"}, chrome, true},
		{"serial match", Need{Device: "emulator-5554"}, pixel, true},
		{"serial mismatch", Need{Device: "emulator-5556"}, pixel, false},
		{"name match", Need{Device: "Pixel 7"}, pixel, true},
		{"name match ignores case", Need{Device: "pIxEl 7"}, pixel, true},
		{"serial is case-sensitive", Need{Device: "EMULATOR-5554"}, pixel, false},
		{"platform and device both checked", Need{Platform: "ios", Device: "Pixel 7"}, pixel, false},
		{"app id alone does not filter", Need{AppID: "com.app"}, pixel, true},
		{"phone app fits a phone", Need{AppID: "com.app", Mobile: true}, iphone, true},
		{"phone app never fits a browser", Need{AppID: "com.app", Mobile: true}, chrome, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.need.fits(c.dev); got != c.want {
				t.Errorf("fits = %v, want %v", got, c.want)
			}
		})
	}
}

func TestPoolFitsAsksInstalled(t *testing.T) {
	p := NewPool([]device.Info{pixel})
	if !p.fits(Need{AppID: "com.app"}, pixel) {
		t.Error("nil Installed should let any device fit")
	}
	var asked []string
	p.Installed = func(serial, app string) bool {
		asked = append(asked, serial+"/"+app)
		return app == "com.yes"
	}
	if !p.fits(Need{AppID: "com.yes"}, pixel) {
		t.Error("installed app should fit")
	}
	if p.fits(Need{AppID: "com.no"}, pixel) {
		t.Error("missing app should not fit")
	}
	if !p.fits(Need{}, pixel) {
		t.Error("a need without app id should not consult Installed")
	}
	if p.fits(Need{Platform: "ios", AppID: "com.yes"}, pixel) {
		t.Error("platform is checked before the app")
	}
	want := []string{"emulator-5554/com.yes", "emulator-5554/com.no"}
	if !reflect.DeepEqual(asked, want) {
		t.Errorf("Installed calls = %v, want %v", asked, want)
	}
}

func TestPoolDevices(t *testing.T) {
	devs := []device.Info{pixel, iphone}
	if got := NewPool(devs).Devices(); !reflect.DeepEqual(got, devs) {
		t.Errorf("Devices = %v", got)
	}
}

func TestTryAllocAllOrNothing(t *testing.T) {
	p := NewPool([]device.Info{pixel})
	if got := p.tryAlloc([]Need{{Role: "a"}, {Role: "b"}}); got != nil {
		t.Fatalf("two needs, one device: got %v, want nil", got)
	}
	if len(p.busy) != 0 {
		t.Fatalf("failed alloc left devices busy: %v", p.busy)
	}
	got := p.tryAlloc([]Need{{}})
	if got[""].Serial != pixel.Serial {
		t.Fatalf("single need: got %v", got)
	}
	if !p.busy[pixel.Serial] {
		t.Error("allocated device should be busy")
	}
}

func TestTryAllocBusyAndRelease(t *testing.T) {
	p := NewPool([]device.Info{pixel, galaxy})
	a := p.tryAlloc([]Need{{}})
	b := p.tryAlloc([]Need{{}})
	if a[""].Serial == b[""].Serial {
		t.Fatalf("both jobs got %s", a[""].Serial)
	}
	if c := p.tryAlloc([]Need{{}}); c != nil {
		t.Fatalf("pool is full, got %v", c)
	}
	p.release(a)
	c := p.tryAlloc([]Need{{}})
	if c[""].Serial != a[""].Serial {
		t.Fatalf("after release got %v, want %s", c, a[""].Serial)
	}
	p.release(b)
	p.release(c)
	if len(p.busy) != 0 {
		t.Errorf("busy after releasing all: %v", p.busy)
	}
}

func TestTryAllocOrder(t *testing.T) {
	t.Run("pinned before any", func(t *testing.T) {
		p := NewPool([]device.Info{pixel, galaxy})
		got := p.tryAlloc([]Need{{Role: "any"}, {Role: "pinned", Device: "emulator-5554"}})
		if got == nil {
			t.Fatal("alloc failed: the unpinned role took the pinned device")
		}
		if got["pinned"].Serial != pixel.Serial || got["any"].Serial != galaxy.Serial {
			t.Errorf("got %v", got)
		}
	})
	t.Run("platform before any", func(t *testing.T) {
		p := NewPool([]device.Info{pixel, iphone})
		got := p.tryAlloc([]Need{{Role: "any"}, {Role: "droid", Platform: "android"}})
		if got == nil {
			t.Fatal("alloc failed: the any role took the only android device")
		}
		if got["droid"].Serial != pixel.Serial || got["any"].Serial != iphone.Serial {
			t.Errorf("got %v", got)
		}
	})
	t.Run("pinned before platform", func(t *testing.T) {
		p := NewPool([]device.Info{pixel, galaxy})
		got := p.tryAlloc([]Need{{Role: "plat", Platform: "android"}, {Role: "pin", Device: "Pixel 7"}})
		if got == nil {
			t.Fatal("alloc failed")
		}
		if got["pin"].Serial != pixel.Serial || got["plat"].Serial != galaxy.Serial {
			t.Errorf("got %v", got)
		}
	})
	t.Run("pinned device by name", func(t *testing.T) {
		p := NewPool([]device.Info{pixel, galaxy})
		got := p.tryAlloc([]Need{{Device: "galaxy s22"}})
		if got[""].Serial != galaxy.Serial {
			t.Errorf("got %v", got)
		}
	})
}

func TestTryAllocRespectsInstalled(t *testing.T) {
	p := NewPool([]device.Info{pixel, galaxy})
	p.Installed = func(serial, app string) bool { return serial == galaxy.Serial }
	got := p.tryAlloc([]Need{{AppID: "com.app"}})
	if got[""].Serial != galaxy.Serial {
		t.Fatalf("got %v, want the device with the app", got)
	}
	if again := p.tryAlloc([]Need{{AppID: "com.app"}}); again != nil {
		t.Errorf("only device with the app is busy, got %v", again)
	}
}

func TestTryAllocAddsBrowsers(t *testing.T) {
	p := NewPool([]device.Info{chrome})
	got := p.tryAlloc([]Need{{Role: "a", Platform: "web"}, {Role: "b", Platform: "web"}, {Role: "c", Platform: "web"}})
	if got == nil {
		t.Fatal("alloc failed")
	}
	var serials []string
	for _, d := range got {
		serials = append(serials, d.Serial)
	}
	sort.Strings(serials)
	if want := []string{"web", "web-2", "web-3"}; !reflect.DeepEqual(serials, want) {
		t.Errorf("serials = %v, want %v", serials, want)
	}
	if n := len(p.Devices()); n != 3 {
		t.Errorf("pool has %d devices, want 3", n)
	}
	if p.Devices()[2].Name != "Chrome 3" {
		t.Errorf("third browser name = %q", p.Devices()[2].Name)
	}
	p.release(got)
	p.tryAlloc([]Need{{Platform: "web"}, {Platform: "web"}})
	if n := len(p.Devices()); n != 3 {
		t.Errorf("after reuse pool has %d devices, want 3", n)
	}
}

func TestTryAllocBrowserCap(t *testing.T) {
	p := NewPool([]device.Info{chrome})
	var needs []Need
	for i := 0; i < 9; i++ {
		needs = append(needs, Need{Role: string(rune('a' + i)), Platform: "web"})
	}
	if got := p.tryAlloc(needs); got != nil {
		t.Errorf("9 browsers: got %d devices, want nil", len(got))
	}
	webs := 0
	for _, d := range p.Devices() {
		if d.Platform == device.Web {
			webs++
		}
	}
	if webs > 8 {
		t.Errorf("pool grew to %d browsers", webs)
	}
	if len(p.busy) != 0 {
		t.Errorf("failed alloc left busy devices: %v", p.busy)
	}
	if got := p.tryAlloc(needs[:8]); len(got) != 8 {
		t.Errorf("8 browsers: got %d", len(got))
	}
}

func TestTryAllocBrowserNeedsChromeAndNoPin(t *testing.T) {
	p := NewPool([]device.Info{pixel})
	if got := p.tryAlloc([]Need{{Platform: "web"}}); got != nil {
		t.Errorf("no browser in pool: got %v", got)
	}
	if len(p.Devices()) != 1 {
		t.Errorf("pool grew: %v", p.Devices())
	}
	p = NewPool([]device.Info{chrome})
	p.tryAlloc([]Need{{Platform: "web"}})
	if got := p.tryAlloc([]Need{{Platform: "web", Device: "web-2"}}); got != nil {
		t.Errorf("pinned browser that does not exist: got %v", got)
	}
	if len(p.Devices()) != 1 {
		t.Errorf("pinned need added a browser: %v", p.Devices())
	}
}

func TestAddWeb(t *testing.T) {
	p := NewPool(nil)
	if _, ok := p.addWeb(); ok {
		t.Error("addWeb with no browser should refuse")
	}
	p = NewPool([]device.Info{pixel, chrome})
	d, ok := p.addWeb()
	if !ok || d.Serial != "web-2" || d.Name != "Chrome 2" || d.Platform != device.Web {
		t.Errorf("addWeb = %+v, %v", d, ok)
	}
	for i := 3; i <= 8; i++ {
		if d, ok := p.addWeb(); !ok || d.Serial != "web-"+string(rune('0'+i)) {
			t.Fatalf("browser %d: %+v %v", i, d, ok)
		}
	}
	if _, ok := p.addWeb(); ok {
		t.Error("a ninth browser should be refused")
	}
}

func TestSatisfiable(t *testing.T) {
	cases := []struct {
		name  string
		devs  []device.Info
		needs []Need
		want  string
	}{
		{"any device present", []device.Info{pixel}, []Need{{}}, ""},
		{"no devices at all", nil, []Need{{}}, "needs a device, and none is connected"},
		{"android missing", []device.Info{iphone}, []Need{{Platform: "android"}}, "needs an android device, and none is connected"},
		{"ios missing", []device.Info{pixel}, []Need{{Platform: "ios"}}, "needs an iOS simulator, and none is connected"},
		{"browser missing", []device.Info{pixel}, []Need{{Platform: "web"}}, "needs a browser (install Google Chrome or set TB_CHROME), and none is connected"},
		{"pinned device missing", []device.Info{pixel}, []Need{{Device: "R58M"}}, `needs device "R58M", and none is connected`},
		{"pinned device wins over platform text", []device.Info{pixel}, []Need{{Platform: "ios", Device: "iPhone 9"}}, `needs device "iPhone 9", and none is connected`},
		{"role wording", []device.Info{pixel}, []Need{{Role: "alice", Platform: "ios"}}, `role "alice" needs an iOS simulator, and none is free among the connected devices`},
		{"two roles one device", []device.Info{pixel}, []Need{{Role: "a"}, {Role: "b"}}, `role "b" needs a device`},
		{"two roles two devices", []device.Info{pixel, iphone}, []Need{{Role: "a"}, {Role: "b"}}, ""},
		{"phone app with only a browser", []device.Info{chrome}, []Need{{AppID: "com.x", Mobile: true}}, "needs an Android device or iOS simulator with com.x installed, and none is connected"},
		{"browsers are elastic", []device.Info{chrome}, []Need{{Role: "a", Platform: "web"}, {Role: "b", Platform: "web"}}, ""},
		{"pinned browser is not elastic", []device.Info{chrome}, []Need{{Role: "a", Platform: "web"}, {Role: "b", Platform: "web", Device: "web-2"}}, `role "b" needs device "web-2"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := NewPool(c.devs).satisfiable(c.needs)
			switch {
			case c.want == "" && err != nil:
				t.Errorf("unexpected error: %v", err)
			case c.want != "" && err == nil:
				t.Errorf("want error containing %q", c.want)
			case c.want != "" && !strings.Contains(err.Error(), c.want):
				t.Errorf("error %q, want it to contain %q", err, c.want)
			}
		})
	}
}

func TestSatisfiableIgnoresBusyHonorsApps(t *testing.T) {
	p := NewPool([]device.Info{pixel})
	p.tryAlloc([]Need{{}})
	if err := p.satisfiable([]Need{{}}); err != nil {
		t.Errorf("busy device should still satisfy: %v", err)
	}
	p.Installed = func(serial, app string) bool { return false }
	err := p.satisfiable([]Need{{Platform: "android", AppID: "com.app"}})
	if err == nil || !strings.Contains(err.Error(), "needs an android device with com.app installed") {
		t.Errorf("err = %v", err)
	}
}

func TestNeedsFor(t *testing.T) {
	cases := []struct {
		name     string
		yaml     string
		platform string
		want     []Need
		env      []flow.KV
	}{
		{"single flow", "appId: com.app\n---\n- back\n", "", []Need{{AppID: "com.app", Mobile: true}}, nil},
		{"single flow with -p", "appId: com.app\n---\n- back\n", "ios", []Need{{Platform: "ios", AppID: "com.app"}}, nil},
		{"url flow is web", "url: https://example.com\n---\n- back\n", "", []Need{{Platform: "web"}}, nil},
		{"url flow keeps -p", "url: https://example.com\n---\n- back\n", "android", []Need{{Platform: "android"}}, nil},
		{"file url is web", "appId: file:///tmp/x.html\n---\n- back\n", "", []Need{{Platform: "web"}}, nil},
		{"interpolated app id is decided at run time", "appId: ${APP}\n---\n- back\n", "", []Need{{}}, nil},
		{"url from -e is web", "url: ${URL}\n---\n- back\n", "", []Need{{Platform: "web"}},
			[]flow.KV{{Key: "URL", Value: "http://x"}}},
		{"url: header is web even unresolved", "url: ${URL}\n---\n- back\n", "", []Need{{Platform: "web"}}, nil},
		{"url: header is web whatever its value", "url: localhost:3000\n---\n- back\n", "", []Need{{Platform: "web"}}, nil},
		{"app id from -e is a phone app", "appId: ${APP_ID}\n---\n- back\n", "", []Need{{AppID: "com.x", Mobile: true}},
			[]flow.KV{{Key: "APP_ID", Value: "com.x"}}},
		{"appId resolving to a url is web", "appId: ${APP_ID}\n---\n- back\n", "", []Need{{Platform: "web"}},
			[]flow.KV{{Key: "APP_ID", Value: "https://x"}}},
		{"unknown variable is decided at run time", "appId: ${APP_ID}\n---\n- back\n", "", []Need{{}},
			[]flow.KV{{Key: "OTHER", Value: "com.x"}}},
		{"script is decided at run time", "appId: ${APP_ID.toLowerCase()}\n---\n- back\n", "", []Need{{}},
			[]flow.KV{{Key: "APP_ID", Value: "com.x"}}},
		{"flow env default", "url: ${URL}\nenv:\n  URL: ${URL || 'https://dflt'}\n---\n- back\n", "", []Need{{Platform: "web"}}, nil},
		{"flow env literal", "appId: ${APP_ID}\nenv:\n  APP_ID: com.flow\n---\n- back\n", "", []Need{{AppID: "com.flow", Mobile: true}}, nil},
		{"-e overrides a flow env default", "appId: ${APP_ID}\nenv:\n  APP_ID: ${APP_ID || 'https://dflt'}\n---\n- back\n", "",
			[]Need{{AppID: "com.cli", Mobile: true}}, []flow.KV{{Key: "APP_ID", Value: "com.cli"}}},
		{"a literal flow env wins over -e, as in the engine", "appId: ${APP_ID}\nenv:\n  APP_ID: com.flow\n---\n- back\n", "",
			[]Need{{AppID: "com.flow", Mobile: true}}, []flow.KV{{Key: "APP_ID", Value: "com.cli"}}},
		{"-p beats a resolved app id", "appId: ${APP_ID}\n---\n- back\n", "web", []Need{{Platform: "web", AppID: "com.x"}},
			[]flow.KV{{Key: "APP_ID", Value: "com.x"}}},
		{"multi-device url: header makes inheriting roles web", `url: ${URL}
devices:
  alice: android
  bob:
  carol: { appId: "${BOB_APP}", env: { BOB_APP: com.carol } }
---
- back
`, "", []Need{
			{Role: "alice", Platform: "android"},
			{Role: "bob", Platform: "web"},
			{Role: "carol", AppID: "com.carol", Mobile: true},
		}, []flow.KV{{Key: "URL", Value: "https://chat"}}},
		{"multi-device roles", `appId: com.shared
devices:
  alice: { platform: ios, appId: com.alice }
  bob: { platform: android, device: "Pixel 7" }
  carol: web
  dave: { appId: "https://chat.example.com" }
---
- back
`, "android", []Need{
			{Role: "alice", Platform: "ios", AppID: "com.alice"},
			{Role: "bob", Platform: "android", Device: "Pixel 7", AppID: "com.shared"},
			{Role: "carol", Platform: "web", AppID: "com.shared"},
			{Role: "dave", Platform: "web"},
		}, nil},
		{"multi-device role without app inherits nothing", "devices:\n  alice: android\n---\n- alice: [back]\n", "", []Need{{Role: "alice", Platform: "android"}}, nil},
		{"multi-device ignores -p", "appId: com.app\ndevices:\n  alice:\n---\n- alice: [back]\n", "ios", []Need{{Role: "alice", AppID: "com.app", Mobile: true}}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := parseFlow(t, "f", c.yaml)
			if got := NeedsFor(f, c.platform, c.env); !reflect.DeepEqual(got, c.want) {
				t.Errorf("NeedsFor = %+v\nwant %+v", got, c.want)
			}
		})
	}
}

func TestNeedsForPlacesInterpolatedHeaders(t *testing.T) {
	env := []flow.KV{{Key: "URL", Value: "http://x"}, {Key: "APP_ID", Value: "com.x"}}
	for _, c := range []struct{ yaml, want string }{
		{"url: ${URL}\n---\n- back\n", chrome.Serial},
		{"appId: ${URL}\n---\n- back\n", chrome.Serial},
		{"appId: ${APP_ID}\n---\n- back\n", pixel.Serial},
	} {
		p := NewPool([]device.Info{pixel, chrome})
		got := p.tryAlloc(NeedsFor(parseFlow(t, "f", c.yaml), "", env))
		if got[""].Serial != c.want {
			t.Errorf("%q ran on %q, want %q", c.yaml, got[""].Serial, c.want)
		}
	}
	p := NewPool([]device.Info{chrome})
	needs := NeedsFor(parseFlow(t, "f", "appId: ${APP_ID}\n---\n- back\n"), "", env)
	if got := p.tryAlloc(needs); got != nil {
		t.Errorf("phone flow got %v", got)
	}
}

func TestCheckAppsSkips(t *testing.T) {
	ctx := context.Background()
	p := NewPool([]device.Info{chrome})
	p.CheckApps(ctx, []*Job{{Needs: []Need{{AppID: "https://x"}}}})
	if p.Installed != nil {
		t.Error("one device: Installed should stay nil")
	}
	p = NewPool([]device.Info{chrome, device.WebDevice(2)})
	p.CheckApps(ctx, []*Job{{Needs: []Need{{}}}, {Needs: []Need{{Role: "a"}}}})
	if p.Installed != nil {
		t.Error("no app ids: Installed should stay nil")
	}
}

func TestCheckAppsWebAndMobile(t *testing.T) {
	p := NewPool([]device.Info{pixel, chrome})
	url := "https://example.com"
	p.CheckApps(context.Background(), []*Job{{Needs: []Need{{AppID: url}}}})
	if p.Installed == nil {
		t.Fatal("Installed not set")
	}
	if !p.Installed("web", url) {
		t.Error("a URL should be installed on the browser")
	}
	if p.Installed(pixel.Serial, url) {
		t.Error("a URL should not be installed on a phone")
	}
	if !p.Installed("unknown-serial", url) {
		t.Error("an unchecked device should stay eligible")
	}
	if !p.Installed(pixel.Serial, "com.never.checked") {
		t.Error("an unchecked app should not hide a device")
	}
}

func TestCheckAppsNoDeviceHasApp(t *testing.T) {
	p := NewPool([]device.Info{pixel, galaxy})
	url := "https://example.com"
	p.CheckApps(context.Background(), []*Job{{Needs: []Need{{AppID: url}}}, {Needs: []Need{{AppID: url}}}})
	for _, d := range p.Devices() {
		if !p.Installed(d.Serial, url) {
			t.Errorf("%s hidden although no device has the app", d.Serial)
		}
	}
}

func TestCheckAppsAndroid(t *testing.T) {
	log := fakeADB(t, `case "$*" in
  "-s emulator-5554 shell pm path com.app") echo "package:/data/app/com.app/base.apk" ;;
  *) exit 1 ;;
esac`)
	p := NewPool([]device.Info{pixel, galaxy})
	p.CheckApps(context.Background(), []*Job{{Needs: []Need{{AppID: "com.app"}}}})
	if !p.Installed(pixel.Serial, "com.app") {
		t.Error("pixel has the app")
	}
	if p.Installed(galaxy.Serial, "com.app") {
		t.Error("galaxy lacks the app")
	}
	calls := readLines(t, log)
	sort.Strings(calls)
	want := []string{"-s R58M shell pm path com.app", "-s emulator-5554 shell pm path com.app"}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("adb calls = %q, want %q (once per device and app)", calls, want)
	}
	if got := p.tryAlloc([]Need{{AppID: "com.app"}}); got[""].Serial != pixel.Serial {
		t.Errorf("alloc = %v", got)
	}
}

func TestPoolDriverCached(t *testing.T) {
	f := newFake("s1", "One", "android")
	p := poolOf(f)
	for i := 0; i < 3; i++ {
		d, err := p.Driver(context.Background(), f.info())
		if err != nil || d != f {
			t.Fatalf("Driver = %v, %v", d, err)
		}
	}
}

func TestPoolDriverOpenError(t *testing.T) {
	info := device.Info{Platform: "fake", Serial: "f1", Name: "Fake"}
	p := NewPool([]device.Info{info})
	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = p.Driver(context.Background(), info)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err == nil || !strings.Contains(err.Error(), "fake devices are not supported yet") {
			t.Errorf("call %d: err = %v", i, err)
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.drivers) != 0 || len(p.opening) != 0 {
		t.Errorf("drivers=%v opening=%v", p.drivers, p.opening)
	}
}

func TestPoolDriverWaitsForOpening(t *testing.T) {
	f := newFake("s1", "One", "android")
	p := NewPool([]device.Info{f.info()})
	ch := make(chan struct{})
	p.opening[f.serial] = ch
	got := make(chan error, 1)
	go func() {
		d, err := p.Driver(context.Background(), f.info())
		if err == nil && d != f {
			err = fmt.Errorf("got driver %v, want the one the other caller opened", d)
		}
		got <- err
	}()
	time.Sleep(30 * time.Millisecond)
	p.mu.Lock()
	delete(p.opening, f.serial)
	p.drivers[f.serial] = f
	p.mu.Unlock()
	close(ch)
	if err := <-got; err != nil {
		t.Fatalf("waiter: %v", err)
	}
}

func TestPoolClose(t *testing.T) {
	a, b := newFake("a", "A", "android"), newFake("b", "B", "ios")
	p := poolOf(a, b)
	p.Close()
	if a.closed != 1 || b.closed != 1 {
		t.Errorf("closed a=%d b=%d", a.closed, b.closed)
	}
}

func fakeADB(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "calls.log")
	tools := filepath.Join(dir, "platform-tools")
	os.MkdirAll(tools, 0o755)
	adb := filepath.Join(tools, "adb")
	script := "#!/bin/sh\necho \"$*\" >> '" + log + "'\n" + body + "\n"
	if err := os.WriteFile(adb, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ANDROID_HOME", dir)
	if got := device.ADB(); got != adb {
		t.Fatalf("device.ADB() = %q, want the fake %q", got, adb)
	}
	return log
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
}
