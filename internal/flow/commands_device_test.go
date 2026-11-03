package flow

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// ---- app lifecycle ----

// TestLaunchAppForms pins launchApp's bare, scalar and full map forms.
func TestLaunchAppForms(t *testing.T) {
	c := step(t, "- launchApp\n")
	if c.AppID != "" || c.ClearState || c.ClearKeychain || c.StopApp != nil || c.Permissions != nil || c.Arguments != nil {
		t.Errorf("bare: %+v", c)
	}
	if c := step(t, "- launchApp: com.other\n"); c.AppID != "com.other" {
		t.Errorf("scalar: %q", c.AppID)
	}
	c = step(t, `- launchApp:
    appId: com.example.app
    clearState: true
    clearKeychain: true
    stopApp: false
    label: start fresh
    optional: true
    permissions:
      all: deny
      camera: allow
      notifications: unset
    arguments:
      count: 3
      big: 0x10
      flag: true
      ratio: 1.5
      name: tester
      quoted: "42"
      list: [1, 2]
`)
	if c.AppID != "com.example.app" || !c.ClearState || !c.ClearKeychain || c.StopApp == nil || *c.StopApp ||
		c.Label != "start fresh" || !c.Optional {
		t.Errorf("options: %+v", c)
	}
	wantPerms := []KV{{"all", "deny"}, {"camera", "allow"}, {"notifications", "unset"}}
	if !reflect.DeepEqual(c.Permissions, wantPerms) {
		t.Errorf("permissions (ordered): %v", c.Permissions)
	}
	wantArgs := []ArgKV{
		{"count", int64(3)}, {"big", int64(16)}, {"flag", true}, {"ratio", 1.5}, {"name", "tester"},
		{"quoted", "42"}, {"list", []any{1, 2}},
	}
	if !reflect.DeepEqual(c.Arguments, wantArgs) {
		t.Errorf("arguments keep their YAML types:\n got %#v\nwant %#v", c.Arguments, wantArgs)
	}
	if c := step(t, "- launchApp: {stopApp: true}\n"); c.StopApp == nil || !*c.StopApp {
		t.Errorf("stopApp true: %v", c.StopApp)
	}
}

// TestLaunchAppURL pins that a web launchApp's url overrides its appId.
func TestLaunchAppURL(t *testing.T) {
	if c := step(t, "- launchApp: {url: https://e.com, appId: com.x}\n"); c.AppID != "https://e.com" {
		t.Errorf("url wins: %q", c.AppID)
	}
	if c := step(t, "- launchApp: {url: \"\", appId: com.x}\n"); c.AppID != "com.x" {
		t.Errorf("empty url keeps appId: %q", c.AppID)
	}
}

// TestLaunchAppErrors pins launchApp's rejections.
func TestLaunchAppErrors(t *testing.T) {
	runErrCases(t, hdr, []errCase{
		{"clearState not a bool", "- launchApp: {clearState: maybe}\n", "expected true or false", 3},
		{"clearKeychain not a bool", "- launchApp: {clearKeychain: 2}\n", "expected true or false", 3},
		{"stopApp not a bool", "- launchApp: {stopApp: never}\n", "expected true or false", 3},
		{"permissions a list", "- launchApp:\n    permissions: [camera]\n", "expected a map", 4},
		{"arguments a list", "- launchApp:\n    arguments: [a]\n", "arguments must be a map", 4},
		{"unknown key", "- launchApp: {appID: com.x}\n", `"appID" is not an option of launchApp`, 3},
		{"bad optional", "- launchApp: {optional: sometimes}\n", "expected true or false", 3},
	})
}

// TestStopKillClear pins stopApp/killApp/clearState forms.
func TestStopKillClear(t *testing.T) {
	for _, name := range []string{"stopApp", "killApp", "clearState"} {
		t.Run(name, func(t *testing.T) {
			if c := step(t, "- "+name+"\n"); c.AppID != "" {
				t.Errorf("bare: %q", c.AppID)
			}
			if c := step(t, "- "+name+": com.other\n"); c.AppID != "com.other" {
				t.Errorf("scalar: %q", c.AppID)
			}
			c := step(t, "- "+name+": {appId: com.m, label: L, optional: true}\n")
			if c.AppID != "com.m" || c.Label != "L" || !c.Optional {
				t.Errorf("map: %+v", c)
			}
			e := parseErr(t, hdr+"- "+name+": {app: x}\n")
			if !strings.Contains(e.Msg, `"app" is not an option of `+name) {
				t.Errorf("unknown key: %v", e)
			}
		})
	}
}

