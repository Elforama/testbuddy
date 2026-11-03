package flow

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestConfigAllKeys pins every config header key tb reads and where it lands.
func TestConfigAllKeys(t *testing.T) {
	f := mustParse(t, `appId: com.example.app
name: Checkout
tags: [smoke, checkout]
env:
  USER: qa@example.com
  COUNT: 3
  GREETING: ${'hi'}
properties:
  owner: payments
  priority: high
jsEngine: graaljs
customKey:
  nested: 1
onFlowStart:
  - launchApp
  - tapOn: Start
onFlowComplete:
  - takeScreenshot: end
---
- back
`)
	c := f.Config
	if c.AppID != "com.example.app" || c.URL != "" || c.Name != "Checkout" {
		t.Errorf("appId/url/name: %q %q %q", c.AppID, c.URL, c.Name)
	}
	if !reflect.DeepEqual(c.Tags, []string{"smoke", "checkout"}) {
		t.Errorf("tags: %v", c.Tags)
	}
	wantEnv := []KV{{"USER", "qa@example.com"}, {"COUNT", "3"}, {"GREETING", "${'hi'}"}}
	if !reflect.DeepEqual(c.Env, wantEnv) {
		t.Errorf("env (ordered): %v", c.Env)
	}
	if !reflect.DeepEqual(c.Properties, map[string]string{"owner": "payments", "priority": "high"}) {
		t.Errorf("properties: %v", c.Properties)
	}
	if !reflect.DeepEqual(c.PropertyOrder, []string{"owner", "priority"}) {
		t.Errorf("property order: %v", c.PropertyOrder)
	}
	if c.Ext["jsEngine"] != "graaljs" {
		t.Errorf("jsEngine ext: %v", c.Ext["jsEngine"])
	}
	if !reflect.DeepEqual(c.Ext["customKey"], map[string]any{"nested": 1}) {
		t.Errorf("unknown keys keep their decoded value in Ext: %#v", c.Ext["customKey"])
	}
	if _, ok := c.Ext["appId"]; ok {
		t.Error("known keys must not leak into Ext")
	}
	if got := names(c.OnFlowStart); !reflect.DeepEqual(got, []string{"launchApp", "tapOn"}) {
		t.Errorf("onFlowStart: %v", got)
	}
	if len(c.OnFlowComplete) != 1 || c.OnFlowComplete[0].Text != "end" {
		t.Errorf("onFlowComplete: %+v", c.OnFlowComplete)
	}
	if c.OnFlowStart[1].Line != 16 {
		t.Errorf("hook commands keep their line: %d", c.OnFlowStart[1].Line)
	}
	if len(c.Devices) != 0 {
		t.Errorf("single-device flow has no roles: %v", c.Devices)
	}
}

// TestConfigURLAndAppID pins that a web flow's url becomes its AppID, and wins over appId.
func TestConfigURLAndAppID(t *testing.T) {
	for _, tc := range []struct {
		name, src, appID, url string
	}{
		{"appId only", "appId: com.x\n", "com.x", ""},
		{"url only", "url: https://example.com/app\n", "https://example.com/app", "https://example.com/app"},
		{"url wins over appId", "appId: com.x\nurl: https://e.com\n", "https://e.com", "https://e.com"},
		{"url before appId still wins", "url: https://e.com\nappId: com.x\n", "https://e.com", "https://e.com"},
		{"appId with env interpolation", "appId: ${APP_ID}\n", "${APP_ID}", ""},
		{"url with env interpolation", "url: ${URL}\n", "${URL}", "${URL}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := mustParse(t, tc.src+"---\n- back\n").Config
			if c.AppID != tc.appID || c.URL != tc.url {
				t.Errorf("AppID %q URL %q, want %q %q", c.AppID, c.URL, tc.appID, tc.url)
			}
			// The header's url: makes a web flow whatever it resolves to (maestro#2383).
			if c.Web() != (tc.url != "") {
				t.Errorf("Web() = %v", c.Web())
			}
		})
	}
}

