package connect

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/driver"
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "tb-connect-test")
	if err != nil {
		panic(err)
	}
	empty := filepath.Join(dir, "bin")
	os.MkdirAll(empty, 0o755)
	for k, v := range map[string]string{
		"PATH": empty, "HOME": dir, "ANDROID_HOME": dir, "ANDROID_SDK_ROOT": dir,
		"TB_HOME": filepath.Join(dir, "tbhome"), "TB_CHROME": filepath.Join(dir, "no-chrome"),
	} {
		os.Setenv(k, v)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type stubDriver struct{ driver.Driver }

func withOpener(t *testing.T, p device.Platform, open func(context.Context, device.Info) (driver.Driver, error)) {
	t.Helper()
	old, had := openers[p]
	openers[p] = open
	t.Cleanup(func() {
		if had {
			openers[p] = old
		} else {
			delete(openers, p)
		}
	})
}

func TestEveryPlatformHasAnOpener(t *testing.T) {
	for _, p := range []device.Platform{device.Android, device.IOS, device.Web} {
		if openers[p] == nil {
			t.Errorf("no opener for %s", p)
		}
	}
	if len(openers) != 3 {
		t.Errorf("%d openers, want 3", len(openers))
	}
}

func TestOpenDispatchesByPlatform(t *testing.T) {
	type key struct{}
	for _, p := range []device.Platform{device.Android, device.IOS, device.Web} {
		t.Run(string(p), func(t *testing.T) {
			want := &stubDriver{}
			var got device.Info
			var gotCtx context.Context
			withOpener(t, p, func(ctx context.Context, i device.Info) (driver.Driver, error) {
				got, gotCtx = i, ctx
				return want, nil
			})
			info := device.Info{Platform: p, Serial: "s-" + string(p), Name: "N"}
			ctx := context.WithValue(context.Background(), key{}, "v")
			drv, err := Open(ctx, info)
			if err != nil || drv != want {
				t.Fatalf("Open = %v, %v", drv, err)
			}
			if got != info {
				t.Errorf("opener got %+v", got)
			}
			if gotCtx.Value(key{}) != "v" {
				t.Error("opener did not get the caller's context")
			}
		})
	}
}

func TestOpenPassesErrors(t *testing.T) {
	boom := errors.New("agent did not start")
	withOpener(t, device.Android, func(context.Context, device.Info) (driver.Driver, error) { return nil, boom })
	drv, err := Open(context.Background(), device.Info{Platform: device.Android})
	if drv != nil || !errors.Is(err, boom) {
		t.Errorf("Open = %v, %v", drv, err)
	}
}

func TestOpenUnsupported(t *testing.T) {
	for _, p := range []device.Platform{"symbian", ""} {
		drv, err := Open(context.Background(), device.Info{Platform: p, Serial: "x"})
		if drv != nil || err == nil || err.Error() != string(p)+" devices are not supported yet" {
			t.Errorf("Open(%q) = %v, %v", p, drv, err)
		}
	}
}

func TestWebAvailableUsesChrome(t *testing.T) {
	t.Setenv("TB_CHROME", "/opt/some/chrome")
	if !device.WebAvailable() {
		t.Error("WebAvailable should be true when TB_CHROME names a browser")
	}
}

func TestOpenersFailWithoutTools(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, p := range []device.Platform{device.Android, device.IOS, device.Web} {
		d, err := Open(ctx, device.Info{Platform: p, Serial: "x", Name: "x"})
		if err == nil {
			d.Close()
			t.Errorf("%s: opened a device with no tools", p)
		}
	}
}

func TestOverride(t *testing.T) {
	fake := errors.New("fake opener")
	restore := Override(device.Web, func(context.Context, device.Info) (driver.Driver, error) { return nil, fake })
	if _, err := Open(context.Background(), device.Info{Platform: device.Web}); err != fake {
		t.Errorf("overridden: %v", err)
	}
	restore()
	if _, err := Open(context.Background(), device.Info{Platform: device.Web}); err == fake {
		t.Error("restore left the override in place")
	}
	restore = Override("tv", func(context.Context, device.Info) (driver.Driver, error) { return nil, fake })
	restore()
	if _, err := Open(context.Background(), device.Info{Platform: "tv"}); err == nil || err == fake {
		t.Errorf("a new platform's override was not removed: %v", err)
	}
}