// TestSetPermissions pins setPermissions's map form.
func TestSetPermissions(t *testing.T) {
	c := step(t, "- setPermissions:\n    appId: com.x\n    permissions:\n      location: allow\n      camera: deny\n")
	if c.AppID != "com.x" || !reflect.DeepEqual(c.Permissions, []KV{{"location", "allow"}, {"camera", "deny"}}) {
		t.Errorf("%+v", c)
	}
	if c.Mutating() {
		t.Error("setPermissions is not a screen interaction")
	}
	if c := step(t, "- setPermissions: {url: https://e.com, appId: com.x, permissions: {all: allow}}\n"); c.AppID != "https://e.com" {
		t.Errorf("url wins: %q", c.AppID)
	}
	if c := step(t, "- setPermissions: {label: L}\n"); c.Permissions != nil || c.Label != "L" {
		t.Errorf("no permissions: %+v", c)
	}
	runErrCases(t, hdr, []errCase{
		{"scalar", "- setPermissions: all\n", "setPermissions takes a map with permissions", 3},
		{"permissions a list", "- setPermissions: {permissions: [camera]}\n", "expected a map", 3},
		{"unknown key", "- setPermissions: {perms: {all: allow}}\n", `"perms" is not an option of setPermissions`, 3},
	})
}

// TestOpenLink pins openLink's scalar and map forms.
func TestOpenLink(t *testing.T) {
	if c := step(t, "- openLink: https://example.com/a?b=c\n"); c.Text != "https://example.com/a?b=c" || c.AutoVerify || c.Browser {
		t.Errorf("scalar: %+v", c)
	}
	c := step(t, "- openLink:\n    link: myapp://home\n    autoVerify: true\n    browser: true\n    label: deep link\n")
	if c.Text != "myapp://home" || !c.AutoVerify || !c.Browser || c.Label != "deep link" {
		t.Errorf("map: %+v", c)
	}
	runErrCases(t, hdr, []errCase{
		{"autoVerify not a bool", "- openLink: {link: x, autoVerify: y}\n", "expected true or false", 3},
		{"browser not a bool", "- openLink: {link: x, browser: chrome}\n", "expected true or false", 3},
		{"unknown key", "- openLink: {url: x}\n", `"url" is not an option of openLink`, 3},
	})
}

// ---- screenshots and recordings ----

// TestScreenshotCommands pins takeScreenshot/startRecording/assertScreenshot fields.
func TestScreenshotCommands(t *testing.T) {
	if c := step(t, "- takeScreenshot: home\n"); c.Text != "home" || c.CropOn != nil || c.Threshold != "" || c.Mutating() {
		t.Errorf("takeScreenshot scalar: %+v", c)
	}
	c := step(t, "- takeScreenshot:\n    path: shots/home\n    cropOn: {id: header}\n    label: L\n")
	if c.Text != "shots/home" || c.CropOn == nil || c.CropOn.ID != "header" || c.Label != "L" {
		t.Errorf("takeScreenshot map: %+v", c)
	}
	if c := step(t, "- startRecording: {path: rec, optional: true}\n"); c.Text != "rec" || !c.Optional {
		t.Errorf("startRecording: %+v", c)
	}
	if c := step(t, "- assertScreenshot: golden\n"); c.Text != "golden" || c.Threshold != "95" {
		t.Errorf("assertScreenshot default threshold: %+v", c)
	}
	c = step(t, "- assertScreenshot: {path: golden, thresholdPercentage: 90, cropOn: Title}\n")
	if c.Threshold != "90" || c.CropOn.Text != "Title" {
		t.Errorf("assertScreenshot map: %+v", c)
	}
	runErrCases(t, hdr, []errCase{
		{"startRecording has no cropOn", "- startRecording: {path: r, cropOn: X}\n", `"cropOn" is not an option of startRecording`, 3},
		{"threshold only on assertScreenshot", "- takeScreenshot: {path: p, thresholdPercentage: 90}\n", `"thresholdPercentage" is not an option of takeScreenshot`, 3},
		{"unknown key in cropOn", "- takeScreenshot: {path: p, cropOn: {txt: X}}\n", `"txt" is not an option of cropOn`, 3},
		{"unknown key", "- assertScreenshot: {file: p}\n", `"file" is not an option of assertScreenshot`, 3},
	})
}

