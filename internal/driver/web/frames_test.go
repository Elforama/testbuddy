package web

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/driver"
)

func TestFramesProbe(t *testing.T) {
	u := os.Getenv("TB_WEB_FRAMES")
	if u == "" {
		t.Skip("set TB_WEB_FRAMES")
	}
	os.Setenv("TB_WEB_HEADLESS", "1")
	ctx := context.Background()
	d, err := Open(ctx, device.WebDevice(1))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	d.LaunchApp(ctx, u, driver.LaunchOpts{})
	d.WaitSettled(ctx, 3*time.Second)
	time.Sleep(500 * time.Millisecond)
	d.mu.Lock()
	for id, f := range d.frames {
		t.Logf("frame %s %s", id, f.url)
	}
	d.mu.Unlock()
	var js string
	d.eval(ctx, "window.__tb.snapshot()", &js)
	t.Logf("%s", js)
}
