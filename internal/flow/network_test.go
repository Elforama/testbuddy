package flow

import (
	"path/filepath"
	"reflect"
	"testing"
)

// mockNetwork reads every option; values stay raw (the engine interpolates them).
func TestMockNetworkFull(t *testing.T) {
	c := step(t, `- mockNetwork:
    url: "*/api/items*"
    method: get
    status: 201
    body: '{"items": ["${NAME}"]}'
    headers: {Content-Type: application/json, X-Mock: "yes"}
    delay: 1500
    label: Empty list
    optional: true
`)
	if c.Name != "mockNetwork" || c.Label != "Empty list" || !c.Optional {
		t.Fatalf("command %+v", c)
	}
	want := &NetworkMock{URL: "*/api/items*", Method: "GET", Status: 201, Body: `{"items": ["${NAME}"]}`,
		Headers: []KV{{"Content-Type", "application/json"}, {"X-Mock", "yes"}}, Delay: 1500}
	if !reflect.DeepEqual(c.Mock, want) {
		t.Errorf("mock %+v\nwant %+v", c.Mock, want)
	}
}

// The defaults: any method, status 200, an empty body, no delay.
func TestMockNetworkDefaults(t *testing.T) {
	c := step(t, "- mockNetwork: {url: \"*/api/*\"}\n")
	want := &NetworkMock{URL: "*/api/*", Status: 200}
	if !reflect.DeepEqual(c.Mock, want) {
		t.Errorf("mock %+v, want %+v", c.Mock, want)
	}
}

// fail: true makes the request fail; it takes no response options.
func TestMockNetworkFail(t *testing.T) {
	c := step(t, "- mockNetwork: {url: \"*/ads/*\", fail: true}\n")
	if !c.Mock.Fail || c.Mock.URL != "*/ads/*" {
		t.Errorf("mock %+v", c.Mock)
	}
}

// file: is read at parse time, relative to the flow file, like runScript's file; the body
// keeps its bytes as they are (no interpolation) and the path is kept for its extension.
func TestMockNetworkFile(t *testing.T) {
	dir := writeFiles(t, map[string]string{"fixtures/items.json": `{"items": ["${raw}"]}`})
	f, err := ParseBytes(filepath.Join(dir, "main.yaml"), []byte(hdr+"- mockNetwork: {url: \"*/items\", file: fixtures/items.json}\n"))
	if err != nil {
		t.Fatal(err)
	}
	m := f.Commands[0].Mock
	if m.Body != `{"items": ["${raw}"]}` || !m.BodyFromFile || m.BodyFile != filepath.Join(dir, "fixtures", "items.json") {
		t.Errorf("mock %+v", m)
	}
}

func TestMockNetworkErrors(t *testing.T) {
	runErrCases(t, hdr, []errCase{
		{"no url", "- mockNetwork: {status: 200}\n", "mockNetwork needs url", 3},
		{"empty url", "- mockNetwork: {url: \"\"}\n", "mockNetwork needs url", 3},
		{"scalar", "- mockNetwork: \"*/api/*\"\n", "mockNetwork takes a map", 3},
		{"bare", "- mockNetwork\n", "mockNetwork needs a value", 3},
		{"body and file", "- mockNetwork: {url: x, body: a, file: b.json}\n", "body or file, not both", 3},
		{"missing file", "- mockNetwork: {url: x, file: nope.json}\n", "mock body file not found", 3},
		{"status text", "- mockNetwork: {url: x, status: ok}\n", "expected a number", 3},
		{"status range", "- mockNetwork: {url: x, status: 42}\n", "status must be an HTTP status code (100-599)", 3},
		{"negative delay", "- mockNetwork: {url: x, delay: -5}\n", "delay must not be negative", 3},
		{"fail with body", "- mockNetwork: {url: x, fail: true, body: a}\n", "fail: true sends no response", 3},
		{"headers list", "- mockNetwork: {url: x, headers: [a]}\n", "expected a map", 3},
		{"unknown key", "- mockNetwork: {url: x, respond: 200}\n", `"respond" is not an option of mockNetwork`, 3},
	})
}

// clearNetworkMocks is a bare command; a map form takes label and optional only.
func TestClearNetworkMocks(t *testing.T) {
	if c := step(t, "- clearNetworkMocks\n"); c.Name != "clearNetworkMocks" {
		t.Errorf("name %q", c.Name)
	}
	if c := step(t, "- clearNetworkMocks: {label: reset}\n"); c.Label != "reset" {
		t.Errorf("label %q", c.Label)
	}
	runErrCases(t, hdr, []errCase{
		{"unknown key", "- clearNetworkMocks: {url: x}\n", `"url" is not an option of clearNetworkMocks`, 3},
	})
}

// Mocking changes no screen: it does not count as an interaction.
func TestMockNetworkNotMutating(t *testing.T) {
	for _, n := range []string{"mockNetwork", "clearNetworkMocks"} {
		if (&Command{Name: n}).Mutating() {
			t.Errorf("%s should not be mutating", n)
		}
	}
}
