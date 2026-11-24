package session

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/elforama/testbuddy/internal/daemon"
	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/driver"
)

// issue #399: inside one daemon a session belonged to nobody: a second agent drove the first
// agent's session by its serial, by giving no -d, and closed it, alone or with tb close all.
// A session now knows its owner (TB_SESSION_OWNER, sent as "owner"); another owner is told so
// instead of being let in; tb close all closes the caller's sessions only, unless forced.
func TestSessionsOfAnotherOwner(t *testing.T) {
	t.Setenv("TB_HOME", t.TempDir())
	t.Setenv("TB_LOCK_DIR", t.TempDir())
	adbLists(t, "emulator-5750 sdk_gphone64_arm64")
	s := New()
	s.openDrv = func(_ context.Context, info device.Info) (driver.Driver, error) {
		f := newFake(info.Serial, info.Name, info.Platform)
		f.show(loginScreen()...)
		return f, nil
	}
	defer s.CloseAll(context.Background())
	agent1 := map[string]any{"owner": "agent-1"}
	with := func(base map[string]any, kv ...any) map[string]any {
		m := map[string]any{}
		for k, v := range base {
			m[k] = v
		}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i].(string)] = kv[i+1]
		}
		return m
	}
	if r, _ := call(t, s.open, with(agent1, "device", "emulator-5750", "name", "alice"), ""); !r.OK {
		t.Fatalf("agent 1 opens: %q", r.Text)
	}
	agent2 := map[string]any{"owner": "agent-2"}
	refused := `is session "alice" of another agent: use another device`
	for _, c := range []struct {
		name string
		h    handler
		args map[string]any
	}{
		{"look by serial", s.look, with(agent2, "device", "emulator-5750")},
		{"look with no -d", s.look, agent2},
		{"do by name", s.do, with(agent2, "device", "alice", "commands", "launchApp")},
		{"close by name", s.close, with(agent2, "device", "alice")},
	} {
		r, _ := call(t, c.h, c.args, "")
		if r.OK || !strings.Contains(r.Text, refused) {
			t.Errorf("issue #399: agent 2, %s: %q", c.name, r.Text)
		}
	}
	// tb devices tells agent 2 the session is another agent's; agent 1 sees its own.
	if r, _ := call(t, s.devices, agent2, ""); !strings.Contains(r.Text, "session: alice (another agent's)") || !strings.Contains(string(r.JSON), `"others":true`) {
		t.Errorf("devices for agent 2: %q %s", r.Text, r.JSON)
	}
	if r, _ := call(t, s.devices, agent1, ""); strings.Contains(r.Text, "another agent's") {
		t.Errorf("devices for agent 1: %q", r.Text)
	}
	r, _ := call(t, s.close, with(agent2, "device", "all"), "")
	s.mu.Lock()
	_, open := s.devs["emulator-5750"]
	s.mu.Unlock()
	if !r.OK || !open || !strings.Contains(r.Text, "left 1 session(s) of other agents open") {
		t.Errorf("issue #399: agent 2's tb close all closed agent 1's session: %q", r.Text)
	}
	// Agent 1 still uses it as before, by name, serial or no -d.
	for _, args := range []map[string]any{with(agent1, "device", "alice"), with(agent1, "device", "emulator-5750"), agent1} {
		if r, _ := call(t, s.look, args, ""); !r.OK {
			t.Errorf("agent 1, %v: %q", args, r.Text)
		}
	}
	// Forced, tb close all closes every agent's session.
	if r, _ := call(t, s.close, with(agent2, "device", "all", "force", true), ""); !r.OK {
		t.Errorf("forced close all: %q", r.Text)
	}
	s.mu.Lock()
	_, open = s.devs["emulator-5750"]
	s.mu.Unlock()
	if open {
		t.Error("tb close all --force left agent 1's session open")
	}
}

// issue #399: callers that set no owner keep today's behaviour among themselves.
func TestSessionsWithNoOwner(t *testing.T) {
	t.Setenv("TB_HOME", t.TempDir())
	t.Setenv("TB_LOCK_DIR", t.TempDir())
	adbLists(t, "emulator-5750 sdk_gphone64_arm64")
	s := New()
	s.openDrv = func(_ context.Context, info device.Info) (driver.Driver, error) {
		f := newFake(info.Serial, info.Name, info.Platform)
		f.show(loginScreen()...)
		return f, nil
	}
	defer s.CloseAll(context.Background())
	if r, _ := call(t, s.open, map[string]any{"device": "emulator-5750", "name": "alice"}, ""); !r.OK {
		t.Fatalf("open: %q", r.Text)
	}
	for _, args := range []map[string]any{{"device": "emulator-5750"}, {}, {"device": "alice"}} {
		if r, _ := call(t, s.look, args, ""); !r.OK {
			t.Errorf("no owner, %v: %q", args, r.Text)
		}
	}
	if r, _ := call(t, s.close, map[string]any{"device": "all"}, ""); !r.OK || strings.Contains(r.Text, "left") {
		t.Errorf("close all: %q", r.Text)
	}
}

// issue #399: a tb test run takes a device back only from its own owner's idle session.
func TestYieldOnlyToTheSameOwner(t *testing.T) {
	t.Setenv("TB_HOME", t.TempDir())
	t.Setenv("TB_NO_DEVICE_LOCK", "")
	t.Setenv("TB_LOCK_DIR", t.TempDir())
	f := newFake("emulator-5750", "Pixel 7", device.Android)
	f.show(loginScreen()...)
	s := New()
	s.openDrv = func(context.Context, device.Info) (driver.Driver, error) { return f, nil }
	defer s.CloseAll(context.Background())
	if _, err := s.connect(context.Background(), f.info, "alice", "agent-1"); err != nil {
		t.Fatal(err)
	}
	yield := func(owner string) Yielded {
		args, _ := json.Marshal(YieldArgs{Serial: "emulator-5750", Owner: owner})
		var y Yielded
		json.Unmarshal(s.yield(context.Background(), daemon.Request{Args: args}, nil).JSON, &y)
		return y
	}
	if y := yield("agent-2"); y.Yielded || y.Busy {
		t.Errorf("another owner: %+v", y)
	}
	if y := yield(""); y.Yielded {
		t.Errorf("no owner: %+v", y)
	}
	if y := yield("agent-1"); !y.Yielded {
		t.Errorf("its owner: %+v", y)
	}
}