// TestConfigEmptyOptionalKeys pins that null tags/env/hooks are accepted and stay empty.
func TestConfigEmptyOptionalKeys(t *testing.T) {
	c := mustParse(t, "appId: a\ntags:\nenv:\nonFlowStart:\nonFlowComplete:\nproperties: nope\n---\n- back\n").Config
	if c.Tags != nil || c.Env != nil || c.OnFlowStart != nil || c.OnFlowComplete != nil {
		t.Errorf("null keys should leave fields empty: %+v", c)
	}
	if c.Properties == nil || len(c.Properties) != 0 || c.Ext == nil {
		t.Errorf("Properties/Ext are always non-nil maps; a non-map properties is ignored: %+v %+v", c.Properties, c.Ext)
	}
}

// TestConfigErrors pins the config section's rejections, with messages and lines.
func TestConfigErrors(t *testing.T) {
	runErrCases(t, "", []errCase{
		{"no appId or url", "name: x\n---\n- back\n", "Either 'url' or 'appId' must be specified", 1},
		{"empty appId", "appId: \"\"\n---\n- back\n", "Either 'url' or 'appId'", 1},
		{"non-scalar appId", "appId: [a]\n---\n- back\n", "Either 'url' or 'appId'", 1},
		{"tags not a list", "appId: a\ntags: smoke\n---\n- back\n", "tags must be a list", 2},
		{"env not a map", "appId: a\nenv: [A]\n---\n- back\n", "env must be a map of names to values", 2},
		{"env value not a string", "appId: a\nenv:\n  A: [1]\n---\n- back\n", "env value for A must be a string", 3},
		{"env without value", "appId: a\nenv:\n  K:\n---\n- back\n", "Environment variable K has no value", 3},
		{"onFlowStart not a list", "appId: a\nonFlowStart: launchApp\n---\n- back\n", "onFlowStart must be a list of commands", 2},
		{"onFlowComplete not a list", "appId: a\nonFlowComplete: {back: x}\n---\n- back\n", "onFlowComplete must be a list of commands", 2},
		{"bad command in onFlowStart", "appId: a\nonFlowStart:\n  - tapOnn: X\n---\n- back\n", "Did you mean tapOn", 3},
		{"bad command in onFlowComplete", "appId: a\nonFlowComplete:\n  - frob\n---\n- back\n", "Invalid Command", 3},
		{"rhino engine removed", "appId: a\njsEngine: rhino\n---\n- back\n", "Rhino JS engine has been removed", 2},
		{"rhino engine any case", "appId: a\njsEngine: Rhino\n---\n- back\n", "Rhino JS engine has been removed", 2},
	})
}

// TestDocumentStructure pins the two-document shape: config mapping, '---', command list.
func TestDocumentStructure(t *testing.T) {
	runErrCases(t, "", []errCase{
		{"empty file", "", "Config Section Required", 0},
		{"only commands", "- launchApp\n", "Config Section Required", 1},
		{"scalar config", "hello\n---\n- back\n", "Config Section Required", 1},
		{"config without commands", "appId: a\n", "Commands Section Required", 0},
		{"empty commands document", "appId: a\n---\n", "Commands Section Required", 0},
		{"commands as a map", "appId: a\n---\ntapOn: X\n", "Commands Section Required", 3},
		{"commands as a scalar", "appId: a\n---\nback\n", "Commands Section Required", 3},
	})

	t.Run("leading separator", func(t *testing.T) {
		f := mustParse(t, "---\nappId: a\n---\n- back\n")
		if f.Config.AppID != "a" || len(f.Commands) != 1 {
			t.Fatalf("%+v", f)
		}
	})
	t.Run("empty command list", func(t *testing.T) {
		f := mustParse(t, "appId: a\n---\n[]\n")
		if len(f.Commands) != 0 {
			t.Fatalf("%+v", f.Commands)
		}
	})
	t.Run("documents after the commands are ignored", func(t *testing.T) {
		f := mustParse(t, "appId: a\n---\n- back\n---\n- tapOn: ignored\n")
		if len(f.Commands) != 1 || f.Commands[0].Name != "back" {
			t.Fatalf("%v", names(f.Commands))
		}
	})
	t.Run("comments are fine", func(t *testing.T) {
		f := mustParse(t, "# header\nappId: a # the app\n---\n# steps\n- back # go back\n")
		if f.Config.AppID != "a" || len(f.Commands) != 1 {
			t.Fatalf("%+v", f)
		}
	})
}