// TestWaitForAnimationToEnd pins its bare and {timeout} forms.
func TestWaitForAnimationToEnd(t *testing.T) {
	if c := step(t, "- waitForAnimationToEnd\n"); c.Timeout != "" {
		t.Errorf("bare: %q", c.Timeout)
	}
	if c := step(t, "- waitForAnimationToEnd: {timeout: 5000, label: settle}\n"); c.Timeout != "5000" || c.Label != "settle" {
		t.Errorf("map: %+v", c)
	}
	runErrCases(t, hdr, []errCase{
		{"scalar", "- waitForAnimationToEnd: 5000\n", "waitForAnimationToEnd takes no value or {timeout}", 3},
		{"unknown key", "- waitForAnimationToEnd: {wait: 1}\n", `"wait" is not an option of waitForAnimationToEnd`, 3},
	})
}

// ---- location ----

// TestSetLocation pins setLocation's fields (kept as raw strings for ${...}).
func TestSetLocation(t *testing.T) {
	c := step(t, "- setLocation:\n    latitude: 52.3676\n    longitude: ${LON}\n    label: Amsterdam\n")
	if c.Latitude != "52.3676" || c.Longitude != "${LON}" || c.Label != "Amsterdam" {
		t.Errorf("%+v", c)
	}
	runErrCases(t, hdr, []errCase{
		{"scalar", "- setLocation: \"1,2\"\n", "setLocation takes {latitude, longitude}", 3},
		{"no longitude", "- setLocation: {latitude: 1}\n", "setLocation needs latitude and longitude", 3},
		{"no latitude", "- setLocation: {longitude: 1}\n", "setLocation needs latitude and longitude", 3},
		{"unknown key", "- setLocation: {latitude: 1, longitude: 2, altitude: 3}\n", `"altitude" is not an option of setLocation`, 3},
	})
}

// TestTravel pins travel's points and speed.
func TestTravel(t *testing.T) {
	c := step(t, "- travel:\n    points:\n      - \"52.37, 4.89\"\n      - \"-33.86,151.21\"\n    speed: 7.5\n")
	if !reflect.DeepEqual(c.Points, [][2]float64{{52.37, 4.89}, {-33.86, 151.21}}) || c.TravelSpeed != 7.5 {
		t.Errorf("%+v %v", c.Points, c.TravelSpeed)
	}
	if c := step(t, "- travel: {points: [\"1,2\"]}\n"); c.TravelSpeed != 4.0 {
		t.Errorf("default speed: %v", c.TravelSpeed)
	}
	runErrCases(t, hdr, []errCase{
		{"scalar", "- travel: somewhere\n", "travel takes {points, speed}", 3},
		{"no points", "- travel: {speed: 3}\n", "travel needs points", 3},
		{"points not a list", "- travel: {points: \"1,2\"}\n", "travel needs points", 3},
		{"point without comma", "- travel:\n    points: [\"1,2\", \"12\"]\n", `a travel point is "latitude,longitude"`, 4},
		{"point not numeric", "- travel: {points: [\"a,b\"]}\n", `a travel point is "latitude,longitude"`, 3},
		{"speed not a number", "- travel:\n    points: [\"1,2\"]\n    speed: fast\n", "speed is in metres per second", 5},
		{"unknown key", "- travel: {points: [\"1,2\"], mode: walk}\n", `"mode" is not an option of travel`, 3},
	})
}

// ---- device settings ----

// TestSetOrientation pins accepted orientation spellings, ${...} and its map form.
func TestSetOrientation(t *testing.T) {
	for _, o := range []string{"PORTRAIT", "portrait", "LANDSCAPE_LEFT", "landscapeLeft", "LANDSCAPE_RIGHT",
		"landscape_right", "UPSIDE_DOWN", "upsideDown", "${ORIENTATION}"} {
		if c := step(t, "- setOrientation: "+o+"\n"); c.Text != o {
			t.Errorf("%s: Text %q (raw value kept)", o, c.Text)
		}
	}
	c := step(t, "- setOrientation: {orientation: landscapeLeft, label: turn, optional: true}\n")
	if c.Text != "landscapeLeft" || c.Label != "turn" || !c.Optional {
		t.Errorf("map: %+v", c)
	}
	runErrCases(t, hdr, []errCase{
		{"unknown", "- setOrientation: SIDEWAYS\n", "orientation must be PORTRAIT, LANDSCAPE_LEFT, LANDSCAPE_RIGHT or UPSIDE_DOWN", 3},
		{"map without orientation", "- setOrientation: {label: x}\n", "orientation must be", 3},
		{"list", "- setOrientation: [PORTRAIT]\n", "orientation must be", 3},
	})
}

