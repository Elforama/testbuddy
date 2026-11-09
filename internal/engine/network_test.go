package engine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/flow"
)

// mockFake is a web-like device that can mock network requests (driver.NetworkMocker). It
// records "mock" and "clear" calls in order, and the mocks it was given.
type mockFake struct {
	*fakeDriver
	log   []string
	mocks []driver.NetworkMock
	err   error
}

func (d *mockFake) MockNetwork(ctx context.Context, m driver.NetworkMock) error {
	if d.err != nil {
		return d.err
	}
	d.log = append(d.log, "mock")
	d.mocks = append(d.mocks, m)
	return nil
}

func (d *mockFake) ClearNetworkMocks(ctx context.Context) error {
	d.log = append(d.log, "clear")
	d.mocks = nil
	return nil
}

func newMockFake() *mockFake {
	return &mockFake{fakeDriver: &fakeDriver{screen: static(), plat: "web"}}
}

// The mock as the driver gets it: values interpolated, the method upper-cased, the delay in
// milliseconds, and a Content-Type guessed unless the flow gives one.
func TestMockNetworkValues(t *testing.T) {
	d := newMockFake()
	o := quick
	o.Env = []flow.KV{{Key: "HOST", Value: "shop.example.com"}, {Key: "NAME", Value: "Ann"}}
	dir := t.TempDir()
	writeFile(t, dir, "list.json", "not json at all")
	src := `appId: app
---
- mockNetwork:
    url: "https://${HOST}/api/*"
    method: post
    status: 503
    body: 'Hello ${NAME}'
    headers: {X-Who: "${NAME}"}
    delay: 250
- mockNetwork: {url: "*/a", body: '{"ok": true}'}
- mockNetwork: {url: "*/b", body: '[1]', headers: {content-type: text/csv}}
- mockNetwork: {url: "*/c", file: list.json}
- mockNetwork: {url: "*/d", fail: true}
- mockNetwork: {url: "*/e"}
`
	fl, err := flow.ParseBytes(dir+"/flow.yaml", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	r := New(d, o)
	if err := r.Begin(fl, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.RunList(context.Background(), fl.Commands, fl); err != nil {
		t.Fatal(err)
	}
	text := [][2]string{{"Content-Type", "text/plain; charset=utf-8"}}
	want := []driver.NetworkMock{
		{URL: "https://shop.example.com/api/*", Method: "POST", Status: 503, Body: []byte("Hello Ann"),
			Headers: [][2]string{{"X-Who", "Ann"}, text[0]}, Delay: 250 * time.Millisecond},
		{URL: "*/a", Status: 200, Body: []byte(`{"ok": true}`), Headers: [][2]string{{"Content-Type", "application/json"}}},
		{URL: "*/b", Status: 200, Body: []byte(`[1]`), Headers: [][2]string{{"content-type", "text/csv"}}},
		{URL: "*/c", Status: 200, Body: []byte("not json at all"), Headers: [][2]string{{"Content-Type", "application/json"}}},
		{URL: "*/d", Status: 200, Fail: true},
		{URL: "*/e", Status: 200, Body: []byte{}, Headers: text},
	}
	if len(d.mocks) != len(want) {
		t.Fatalf("got %d mocks: %+v", len(d.mocks), d.mocks)
	}
	for i := range want {
		if !reflect.DeepEqual(d.mocks[i], want[i]) {
			t.Errorf("mock %d:\n got %+v\nwant %+v", i, d.mocks[i], want[i])
		}
	}
	r.End()
	if len(d.mocks) != 0 {
		t.Errorf("End left mocks: %v", d.mocks)
	}
}

// clearNetworkMocks clears; a flow also starts and ends with no mocks.
func TestClearNetworkMocks(t *testing.T) {
	d := newMockFake()
	mustPass(t, runWith(t, d, quick, "- mockNetwork: {url: '*'}\n- clearNetworkMocks\n- mockNetwork: {url: '*'}\n"))
	if want := []string{"clear", "mock", "clear", "mock", "clear"}; !reflect.DeepEqual(d.log, want) {
		t.Errorf("calls %v, want %v", d.log, want)
	}
}

// A failing flow still clears its mocks.
func TestMockNetworkClearedAfterFailure(t *testing.T) {
	d := newMockFake()
	mustFail(t, runWith(t, d, quick, "- mockNetwork: {url: '*'}\n- assertVisible: Nowhere\n"), "Nowhere")
	if want := []string{"clear", "mock", "clear"}; !reflect.DeepEqual(d.log, want) {
		t.Errorf("calls %v, want %v", d.log, want)
	}
}

// An agent session (tb do) keeps mocks between calls until it clears them.
func TestMockNetworkKeptAcrossDoCalls(t *testing.T) {
	d := newMockFake()
	r := New(d, quick)
	for _, src := range []string{"- mockNetwork: {url: '*/a'}\n", "- mockNetwork: {url: '*/b'}\n"} {
		cmds, err := flow.ParseCommands(t.TempDir(), []byte(src))
		if err != nil {
			t.Fatal(err)
		}
		if err := r.RunCommands(context.Background(), cmds, "app", nil); err != nil {
			t.Fatal(err)
		}
	}
	if want := []string{"mock", "mock"}; !reflect.DeepEqual(d.log, want) {
		t.Errorf("calls %v, want %v", d.log, want)
	}
}

// Android and iOS cannot mock: the step fails, or warns when optional.
func TestMockNetworkUnsupported(t *testing.T) {
	mustFail(t, runWith(t, &fakeDriver{screen: static()}, quick, "- mockNetwork: {url: '*'}"),
		"mockNetwork works only in web flows (url:)")
	mustFail(t, runWith(t, &fakeDriver{screen: static()}, quick, "- clearNetworkMocks"),
		"clearNetworkMocks works only in web flows (url:)")
	res := runWith(t, &fakeDriver{screen: static()}, quick, "- mockNetwork: {url: '*', optional: true}")
	mustPass(t, res)
	if res.Steps[0].Status != Warned {
		t.Errorf("status %s", res.Steps[0].Status)
	}
}

// A driver error fails the step.
func TestMockNetworkDriverError(t *testing.T) {
	d := newMockFake()
	d.err = errors.New("Fetch.enable: no target")
	mustFail(t, runWith(t, d, quick, "- mockNetwork: {url: '*'}"), "Fetch.enable: no target")
}

// A bad interpolated value fails the step instead of reaching the driver.
func TestMockNetworkBadScript(t *testing.T) {
	d := newMockFake()
	mustFail(t, runWith(t, d, quick, "- mockNetwork: {url: '${nope.x}'}"), "JavaScript error")
	if len(d.mocks) != 0 || contains(fmt.Sprint(d.log), "mock") {
		t.Errorf("mocks %v", d.mocks)
	}
}
