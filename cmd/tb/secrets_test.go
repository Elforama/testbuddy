package main

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const fakeSecret = "fake.secret-0451"

func TestDoSecretFlag(t *testing.T) {
	f := startFakeDaemon(t)
	t.Setenv("TB_TEST_OTP", "fake-otp-7731")
	var code int
	_, stderr := capture(t, func() {
		code = cmdDo([]string{"--secret", "PW=" + fakeSecret, "--secret", "TB_TEST_OTP", "-e", "USER=sam", "inputText: ${PW}"})
	})
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	_, args, _ := f.last(t)
	want := map[string]any{"PW": fakeSecret, "TB_TEST_OTP": "fake-otp-7731"}
	if !reflect.DeepEqual(args["secrets"], want) || !reflect.DeepEqual(args["env"], map[string]any{"USER": "sam"}) {
		t.Errorf("secrets %v, env %v", args["secrets"], args["env"])
	}
	if strings.Contains(args["commands"].(string), fakeSecret) {
		t.Errorf("commands carry the value: %v", args["commands"])
	}
}

func TestDoSecretFlagNeedsAValue(t *testing.T) {
	startFakeDaemon(t)
	for spec, want := range map[string]string{
		"TB_TEST_UNSET":           "TB_TEST_UNSET is not set in the environment",
		"PW=":                     "--secret PW has no value",
		"=" + fakeSecret:          "--secret takes NAME=value or NAME",
		"my-pw=" + fakeSecret:     "NAME is letters, digits and _",
		fakeSecret + "=x":         "--secret takes NAME=value or NAME",
		"1PW=" + fakeSecret + "x": "--secret takes NAME=value or NAME",
	} {
		var code int
		_, stderr := capture(t, func() { code = cmdDo([]string{"--secret", spec, "inputText: ${PW}"}) })
		if code != 2 || !strings.Contains(stderr, want) || strings.Contains(stderr, fakeSecret) {
			t.Errorf("--secret %s: exit %d, stderr %q", spec, code, stderr)
		}
	}
	r := mcpCall(context.Background(), "run", map[string]any{"commands": "inputText: hi", "secrets": []any{"=" + fakeSecret}})
	if text := r["content"].([]map[string]any)[0]["text"].(string); r["isError"] != true || strings.Contains(text, fakeSecret) {
		t.Errorf("MCP run with a nameless secret: %v", r)
	}
}

func TestMCPRunSecrets(t *testing.T) {
	f := startFakeDaemon(t)
	r := mcpCall(context.Background(), "run", map[string]any{"commands": "inputText: ${PW}", "secrets": []any{"PW=" + fakeSecret}})
	if r["isError"] != false {
		t.Fatalf("result %v", r)
	}
	if _, args, _ := f.last(t); !reflect.DeepEqual(args["secrets"], map[string]any{"PW": fakeSecret}) {
		t.Errorf("secrets %v", args["secrets"])
	}
	r = mcpCall(context.Background(), "run", map[string]any{"commands": "inputText: ${PW}", "secrets": []any{"TB_TEST_UNSET"}})
	if r["isError"] != true || !strings.Contains(r["content"].([]map[string]any)[0]["text"].(string), "TB_TEST_UNSET is not set") {
		t.Errorf("unset secret: %v", r)
	}
}

func TestSavedSecretFlowReplaysWithEnv(t *testing.T) {
	app := withFakePhone(t)
	dir := t.TempDir()
	path := writeFlow(t, dir, "sign-in.yaml", "appId: com.example.app\n---\n- launchApp\n- inputText: ${PW}\n- assertVisible: Home\n")
	var code int
	out, stderr := capture(t, func() {
		code = cmdReplay([]string{path, "-d", app.serial, "-e", "PW=" + fakeSecret, "--debug-output", filepath.Join(dir, "debug")})
	})
	if code != 0 || !reflect.DeepEqual(app.typed, []string{fakeSecret}) {
		t.Fatalf("exit %d, typed %q\n%s\n%s", code, app.typed, out, stderr)
	}
}