// TestToggleModes pins setAirplaneMode/setDarkMode values.
func TestToggleModes(t *testing.T) {
	for _, name := range []string{"setAirplaneMode", "setDarkMode"} {
		t.Run(name, func(t *testing.T) {
			if c := step(t, "- "+name+": enabled\n"); !c.Enabled {
				t.Error("enabled")
			}
			if c := step(t, "- "+name+": disabled\n"); c.Enabled {
				t.Error("disabled")
			}
			c := step(t, "- "+name+": {value: enabled, label: L, optional: true}\n")
			if !c.Enabled || c.Label != "L" || !c.Optional {
				t.Errorf("map: %+v", c)
			}
			for _, bad := range []string{"on", "true", "{value: off}", "[enabled]"} {
				e := parseErr(t, hdr+"- "+name+": "+bad+"\n")
				if !strings.Contains(e.Msg, name+" takes enabled or disabled") || e.Line != 3 {
					t.Errorf("%s: %v", bad, e)
				}
			}
		})
	}
}

// TestAddMedia pins addMedia's list and map forms; files must exist and become absolute.
func TestAddMedia(t *testing.T) {
	dir := writeFiles(t, map[string]string{"pic.png": "x", "media/clip.mp4": "y"})
	main := filepath.Join(dir, "main.yaml")
	f, err := ParseBytes(main, []byte(hdr+"- addMedia: [pic.png, media/clip.mp4]\n- addMedia:\n    files: [pic.png]\n    label: L\n"))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{filepath.Join(dir, "pic.png"), filepath.Join(dir, "media", "clip.mp4")}; !reflect.DeepEqual(f.Commands[0].Files, want) {
		t.Errorf("list: %v", f.Commands[0].Files)
	}
	if c := f.Commands[1]; len(c.Files) != 1 || c.Label != "L" {
		t.Errorf("map: %+v", c)
	}
	for _, tc := range []struct{ name, body, want string }{
		{"missing file", "- addMedia: [nope.png]\n", "media file not found: " + filepath.Join(dir, "nope.png")},
		{"scalar", "- addMedia: pic.png\n", "addMedia takes a list of files"},
		{"map without files", "- addMedia: {label: x}\n", "addMedia takes a list of files"},
		{"unknown key", "- addMedia: {files: [pic.png], album: x}\n", `"album" is not an option of addMedia`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseBytes(main, []byte(hdr+tc.body))
			if e := asFlowErr(t, err); !strings.Contains(e.Msg, tc.want) || e.Line != 3 {
				t.Errorf("%v", e)
			}
		})
	}
}

// ---- scripts and AI ----

// TestEvalScript pins evalScript's scalar and map forms.
func TestEvalScript(t *testing.T) {
	if c := step(t, "- evalScript: ${output.n = 1}\n"); c.Text != "${output.n = 1}" {
		t.Errorf("scalar: %+v", c)
	}
	c := step(t, "- evalScript:\n    script: ${output.n = 2}\n    label: set n\n")
	if c.Text != "${output.n = 2}" || c.Label != "set n" {
		t.Errorf("map: %+v", c)
	}
}

// TestAICommands pins the AI steps: optional by default, and their text fields.
func TestAICommands(t *testing.T) {
	if c := step(t, "- assertWithAI: the cart is empty\n"); c.Text != "the cart is empty" || !c.Optional {
		t.Errorf("assertWithAI scalar: %+v", c)
	}
	c := step(t, "- assertWithAI: {assertion: no errors, optional: false, label: L}\n")
	if c.Text != "no errors" || c.Optional || c.Label != "L" {
		t.Errorf("assertWithAI map: %+v", c)
	}
	c = step(t, "- extractTextWithAI: {query: the order number, outputVariable: order}\n")
	if c.Text != "the order number" || c.OutputVariable != "order" || !c.Optional {
		t.Errorf("extractTextWithAI: %+v", c)
	}
	if c := step(t, "- assertNoDefectsWithAI\n"); !c.Optional || c.Mutating() {
		t.Errorf("assertNoDefectsWithAI: %+v", c)
	}
	if c := step(t, "- assertNoDefectsWithAI: {optional: false}\n"); c.Optional {
		t.Errorf("assertNoDefectsWithAI map: %+v", c)
	}
}

// TestListValuesRejected pins that commands taking a text or a map reject a list value.
func TestListValuesRejected(t *testing.T) {
	for _, name := range []string{"openLink", "takeScreenshot", "startRecording", "assertScreenshot", "setClipboard",
		"eraseText", "launchApp", "stopApp", "killApp", "clearState", "evalScript", "assertWithAI", "extractTextWithAI"} {
		t.Run(name, func(t *testing.T) {
			parseErr(t, hdr+"- "+name+": [a, b]\n")
		})
	}
}
