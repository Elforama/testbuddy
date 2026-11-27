package session

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/engine"
	"github.com/elforama/testbuddy/internal/flow"
)

const fakeSecret = "fake.secret-0451"

func secretSession(t *testing.T) (*Service, *fakeDriver) {
	t.Helper()
	s := New()
	f := newFake("e", "E", device.Android)
	field := item{class: "android.widget.EditText", hint: "Password", id: "com.app:id/password", top: 200}
	f.show(field, button("Sign in", 300))
	f.onType = func(typed string) {
		shown := field
		shown.text = typed
		f.show(shown, button("Sign in", 300), label(typed, 400))
	}
	addDev(s, f, "")
	return s, f
}

func leaks(t *testing.T, where, text string) {
	t.Helper()
	if strings.Contains(text, fakeSecret) || strings.Contains(text, regexp.QuoteMeta(fakeSecret)) {
		t.Errorf("%s shows the secret text:\n%s", where, text)
	}
}

func TestSecretIsTypedButNeverShown(t *testing.T) {
	s, f := secretSession(t)
	h := s.handlers()
	resp, _ := call(t, h["do"], map[string]any{"secrets": map[string]string{"PW": fakeSecret}, "json": true,
		"commands": "- inputText: ${PW}\n- assertVisible: {id: nope, text: \"${PW}\"}\n"}, "")
	if !reflect.DeepEqual(f.typed, []string{fakeSecret}) {
		t.Fatalf("typed %q, want the secret's value", f.typed)
	}
	if resp.Exit != 1 || !strings.Contains(resp.Text, "ok Input text ${PW}") || !strings.Contains(resp.Text, "•••") {
		t.Errorf("do output:\n%s", resp.Text)
	}
	leaks(t, "tb do", resp.Text)
	leaks(t, "tb do --json", string(resp.JSON))
	if !json.Valid(resp.JSON) || !strings.Contains(string(resp.JSON), "•••") {
		t.Errorf("do --json: %s", resp.JSON)
	}
	for _, args := range []map[string]any{{}, {"json": true}, {"all": true}} {
		resp, _ := call(t, h["look"], args, "")
		leaks(t, "tb look", resp.Text+string(resp.JSON))
		if !strings.Contains(resp.Text+string(resp.JSON), "•••") {
			t.Errorf("look %v does not mark the hidden text:\n%s%s", args, resp.Text, resp.JSON)
		}
	}
	resp, _ = call(t, h["locate"], map[string]any{"selector": "{id: password}", "json": true}, "")
	leaks(t, "tb locate", resp.Text+string(resp.JSON))
	resp, parts := call(t, h["watch"], map[string]any{"timeout": 50}, "")
	for _, p := range append(parts, resp) {
		leaks(t, "tb watch", p.Text)
	}
}

func TestSecretIsRecordedByName(t *testing.T) {
	s, _ := secretSession(t)
	h := s.handlers()
	call(t, h["do"], map[string]any{"secrets": map[string]string{"PW": fakeSecret}, "app": "com.app",
		"commands": "- launchApp\n- inputText: ${PW}\n"}, "")
	listing, _ := call(t, h["look"], nil, "")
	m := regexp.MustCompile(`(@\d+) Text "•••"`).FindStringSubmatch(listing.Text)
	if m == nil {
		t.Fatalf("no hidden label in the listing:\n%s", listing.Text)
	}
	resp, _ := call(t, h["do"], map[string]any{"commands": "- assertVisible: " + m[1] + "\n- inputText: " + fakeSecret + "\n"}, "")
	if !resp.OK {
		t.Fatalf("do by ref: %+v", resp)
	}
	leaks(t, "tb do by ref", resp.Text)
	want := "- launchApp\n- inputText: ${PW}\n- assertVisible: {text: \"${PW}\", below: \"Sign in\"}\n- inputText: ${PW}\n"
	if resp, _ := call(t, h["log"], nil, ""); resp.Text != want {
		t.Errorf("log:\n%s\nwant:\n%s", resp.Text, want)
	}
	cwd := t.TempDir()
	resp, _ = call(t, h["save"], map[string]any{"path": "sign-in.yaml"}, cwd)
	path := filepath.Join(cwd, "sign-in.yaml")
	leaks(t, "tb save", resp.Text)
	if !strings.Contains(resp.Text, "tb replay "+path+" -d DEVICE -e PW=...") {
		t.Errorf("save does not say what replay needs:\n%s", resp.Text)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	leaks(t, "the saved flow", string(saved))
	if string(saved) != "appId: com.app\n---\n"+want {
		t.Errorf("saved flow:\n%s", saved)
	}
	resp, _ = call(t, h["save"], map[string]any{"preview": true}, cwd)
	leaks(t, "tb save --preview", resp.Text+string(resp.JSON))
	var preview struct {
		Warnings []recordingWarning `json:"warnings"`
	}
	json.Unmarshal(resp.JSON, &preview)
	found := false
	for _, w := range preview.Warnings {
		found = found || (w.Code == "secret_variables" && strings.Contains(w.Message, "-e PW=..."))
	}
	if !found || !strings.Contains(resp.Text, "-e PW=...") {
		t.Errorf("preview does not list the variables replay needs:\n%s\n%s", resp.Text, resp.JSON)
	}

	fl, err := flow.ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	replayed := newFake("r", "R", device.Android)
	replayed.show(button("Sign in", 300), label(fakeSecret, 400))
	res := engine.New(replayed, engine.Options{Env: []flow.KV{{Key: "PW", Value: fakeSecret}}}).RunFlow(context.Background(), fl)
	if res.Err != nil || !reflect.DeepEqual(replayed.typed, []string{fakeSecret, fakeSecret}) {
		t.Errorf("replay with -e PW: err %v, typed %q", res.Err, replayed.typed)
	}
}

func TestSecretsEndWithTheSession(t *testing.T) {
	s, _ := secretSession(t)
	h := s.handlers()
	call(t, h["do"], map[string]any{"secrets": map[string]string{"PW": "fake"}, "commands": "- inputText: ${PW}\n"}, "")
	if resp, _ := call(t, h["look"], nil, ""); strings.Contains(resp.Text, `"fake"`) {
		t.Fatalf("look in the session: %+v", resp)
	}
	if resp, _ := call(t, h["close"], nil, ""); !resp.OK {
		t.Fatalf("close: %+v", resp)
	}
	again := newFake("e2", "E2", device.Android)
	again.show(label("fake", 100))
	addDev(s, again, "")
	if resp, _ := call(t, h["look"], nil, ""); !strings.Contains(resp.Text, `"fake"`) {
		t.Errorf("look on another session: %+v", resp)
	}
}

func TestEmptySecretIsRefused(t *testing.T) {
	s, f := secretSession(t)
	resp, _ := call(t, s.handlers()["do"], map[string]any{"secrets": map[string]string{"PW": ""}, "commands": "- inputText: ${PW}\n"}, "")
	if resp.Exit != 2 || !strings.Contains(resp.Text, "PW") || len(f.typed) != 0 {
		t.Errorf("resp %+v, typed %q", resp, f.typed)
	}
}