// TestYAMLSyntaxError pins that malformed YAML is a *Error without the "yaml: " prefix.
func TestYAMLSyntaxError(t *testing.T) {
	e := parseErr(t, "appId: [a\n---\n- back\n")
	if strings.HasPrefix(e.Msg, "yaml:") || e.Msg == "" {
		t.Errorf("msg %q", e.Msg)
	}
	if e.Line != 0 || !strings.HasSuffix(e.File, "flow.yaml") {
		t.Errorf("file/line: %q %d", e.File, e.Line)
	}
}

// TestErrorString pins Error's "file:line: msg" format (line omitted when unknown).
func TestErrorString(t *testing.T) {
	if got := (&Error{File: "a.yaml", Line: 3, Msg: "boom"}).Error(); got != "a.yaml:3: boom" {
		t.Errorf("with line: %q", got)
	}
	if got := (&Error{File: "a.yaml", Msg: "boom"}).Error(); got != "a.yaml: boom" {
		t.Errorf("without line: %q", got)
	}
	e := parseErr(t, hdr+"- tapOn: {txt: X}\n")
	if !strings.HasSuffix(e.File, string(filepath.Separator)+"flow.yaml") || !filepath.IsAbs(e.File) {
		t.Errorf("errors carry the absolute file: %q", e.File)
	}
	if !strings.Contains(e.Error(), "flow.yaml:3: Unknown Property") {
		t.Errorf("full text: %q", e.Error())
	}
}

// TestFlowName pins Flow.Name: the config name, else the file name without its extension.
func TestFlowName(t *testing.T) {
	for _, tc := range []struct {
		name string
		f    Flow
		want string
	}{
		{"config name wins", Flow{Path: "/x/login.yaml", Config: Config{Name: "Log in"}}, "Log in"},
		{"file name", Flow{Path: "/x/y/login.yaml"}, "login"},
		{"yml extension", Flow{Path: "/x/checkout.yml"}, "checkout"},
		{"only the last extension", Flow{Path: "/x/checkout.flow.yaml"}, "checkout.flow"},
		{"no extension", Flow{Path: "/x/README"}, "README"},
		{"inline flow", Flow{}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.f.Name(); got != tc.want {
				t.Errorf("Name() = %q, want %q", got, tc.want)
			}
		})
	}

	dir := writeFiles(t, map[string]string{"Sign In.yaml": hdr + "- back\n"})
	f, err := ParseFile(filepath.Join(dir, "Sign In.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if f.Name() != "Sign In" {
		t.Errorf("parsed name: %q", f.Name())
	}
}

// TestParseFilePaths pins that ParseFile records absolute Path/Dir, even for relative input.
func TestParseFilePaths(t *testing.T) {
	dir := writeFiles(t, map[string]string{"flow.yaml": hdr + "- back\n"})
	t.Chdir(dir)
	f, err := ParseFile("flow.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(f.Path) || filepath.Base(f.Path) != "flow.yaml" || f.Dir != filepath.Dir(f.Path) {
		t.Errorf("Path %q Dir %q", f.Path, f.Dir)
	}
	if f.Commands[0].File != f.Path || f.Commands[0].Line != 3 {
		t.Errorf("command location: %s:%d", f.Commands[0].File, f.Commands[0].Line)
	}
}

