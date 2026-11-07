package ios

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/engine"
	"github.com/elforama/testbuddy/internal/flow"
)

// The Flutter and UIKit screens here belong to the SAME bundle/PID. A bundle-only
// detector incorrectly applies the Flutter drag to both.
func TestFlutterSurfaceRouting(t *testing.T) {
	if os.Getenv("TB_FLUTTER_BENCH") != "1" || os.Getenv("TB_IOS_UDID") == "" {
		t.Skip("requires the Flutter benchmark installed on a nominated simulator")
	}
	d := openSim(t)
	defer d.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	run := func(steps string) {
		t.Helper()
		cmds, err := flow.ParseCommands(".", []byte(steps))
		if err != nil {
			t.Fatal(err)
		}
		if err = engine.New(d, engine.Options{}).RunCommands(ctx, cmds, "dev.testbuddy.bench.flutter", nil); err != nil {
			t.Fatal(err)
		}
	}
	drag := func(want bool) {
		t.Helper()
		raw, err := d.Raw(ctx, "swipe", map[string]any{"x1": 180, "y1": 470, "x2": 180, "y2": 425, "drag": true, "settle": false})
		if err != nil {
			t.Fatal(err)
		}
		var result struct {
			Flutter *bool `json:"flutterDrag"`
		}
		if err = json.Unmarshal(raw, &result); err != nil || result.Flutter == nil || *result.Flutter != want {
			t.Fatalf("want Flutter drag %v: %s (%v)", want, raw, err)
		}
	}
	run("- launchApp: {clearState: true}\n- assertVisible: {id: scenarios_title}")
	drag(true)
	run("- scrollUntilVisible: {element: {id: scenario_native}}\n- tapOn: {id: scenario_native}\n- tapOn: {id: tk_uikit}\n- assertVisible: {id: tk_title}")
	drag(false)
	run("- tapOn: {id: back}\n- assertVisible: {id: tk_uikit}")
	var surface struct {
		Flutter bool `json:"flutter"`
	}
	raw, err := d.Raw(ctx, "flutterSurface", map[string]any{"x": 180, "y": 470, "timeout": 5000})
	if err != nil || json.Unmarshal(raw, &surface) != nil || !surface.Flutter {
		t.Fatalf("Flutter surface did not return: %s (%v)", raw, err)
	}
	drag(true)

	run("- launchApp: {clearState: true}\n- tapOn: {id: scenario_journey}\n- tapOn: {id: username}\n- inputText: buddy")
	screen, err := d.Screen(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range screen.Windows {
		if w.Type != "keyboard" || w.Bounds.Empty() {
			continue
		}
		var keyboardPoint struct {
			Flutter *bool `json:"flutter"`
		}
		raw, err := d.Raw(ctx, "flutterSurface", map[string]any{"x": w.Bounds.CX(), "y": w.Bounds.CY()})
		if err != nil || json.Unmarshal(raw, &keyboardPoint) != nil || keyboardPoint.Flutter == nil || *keyboardPoint.Flutter {
			t.Fatalf("system keyboard claimed by Flutter: %s (%v)", raw, err)
		}
		return
	}
	t.Fatal("fixture did not show the software keyboard")
}
