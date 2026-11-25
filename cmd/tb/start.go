package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/elforama/testbuddy/internal/device"
)

var (
	bootLimit = 5 * time.Minute
	bootPoll  = 2 * time.Second

	runTool = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return exec.CommandContext(ctx, name, args...).Output()
	}
	spawnTool = func(name string, args ...string) (<-chan struct{}, error) {
		cmd := exec.Command(name, args...)
		cmd.SysProcAttr = emulatorAttr()
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		exited := make(chan struct{})
		go func() {
			cmd.Wait()
			close(exited)
		}()
		return exited, nil
	}
	hasMacSimulators = func() bool { return runtime.GOOS == "darwin" }
)

func startDevice(ctx context.Context, platform device.Platform, say func(string)) bool {
	switch platform {
	case device.Android:
		started, _ := startEmulator(ctx, say)
		return started
	case device.IOS:
		return hasMacSimulators() && bootSimulator(ctx, say)
	}
	return false
}

func emulatorPath() string {
	exe := "emulator"
	if runtime.GOOS == "windows" {
		exe = "emulator.exe"
	}
	for _, env := range []string{"ANDROID_HOME", "ANDROID_SDK_ROOT"} {
		if root := os.Getenv(env); root != "" {
			p := filepath.Join(root, "emulator", exe)
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	if p, err := exec.LookPath(exe); err == nil {
		return p
	}
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Android", "sdk", "emulator", exe)
	case "windows":
		local := os.Getenv("LOCALAPPDATA")
		if local == "" {
			local = filepath.Join(home, "AppData", "Local")
		}
		return filepath.Join(local, "Android", "Sdk", "emulator", exe)
	}
	return filepath.Join(home, "Android", "Sdk", "emulator", exe)
}

func startEmulator(ctx context.Context, say func(string)) (started, tried bool) {
	emu := emulatorPath()
	c, cancel := context.WithTimeout(ctx, 30*time.Second)
	out, err := runTool(c, emu, "-list-avds")
	cancel()
	if err != nil {
		return false, false
	}
	avd := ""
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.Contains(line, " ") {
			avd = line
			break
		}
	}
	if avd == "" {
		return false, false
	}
	say(fmt.Sprintf("no device is running: starting the Android emulator %s (it keeps running after the test)", avd))
	exited, err := spawnTool(emu, "-avd", avd)
	if err != nil {
		say(fmt.Sprintf("could not start the emulator: %v", err))
		return false, true
	}
	deadline := time.Now().Add(bootLimit)
	for time.Now().Before(deadline) {
		for _, d := range listDevices(ctx) {
			if d.Platform != device.Android || !d.Emulator {
				continue
			}
			c, cancel := context.WithTimeout(ctx, 10*time.Second)
			out, err := runTool(c, device.ADB(), "-s", d.Serial, "shell", "getprop", "sys.boot_completed")
			cancel()
			if err == nil && strings.TrimSpace(string(out)) == "1" {
				return true, true
			}
		}
		select {
		case <-ctx.Done():
			return false, true
		case <-exited:
			say(fmt.Sprintf("the emulator %s exited before it started: run `%s -avd %s` to see why", avd, emu, avd))
			return false, true
		case <-time.After(bootPoll):
		}
	}
	say(fmt.Sprintf("the emulator %s did not finish starting in %s", avd, bootLimit))
	return false, true
}

func iosVersion(runtimeID string) []int {
	_, v, _ := strings.Cut(runtimeID, ".iOS-")
	var out []int
	for _, p := range strings.Split(v, "-") {
		n, _ := strconv.Atoi(p)
		out = append(out, n)
	}
	return out
}

func newerIOS(a, b string) bool {
	va, vb := iosVersion(a), iosVersion(b)
	for i := 0; i < len(va) && i < len(vb); i++ {
		if va[i] != vb[i] {
			return va[i] > vb[i]
		}
	}
	return len(va) > len(vb)
}

func bootSimulator(ctx context.Context, say func(string)) bool {
	c, cancel := context.WithTimeout(ctx, time.Minute)
	out, err := runTool(c, "xcrun", "simctl", "list", "devices", "available", "-j")
	cancel()
	if err != nil {
		return false
	}
	var parsed struct {
		Devices map[string][]struct {
			UDID  string `json:"udid"`
			Name  string `json:"name"`
			State string `json:"state"`
		} `json:"devices"`
	}
	if json.Unmarshal(out, &parsed) != nil {
		return false
	}
	runtimes := make([]string, 0, len(parsed.Devices))
	for rt := range parsed.Devices {
		if strings.Contains(rt, ".iOS-") {
			runtimes = append(runtimes, rt)
		}
	}
	sort.Slice(runtimes, func(i, j int) bool { return newerIOS(runtimes[i], runtimes[j]) })
	udid, name := "", ""
	for _, rt := range runtimes {
		for _, d := range parsed.Devices[rt] {
			if d.State == "Shutdown" && strings.HasPrefix(d.Name, "iPhone") {
				udid, name = d.UDID, d.Name
				break
			}
		}
		if udid != "" {
			break
		}
	}
	if udid == "" {
		return false
	}
	say(fmt.Sprintf("no device is running: booting the iOS simulator %s, without its window (it keeps running after the test)", name))
	c, cancel = context.WithTimeout(ctx, bootLimit)
	defer cancel()
	if _, err := runTool(c, "xcrun", "simctl", "bootstatus", udid, "-b"); err != nil {
		if ctx.Err() == nil {
			say(fmt.Sprintf("the simulator %s did not boot: %v", name, err))
		}
		return false
	}
	return true
}
