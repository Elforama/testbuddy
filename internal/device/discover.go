// Package device finds the Android devices and iOS simulators this machine can drive.
package device

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/elforama/testbuddy/internal/ci"
)

type Platform string

const (
	Android Platform = "android"
	IOS     Platform = "ios"
	Web     Platform = "web"
)

// WebAvailable reports whether a browser for web flows exists (set by the web driver).
var WebAvailable = func() bool { return false }

// Info describes one device that is connected and ready (an Android device in the
// "device" state, or a booted iOS simulator).
type Info struct {
	Platform Platform `json:"platform"`
	Serial   string   `json:"serial"` // adb serial or simulator UDID
	Name     string   `json:"name"`
	Model    string   `json:"model,omitempty"`
	Emulator bool     `json:"emulator"`
	State    string   `json:"state"`
	Headless bool     `json:"-"` // start this browser session without a window
}

func (i Info) String() string {
	kind := "device"
	if i.Platform == Web {
		return fmt.Sprintf("web %s %q (browser)", i.Serial, i.Name)
	}
	if i.Emulator {
		kind = "emulator"
		if i.Platform == IOS {
			kind = "simulator"
		}
	}
	return fmt.Sprintf("%s %s %q (%s)", i.Platform, i.Serial, i.Name, kind)
}

// ADB returns the path to adb, preferring ANDROID_HOME and the default SDK location.
func ADB() string {
	exe := "adb"
	if runtime.GOOS == "windows" {
		exe = "adb.exe"
	}
	for _, env := range []string{"ANDROID_HOME", "ANDROID_SDK_ROOT"} {
		if root := os.Getenv(env); root != "" {
			p := filepath.Join(root, "platform-tools", exe)
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	if p, err := exec.LookPath("adb"); err == nil {
		return p
	}
	home, _ := os.UserHomeDir()
	var guess string
	switch runtime.GOOS {
	case "darwin":
		guess = filepath.Join(home, "Library", "Android", "sdk", "platform-tools", exe)
	case "windows": // Android Studio's default SDK folder
		local := os.Getenv("LOCALAPPDATA")
		if local == "" {
			local = filepath.Join(home, "AppData", "Local")
		}
		guess = filepath.Join(local, "Android", "Sdk", "platform-tools", exe)
	default:
		guess = filepath.Join(home, "Android", "Sdk", "platform-tools", exe)
	}
	return guess
}

// List returns every ready device, Android first, then iOS. Errors from one
// platform's tooling (adb missing, not on macOS) are ignored so the other still works.
func List(ctx context.Context) []Info {
	var out []Info
	out = append(out, listAndroid(ctx)...)
	out = append(out, listIOS(ctx)...)
	if WebAvailable() {
		out = append(out, WebDevice(1))
	}
	return out
}

// WebDevice is the n-th browser device. Browsers are made on demand, so a flow with two
// web roles gets two.
func WebDevice(n int) Info {
	if n <= 1 {
		return Info{Platform: Web, Serial: "web", Name: "Chrome", State: "available"}
	}
	return Info{Platform: Web, Serial: fmt.Sprintf("web-%d", n), Name: fmt.Sprintf("Chrome %d", n), State: "available"}
}

// IsURL reports whether an app id is a web address (a web flow's url:).
func IsURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "file://") ||
		strings.HasPrefix(s, "about:") || strings.HasPrefix(s, "data:")
}

func listAndroid(ctx context.Context) []Info {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	b, err := exec.CommandContext(ctx, ADB(), "devices", "-l").Output()
	if err != nil {
		return nil
	}
	var out []Info
	sc := bufio.NewScanner(strings.NewReader(string(b)))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "List of devices") || strings.HasPrefix(line, "*") {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		info := Info{Platform: Android, Serial: f[0], State: f[1], Emulator: strings.HasPrefix(f[0], "emulator-")}
		for _, kv := range f[2:] {
			if k, v, ok := strings.Cut(kv, ":"); ok {
				switch k {
				case "model":
					info.Model = v
				case "device":
					if info.Name == "" {
						info.Name = v
					}
				}
			}
		}
		if info.Model != "" {
			info.Name = strings.ReplaceAll(info.Model, "_", " ")
		}
		if info.State == "device" {
			out = append(out, info)
		}
	}
	return out
}

// listTimeout is how long simctl may take to list the booted simulators; ciListTimeout is
// that in CI (package ci), where a busy runner took over 10 s right after a run.
var listTimeout, ciListTimeout = 10 * time.Second, 60 * time.Second

func listIOS(ctx context.Context) []Info {
	if runtime.GOOS != "darwin" {
		return nil
	}
	limit := listTimeout
	if ci.On() {
		limit = ciListTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	b, err := exec.CommandContext(ctx, "xcrun", "simctl", "list", "devices", "booted", "-j").Output()
	if err != nil {
		return nil
	}
	var parsed struct {
		Devices map[string][]struct {
			UDID       string `json:"udid"`
			Name       string `json:"name"`
			State      string `json:"state"`
			DeviceType string `json:"deviceTypeIdentifier"`
		} `json:"devices"`
	}
	if json.Unmarshal(b, &parsed) != nil {
		return nil
	}
	var out []Info
	runtimes := make([]string, 0, len(parsed.Devices))
	for rt := range parsed.Devices {
		runtimes = append(runtimes, rt)
	}
	sort.Strings(runtimes)
	for _, rt := range runtimes {
		for _, d := range parsed.Devices[rt] {
			if d.State != "Booted" {
				continue
			}
			model := strings.TrimPrefix(d.DeviceType, "com.apple.CoreSimulator.SimDeviceType.")
			out = append(out, Info{Platform: IOS, Serial: d.UDID, Name: d.Name, Model: model, Emulator: true, State: "booted"})
		}
	}
	return out
}

// Pick chooses a device. want may be empty, a serial/UDID, or a device name
// (case-insensitive). platform may be empty to allow either.
func Pick(devs []Info, platform Platform, want string) (Info, error) {
	var cands []Info
	for _, d := range devs {
		if platform != "" && d.Platform != platform {
			continue
		}
		cands = append(cands, d)
	}
	if want != "" {
		for _, d := range cands {
			if d.Serial == want || strings.EqualFold(d.Name, want) {
				return d, nil
			}
		}
		return Info{}, fmt.Errorf("no connected device matches %q (see `tb devices`)", want)
	}
	if len(cands) == 0 {
		if platform != "" {
			return Info{}, fmt.Errorf("no %s device is connected: start an emulator or simulator first", platform)
		}
		return Info{}, fmt.Errorf("no device is connected: start an Android emulator or boot an iOS simulator first")
	}
	return cands[0], nil
}

// AppInstalled reports whether the app (package or bundle id) is installed on the device.
// It answers true when it cannot tell, so a failed check never hides a device.
func AppInstalled(ctx context.Context, d Info, appID string) bool {
	if d.Platform == Web {
		return IsURL(appID)
	}
	if IsURL(appID) {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	switch d.Platform {
	case Android:
		out, err := exec.CommandContext(ctx, ADB(), "-s", d.Serial, "shell", "pm", "path", appID).Output()
		if err != nil {
			return len(out) > 0 || ctx.Err() != nil
		}
		return strings.Contains(string(out), "package:")
	case IOS:
		err := exec.CommandContext(ctx, "xcrun", "simctl", "get_app_container", d.Serial, appID).Run()
		if err == nil {
			return true
		}
		_, isExit := err.(*exec.ExitError)
		return !isExit
	}
	return true
}
