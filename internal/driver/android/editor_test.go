package android

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
)

// AND-005: the app's soft-key callback belongs to its editor connection. Keep
// actual deletion and that callback together, with no hardware replay afterwards.
func TestBackspaceUsesEditorConnectionWithoutDuplicateKey(t *testing.T) {
	for _, mode := range []string{"sent", "unavailable", "rejected", "lost reply"} {
		t.Run(mode, func(t *testing.T) {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
			var callbacks atomic.Int32
			go func() {
				for {
					c, e := ln.Accept()
					if e != nil {
						return
					}
					func() {
						defer c.Close()
						var target map[string]any
						if json.NewDecoder(c).Decode(&target) != nil {
							return
						}
						if target["pkg"] != "example.app" {
							return
						}
						if mode == "sent" || mode == "lost reply" {
							callbacks.Add(1)
						}
						if mode != "lost reply" {
							fmt.Fprintln(c, mode)
						}
					}()
				}
			}()
			a := newFakeAgent(t)
			a.on("editorTarget", func(map[string]any) map[string]any {
				return map[string]any{"seq": 12, "target": map[string]any{"pkg": "example.app", "window": 7, "node": 42, "id": "field", "cls": "EditText"}}
			})
			a.on("settle", func(map[string]any) map[string]any { return map[string]any{"result": "changed", "seq": 13} })
			a.on("key", func(map[string]any) map[string]any { return map[string]any{"result": "changed"} })
			port := ln.Addr().(*net.TCPAddr).Port
			stub := stubADB(t, []stubRule{{match: "forward tcp:0 localabstract:testbuddy-editor", out: fmt.Sprint(port)}})
			t.Setenv("TB_NO_A11Y_SERVICE", "1")
			d := stub.driver()
			c, h, e := dialHello(a.port(), 2*time.Second)
			if e != nil {
				t.Fatal(e)
			}
			d.adopt(c, a.port(), h)
			defer d.Close()
			t.Setenv("TB_NO_A11Y_SERVICE", "")
			_, e = d.PressKey(context.Background(), "Backspace", driver.ActionOpts{React: true})
			switch mode {
			case "sent":
				if e != nil || callbacks.Load() != 1 || len(a.requests("key")) != 0 {
					t.Fatalf("callback=%d hardware=%d error=%v", callbacks.Load(), len(a.requests("key")), e)
				}
			case "unavailable":
				if e != nil || callbacks.Load() != 0 || len(a.requests("key")) != 1 {
					t.Fatalf("fallback duplicated/lost key: callback=%d hardware=%d error=%v", callbacks.Load(), len(a.requests("key")), e)
				}
			default:
				if e == nil || len(a.requests("key")) != 0 {
					t.Fatalf("unsafe replay after %s: hardware=%d error=%v", mode, len(a.requests("key")), e)
				}
			}
			if mode == "sent" && !strings.Contains(strings.Join(stub.joined(), "\n"), "localabstract:testbuddy-editor") {
				t.Fatal("editor was not reached through adb")
			}
		})
	}
}

func TestBackspaceKeepsLegacyPathWhenEditorCapabilityIsUnavailable(t *testing.T) {
	for _, tc := range []struct {
		sdk      int
		disabled bool
	}{{26, false}, {29, false}, {32, false}, {33, true}, {34, true}, {34, false}} {
		t.Run(fmt.Sprintf("sdk%d_disabled%v", tc.sdk, tc.disabled), func(t *testing.T) {
			a := newFakeAgent(t)
			a.on("editorTarget", func(map[string]any) map[string]any { return map[string]any{"seq": 1} })
			a.on("key", func(map[string]any) map[string]any { return map[string]any{"result": "changed"} })
			d := newDriver(t, a)
			d.stateMu.Lock()
			d.sdk = tc.sdk
			d.stateMu.Unlock()
			setting := ""
			if tc.disabled {
				setting = "1"
			}
			t.Setenv("TB_NO_A11Y_SERVICE", setting)
			if _, err := d.PressKey(ctxT(t), "Backspace", driver.ActionOpts{}); err != nil {
				t.Fatal(err)
			}
			if len(a.requests("key")) != 1 {
				t.Fatal("legacy key was lost or duplicated")
			}
			if tc.sdk < 33 || tc.disabled {
				if len(a.requests("editorTarget")) != 0 {
					t.Fatal("unavailable API was queried")
				}
			}
		})
	}
}
