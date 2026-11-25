package main

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/device"
)

type fakeTools struct {
	mu      sync.Mutex
	avds    string
	sims    string
	spawned []string
	booted  []string
	started bool
	dies    bool
	polls   int
}

func withFakeTools(t *testing.T, mac bool) *fakeTools {
	t.Helper()
	f := &fakeTools{}
	t.Setenv("TB_CI", "0")
	oldRun, oldSpawn, oldMac, oldLimit, oldPoll, oldList := runTool, spawnTool, hasMacSimulators, bootLimit, bootPoll, listDevices
	runTool = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		all := strings.Join(args, " ")
		switch {
		case all == "-list-avds":
			if f.avds == "" {
				return nil, errors.New("no emulator")
			}
			return []byte(f.avds), nil
		case strings.HasSuffix(all, "getprop sys.boot_completed"):
			f.polls++
			if f.polls < 2 {
				return []byte("\n"), nil
			}
			return []byte("1\n"), nil
		case all == "simctl list devices available -j":
			if f.sims == "" {
				return nil, errors.New("no xcrun")
			}
			return []byte(f.sims), nil
		case strings.HasPrefix(all, "simctl bootstatus "):
			f.booted = append(f.booted, args[2])
			f.started = true
			return nil, nil
		}
		return nil, errors.New("unexpected " + name + " " + all)
	}
	spawnTool = func(name string, args ...string) (<-chan struct{}, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.spawned = append(f.spawned, filepath.Base(name)+" "+strings.Join(args, " "))
		exited := make(chan struct{})
		if f.dies {
			close(exited)
		} else {
			f.started = true
		}
		return exited, nil
	}
	hasMacSimulators = func() bool { return mac }
	bootLimit, bootPoll = 5*time.Second, time.Millisecond
	t.Cleanup(func() {
		runTool, spawnTool, hasMacSimulators, bootLimit, bootPoll, listDevices = oldRun, oldSpawn, oldMac, oldLimit, oldPoll, oldList
	})
	return f
}

func phoneOnceStarted(t *testing.T, f *fakeTools, info device.Info) {
	t.Helper()
	app := withFakePhone(t)
	app.serial = info.Serial
	listDevices = func(ctx context.Context) []device.Info {
		f.mu.Lock()
		defer f.mu.Unlock()
		if !f.started {
			return []device.Info{device.WebDevice(1)}
		}
		return []device.Info{device.WebDevice(1), info}
	}
}