// TestParseFileMissing pins that a missing file is the OS error, not a *Error.
func TestParseFileMissing(t *testing.T) {
	_, err := ParseFile(filepath.Join(t.TempDir(), "nope.yaml"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("got %v", err)
	}
}

// TestParseBytes pins that ParseBytes parses text as if it were the file at path.
func TestParseBytes(t *testing.T) {
	dir := writeFiles(t, map[string]string{"sub.yaml": hdr + "- tapOn: Sub\n"})
	virtual := filepath.Join(dir, "virtual.yaml") // never written
	f, err := ParseBytes(virtual, []byte(hdr+"- runFlow: sub.yaml\n"))
	if err != nil {
		t.Fatal(err)
	}
	if f.Path != virtual || f.Dir != dir {
		t.Errorf("Path %q Dir %q", f.Path, f.Dir)
	}
	if c := f.Commands[0]; c.SubFile != filepath.Join(dir, "sub.yaml") || c.SubFlow == nil {
		t.Errorf("relative runFlow resolves next to path: %+v", c)
	}
	if _, err := ParseBytes(virtual, []byte("- back\n")); err == nil {
		t.Error("ParseBytes still requires a config section")
	}
	// Running itself is caught by comparing paths, before the file would be re-parsed.
	if err := os.WriteFile(virtual, []byte(hdr+"- back\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = ParseBytes(virtual, []byte(hdr+"- runFlow: virtual.yaml\n"))
	if e := asFlowErr(t, err); !strings.Contains(e.Msg, "a flow cannot run itself") {
		t.Errorf("self-run: %v", e)
	}
}

// TestParseCommands pins the header-less command parsing used by `tb do`.
func TestParseCommands(t *testing.T) {
	dir := writeFiles(t, map[string]string{"sub.yaml": hdr + "- back\n"})
	for _, tc := range []struct {
		name, src string
		want      []string
	}{
		{"list", "- back\n- tapOn: X\n", []string{"back", "tapOn"}},
		{"single map without dash", "tapOn: Login\n", []string{"tapOn"}},
		{"single bare command", "back\n", []string{"back"}},
		{"flow-style list", "[back, hideKeyboard]\n", []string{"back", "hideKeyboard"}},
		{"runFlow relative to dir", "runFlow: sub.yaml\n", []string{"runFlow"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cs, err := ParseCommands(dir, []byte(tc.src))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(names(cs), tc.want) {
				t.Errorf("got %v want %v", names(cs), tc.want)
			}
			for _, c := range cs {
				if c.File != filepath.Join(dir, "<commands>") {
					t.Errorf("File %q", c.File)
				}
				if c.Name == "runFlow" && c.SubFile != filepath.Join(dir, "sub.yaml") {
					t.Errorf("SubFile %q", c.SubFile)
				}
			}
		})
	}

	t.Run("empty input", func(t *testing.T) {
		cs, err := ParseCommands(dir, nil)
		if err != nil || cs != nil {
			t.Fatalf("got %v %v", cs, err)
		}
	})
	t.Run("bad yaml", func(t *testing.T) {
		_, err := ParseCommands(dir, []byte("tapOn: [\n"))
		if e := asFlowErr(t, err); e.File != "<commands>" || strings.HasPrefix(e.Msg, "yaml:") {
			t.Errorf("%+v", e)
		}
	})
	t.Run("unknown command", func(t *testing.T) {
		_, err := ParseCommands(dir, []byte("- back\n- tapOnn: X\n"))
		e := asFlowErr(t, err)
		if e.Line != 2 || e.File != filepath.Join(dir, "<commands>") || !strings.Contains(e.Msg, "Did you mean tapOn") {
			t.Errorf("%+v", e)
		}
	})
	t.Run("parallel needs a multi-device flow", func(t *testing.T) {
		_, err := ParseCommands(dir, []byte("parallel: {alice: [back]}\n"))
		if e := asFlowErr(t, err); !strings.Contains(e.Msg, "only for multi-device flows") {
			t.Errorf("%v", e)
		}
	})
	t.Run("two keys in one command", func(t *testing.T) {
		_, err := ParseCommands(dir, []byte("tapOn: X\noptional: true\n"))
		if e := asFlowErr(t, err); !strings.Contains(e.Msg, `unexpected top-level field "optional"`) {
			t.Errorf("%v", e)
		}
	})
}
