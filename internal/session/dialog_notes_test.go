package session

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/daemon"
	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/engine"
	"github.com/elforama/testbuddy/internal/look"
)

// notingFake is a device that does things on its own and says so (driver.Noter), as the web
// driver does when it answers a JavaScript dialog: a tap opens one.
type notingFake struct {
	*fakeDriver
	nmu   sync.Mutex
	notes []string
}

func (f *notingFake) note(s string) {
	f.nmu.Lock()
	f.notes = append(f.notes, s)
	f.nmu.Unlock()
}

func (f *notingFake) Notes() []string {
	f.nmu.Lock()
	defer f.nmu.Unlock()
	n := f.notes
	f.notes = nil
	return n
}

func (f *notingFake) Tap(ctx context.Context, x, y int, o driver.ActionOpts) (driver.ActionResult, error) {
	f.note(`web: the page opened a JavaScript confirm "Delete it?": tb answered OK (confirm() returned true)`)
	return f.fakeDriver.Tap(ctx, x, y, o)
}

func addNotingDev(s *Service, f *notingFake) *Dev {
	d := &Dev{Info: f.info, Drv: f, refs: look.NewRefs()}
	d.runner = engine.New(f, engine.Options{ArtifactsDir: filepath.Join(daemon.Dir(), "artifacts"),
		LookupTimeout: 300 * time.Millisecond, OptionalTimeout: 100 * time.Millisecond})
	s.mu.Lock()
	s.devs[f.info.Serial] = d
	s.mu.Unlock()
	return d
}

// issue #91: what the driver did on its own (web: a JavaScript dialog it answered) was written
// only to a run's trace, which `tb do` and `tb look` have none of, so an agent driving a page
// never learnt that a dialog had opened, or what it said. do prints it under the step it
// happened in (and in --json), and look prints what happened since the last command.
func TestDoAndLookShowDriverNotes(t *testing.T) {
	s := New()
	f := &notingFake{fakeDriver: newFake("chrome", "Chrome", device.Web)}
	f.show(button("Delete", 100), label("Items", 200))
	addNotingDev(s, f)

	resp, _ := call(t, s.do, map[string]any{"commands": "- tapOn: Delete\n- assertVisible: Items\n", "json": true}, "")
	if !resp.OK {
		t.Fatalf("resp %+v", resp)
	}
	want := `  note: web: the page opened a JavaScript confirm "Delete it?": tb answered OK`
	lines := strings.Split(resp.Text, "\n")
	if len(lines) < 2 || !strings.HasPrefix(lines[0], `ok Tap on "Delete"`) || !strings.HasPrefix(lines[1], want) {
		t.Errorf("do does not show the dialog under its step:\n%s", resp.Text)
	}
	var res struct {
		Steps []struct {
			Notes []string `json:"notes"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(resp.JSON, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Steps) != 2 || len(res.Steps[0].Notes) != 1 || len(res.Steps[1].Notes) != 0 {
		t.Errorf("--json steps' notes: %+v", res.Steps)
	}

	// A dialog that opens between commands (a timer) is shown by the next look.
	f.note(`web: the page opened a JavaScript alert "Saved!": tb answered OK`)
	resp, _ = call(t, s.look, nil, "")
	if !strings.Contains(resp.Text, `note: web: the page opened a JavaScript alert "Saved!": tb answered OK`) {
		t.Errorf("look does not show the dialog:\n%s", resp.Text)
	}
	resp, _ = call(t, s.look, nil, "")
	if strings.Contains(resp.Text, "note:") {
		t.Errorf("look shows a note twice:\n%s", resp.Text)
	}
}