func TestTestStartsTheEmulatorWhenNoDeviceRuns(t *testing.T) {
	f := withFakeTools(t, true)
	f.avds = "INFO    | Storing crashdata in: /tmp/x\nPixel_8_API_35\nTablet\n"
	phoneOnceStarted(t, f, device.Info{Platform: device.Android, Serial: "emulator-5554", Name: "Pixel 8", Emulator: true, State: "device"})
	dir := t.TempDir()
	flow := writeFlow(t, dir, "pass.yaml", passingFlow)
	var code int
	out, errOut := capture(t, func() {
		code = cmdTest([]string{flow, "--json", "--debug-output", filepath.Join(dir, "debug")})
	})
	if code != 0 {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if len(f.spawned) != 1 || !strings.HasSuffix(f.spawned[0], " -avd Pixel_8_API_35") {
		t.Errorf("spawned %q, want the first AVD", f.spawned)
	}
	if !strings.Contains(errOut, "starting the Android emulator Pixel_8_API_35") {
		t.Errorf("stderr does not say it starts the emulator:\n%s", errOut)
	}
	if f.polls < 2 {
		t.Errorf("did not wait for the boot to complete (%d polls)", f.polls)
	}
}

func TestTestBootsASimulatorWhenNoEmulatorIsSetUp(t *testing.T) {
	f := withFakeTools(t, true)
	f.sims = `{"devices":{
		"com.apple.CoreSimulator.SimRuntime.watchOS-11-0":[{"udid":"W","name":"Apple Watch","state":"Shutdown"}],
		"com.apple.CoreSimulator.SimRuntime.iOS-17-5":[{"udid":"OLD","name":"iPhone 15","state":"Shutdown"}],
		"com.apple.CoreSimulator.SimRuntime.iOS-18-2":[{"udid":"PAD","name":"iPad Air","state":"Shutdown"},{"udid":"NEW","name":"iPhone 16","state":"Shutdown"}]}}`
	phoneOnceStarted(t, f, device.Info{Platform: device.Android, Serial: "emulator-5554", Name: "Fake phone", Emulator: true, State: "device"})
	if !startDevice(context.Background(), "", func(string) {}) {
		t.Fatal("no device started")
	}
	if len(f.spawned) != 0 || len(f.booted) != 1 || f.booted[0] != "NEW" {
		t.Errorf("spawned %q, booted %q; want the iPhone of the newest iOS", f.spawned, f.booted)
	}
}

func TestTestStartsNothing(t *testing.T) {
	cases := []struct {
		name     string
		mac      bool
		platform device.Platform
		avds     string
		sims     string
	}{
		{"no emulator and not a Mac", false, "", "", `{"devices":{"com.apple.CoreSimulator.SimRuntime.iOS-18-2":[{"udid":"A","name":"iPhone 16","state":"Shutdown"}]}}`},
		{"no AVD", false, "", "INFO    | nothing here\n", ""},
		{"web", true, device.Web, "Pixel\n", `{"devices":{"com.apple.CoreSimulator.SimRuntime.iOS-18-2":[{"udid":"A","name":"iPhone 16","state":"Shutdown"}]}}`},
		{"ios asked, only an emulator", true, device.IOS, "Pixel\n", ""},
		{"android asked, only a simulator", true, device.Android, "", `{"devices":{"com.apple.CoreSimulator.SimRuntime.iOS-18-2":[{"udid":"A","name":"iPhone 16","state":"Shutdown"}]}}`},
		{"no shut-down iPhone", true, "", "", `{"devices":{"com.apple.CoreSimulator.SimRuntime.iOS-18-2":[{"udid":"A","name":"iPhone 16","state":"Booted"},{"udid":"B","name":"iPad","state":"Shutdown"}]}}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := withFakeTools(t, c.mac)
			f.avds, f.sims = c.avds, c.sims
			if startDevice(context.Background(), c.platform, func(string) {}) {
				t.Error("started a device")
			}
			if len(f.spawned)+len(f.booted) != 0 {
				t.Errorf("spawned %q, booted %q", f.spawned, f.booted)
			}
		})
	}
}

func TestTestStartsNoDeviceWhenNotOneAtATime(t *testing.T) {
	for _, c := range []struct {
		name string
		args []string
		ci   string
	}{
		{"shards", []string{"--shard-split", "2"}, "0"},
		{"on CI", nil, "1"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := withFakeTools(t, true)
			f.avds = "Pixel\n"
			t.Setenv("TB_CI", c.ci)
			phoneOnceStarted(t, f, device.Info{Platform: device.Android, Serial: "emulator-5554", Name: "Fake phone", Emulator: true, State: "device"})
			flow := writeFlow(t, t.TempDir(), "pass.yaml", passingFlow)
			var code int
			capture(t, func() { code = cmdTest(append([]string{flow, "--debug-output", t.TempDir()}, c.args...)) })
			if code == 0 || len(f.spawned) != 0 {
				t.Errorf("exit %d, spawned %q; want no device started", code, f.spawned)
			}
		})
	}
}

func TestTestSaysWhenTheEmulatorExitsAtOnce(t *testing.T) {
	f := withFakeTools(t, true)
	f.avds = "Pixel\n"
	f.dies = true
	f.sims = `{"devices":{"com.apple.CoreSimulator.SimRuntime.iOS-18-2":[{"udid":"A","name":"iPhone 16","state":"Shutdown"}]}}`
	listDevices = func(ctx context.Context) []device.Info { return nil }
	var said []string
	start := time.Now()
	if startDevice(context.Background(), "", func(s string) { said = append(said, s) }) {
		t.Fatal("reported a device started")
	}
	if time.Since(start) > time.Second {
		t.Errorf("waited %s for an emulator that had exited", time.Since(start))
	}
	if len(said) != 2 || !strings.Contains(said[1], "the emulator Pixel exited before it started: run `") {
		t.Errorf("said %q", said)
	}
	if len(f.booted) != 0 {
		t.Errorf("booted a simulator %q after the emulator failed", f.booted)
	}
}

func TestNewerIOS(t *testing.T) {
	rts := []string{"com.apple.CoreSimulator.SimRuntime.iOS-26-9", "com.apple.CoreSimulator.SimRuntime.iOS-9-3",
		"com.apple.CoreSimulator.SimRuntime.iOS-26-10", "com.apple.CoreSimulator.SimRuntime.iOS-18-2"}
	sort.Slice(rts, func(i, j int) bool { return newerIOS(rts[i], rts[j]) })
	want := "iOS-26-10 iOS-26-9 iOS-18-2 iOS-9-3"
	var got []string
	for _, r := range rts {
		got = append(got, strings.TrimPrefix(r, "com.apple.CoreSimulator.SimRuntime."))
	}
	if strings.Join(got, " ") != want {
		t.Errorf("order %q, want %q", got, want)
	}
}

func TestTestStartsNoPhoneForWebFlows(t *testing.T) {
	f := withFakeTools(t, true)
	f.avds = "Pixel\n"
	phoneOnceStarted(t, f, device.Info{Platform: device.Android, Serial: "emulator-5554", Name: "Fake phone", Emulator: true, State: "device"})
	dir := t.TempDir()
	flow := writeFlow(t, dir, "web.yaml", "url: https://example.com\n---\n- assertVisible: Home\n")
	capture(t, func() { cmdTest([]string{flow, "--debug-output", filepath.Join(dir, "debug")}) })
	if len(f.spawned) != 0 {
		t.Errorf("spawned %q for a web flow", f.spawned)
	}
}
