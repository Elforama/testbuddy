package orchestrate

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/engine"
	"github.com/elforama/testbuddy/internal/flow"
)

func baseOpts(t *testing.T) engine.Options {
	return engine.Options{LookupTimeout: 300 * time.Millisecond, OptionalTimeout: 100 * time.Millisecond, ArtifactsDir: t.TempDir()}
}

const twoRoles = "appId: com.app\ndevices:\n  alice: android\n  bob: android\n---\n"

func pair() (*fakeDriver, *fakeDriver, map[string]driver.Driver) {
	a, b := newFake("a", "Phone A", "android"), newFake("b", "Phone B", "android")
	return a, b, map[string]driver.Driver{"alice": a, "bob": b}
}

func runMulti(t *testing.T, yaml string, drivers map[string]driver.Driver) MultiResult {
	t.Helper()
	f := parseFlow(t, "multi", yaml)
	var res MultiResult
	within(t, 10*time.Second, func() { res = RunMulti(context.Background(), f, drivers, baseOpts(t)) })
	return res
}

// Role blocks run their commands on that role's device, in order.
func TestRunMultiRoleBlocks(t *testing.T) {
	a, b, drivers := pair()
	res := runMulti(t, twoRoles+`- alice:
    - inputText: a1
    - inputText: a2
- bob:
    - inputText: b1
- alice: { inputText: a3 }
`, drivers)
	if res.Status != engine.Passed || res.Err != nil {
		t.Fatalf("status %s err %v", res.Status, res.Err)
	}
	if got := a.Typed(); !reflect.DeepEqual(got, []string{"a1", "a2", "a3"}) {
		t.Errorf("alice typed %v", got)
	}
	if got := b.Typed(); !reflect.DeepEqual(got, []string{"b1"}) {
		t.Errorf("bob typed %v", got)
	}
	if !reflect.DeepEqual(res.Devices, map[string]string{"alice": "Phone A", "bob": "Phone B"}) {
		t.Errorf("devices %v", res.Devices)
	}
	if res.Duration <= 0 {
		t.Error("duration not set")
	}
}

// A role with no device fails before anything runs.
func TestRunMultiMissingDevice(t *testing.T) {
	a := newFake("a", "A", "android")
	res := runMulti(t, twoRoles+"- alice: [{inputText: x}]\n", map[string]driver.Driver{"alice": a})
	if res.Status != engine.Failed || res.Err == nil || res.Err.Error() != `no device for role "bob"` {
		t.Fatalf("status %s err %v", res.Status, res.Err)
	}
	if len(a.Typed()) != 0 {
		t.Error("commands ran although a role had no device")
	}
}

// A failing step stops the flow: later steps never run.
func TestRunMultiFailureStopsLaterSteps(t *testing.T) {
	a, b, drivers := pair()
	a.launchFails = -1
	res := runMulti(t, twoRoles+"- alice: [launchApp]\n- bob: [{inputText: never}]\n", drivers)
	if res.Status != engine.Failed || !strings.Contains(res.Err.Error(), "Unable to launch app com.app") {
		t.Fatalf("status %s err %v", res.Status, res.Err)
	}
	if len(b.Typed()) != 0 {
		t.Error("bob ran after alice failed")
	}
}

// parallel blocks run at the same time: each waits for a signal the other raises, which
// would deadlock if they ran one after the other.
func TestRunMultiParallelIsConcurrent(t *testing.T) {
	a, b, drivers := pair()
	res := runMulti(t, twoRoles+`- parallel:
    alice:
      - signal: alice-up
      - waitForSignal: { name: bob-up, timeout: 3000 }
      - inputText: a
    bob:
      - signal: bob-up
      - waitForSignal: { name: alice-up, timeout: 3000 }
      - inputText: b
`, drivers)
	if res.Status != engine.Passed {
		t.Fatalf("err %v", res.Err)
	}
	if !reflect.DeepEqual(a.Typed(), []string{"a"}) || !reflect.DeepEqual(b.Typed(), []string{"b"}) {
		t.Errorf("alice %v bob %v", a.Typed(), b.Typed())
	}
}

// The first failure in a parallel block cancels the others, and the error names the role.
func TestRunMultiParallelFailureCancelsOthers(t *testing.T) {
	a, b, drivers := pair()
	a.launchFails = -1
	start := time.Now()
	res := runMulti(t, twoRoles+`- parallel:
    alice: [launchApp]
    bob:
      - waitForSignal: never   # default timeout is 120 s
      - inputText: unreachable
`, drivers)
	if res.Status != engine.Failed {
		t.Fatal("want failure")
	}
	if !strings.HasPrefix(res.Err.Error(), "alice: ") || !strings.Contains(res.Err.Error(), "Unable to launch app") {
		t.Errorf("err = %v", res.Err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("bob was not cancelled: took %s", d)
	}
	if len(b.Typed()) != 0 {
		t.Error("bob kept going")
	}
}

// A parallel block waits for every role before the next step.
func TestRunMultiParallelWaitsForAll(t *testing.T) {
	a, b, drivers := pair()
	var mu sync.Mutex
	var order []string
	b.onType = func(s string) {
		if s == "slow" {
			time.Sleep(50 * time.Millisecond)
		}
		mu.Lock()
		order = append(order, "bob:"+s)
		mu.Unlock()
	}
	a.onType = func(s string) {
		mu.Lock()
		order = append(order, "alice:"+s)
		mu.Unlock()
	}
	res := runMulti(t, twoRoles+`- parallel:
    alice: [{inputText: fast}]
    bob: [{inputText: slow}]
- alice: [{inputText: after}]
`, drivers)
	if res.Status != engine.Passed {
		t.Fatal(res.Err)
	}
	mu.Lock()
	defer mu.Unlock()
	if order[len(order)-1] != "alice:after" || len(order) != 3 {
		t.Errorf("order %v: the next step ran before the parallel block finished", order)
	}
}

// All devices share one JavaScript output object: a value set on one is typed on another.
func TestRunMultiSharedOutput(t *testing.T) {
	_, b, drivers := pair()
	res := runMulti(t, twoRoles+`- alice:
    - evalScript: ${output.code = 'ABC' + 123}
- bob:
    - inputText: ${output.code}
`, drivers)
	if res.Status != engine.Passed {
		t.Fatal(res.Err)
	}
	if got := b.Typed(); !reflect.DeepEqual(got, []string{"ABC123"}) {
		t.Errorf("bob typed %v", got)
	}
}

// Each device sees the shared env and TB_ROLE; names only one side defines reach the device.
func TestRunMultiEnv(t *testing.T) {
	a, b, drivers := pair()
	res := runMulti(t, `appId: com.app
env:
  GREETING: hi
devices:
  alice: { platform: android, env: { USER: alice@x } }
  bob: android
---
- alice: [{inputText: "${TB_ROLE} ${USER} ${GREETING}"}]
- bob: [{inputText: "${TB_ROLE} ${GREETING}"}]
`, drivers)
	if res.Status != engine.Passed {
		t.Fatal(res.Err)
	}
	if got := a.Typed(); !reflect.DeepEqual(got, []string{"alice alice@x hi"}) {
		t.Errorf("alice typed %v", got)
	}
	if got := b.Typed(); !reflect.DeepEqual(got, []string{"bob hi"}) {
		t.Errorf("bob typed %v", got)
	}
}

// docs/multi-device.md: "Each device sees the shared env:, then its role's env:", so a
// role's value overrides the shared one of the same name.
func TestRunMultiRoleEnvOverridesShared(t *testing.T) {
	a, b, drivers := pair()
	res := runMulti(t, `appId: com.app
env:
  USER: nobody
devices:
  alice: { env: { USER: alice@x } }
  bob: android
---
- alice: [{inputText: "${USER}"}]
- bob: [{inputText: "${USER}"}]
`, drivers)
	if res.Status != engine.Passed {
		t.Fatal(res.Err)
	}
	if got := a.Typed(); !reflect.DeepEqual(got, []string{"alice@x"}) {
		t.Errorf("alice typed %v, want her role's USER", got)
	}
	if got := b.Typed(); !reflect.DeepEqual(got, []string{"nobody"}) {
		t.Errorf("bob typed %v, want the shared USER", got)
	}
}

// A role's env reaches its device when the flow does not define the same name.
func TestRunMultiRoleEnv(t *testing.T) {
	a, b, drivers := pair()
	res := runMulti(t, `appId: com.app
devices:
  alice: { env: { USER: alice@x } }
  bob: { env: { USER: bob@x } }
---
- alice: [{inputText: "${USER}"}]
- bob: [{inputText: "${USER}"}]
`, drivers)
	if res.Status != engine.Passed {
		t.Fatal(res.Err)
	}
	if !reflect.DeepEqual(a.Typed(), []string{"alice@x"}) || !reflect.DeepEqual(b.Typed(), []string{"bob@x"}) {
		t.Errorf("alice %v bob %v", a.Typed(), b.Typed())
	}
}

// The base options' env (the CLI's -e values) reaches every device.
func TestRunMultiBaseEnv(t *testing.T) {
	a, b, drivers := pair()
	f := parseFlow(t, "multi", twoRoles+"- alice: [{inputText: \"${CLI}\"}]\n- bob: [{inputText: \"${CLI}\"}]\n")
	o := baseOpts(t)
	o.Env = []flow.KV{{Key: "CLI", Value: "from-cli"}}
	res := RunMulti(context.Background(), f, drivers, o)
	if res.Status != engine.Passed {
		t.Fatal(res.Err)
	}
	if !reflect.DeepEqual(a.Typed(), []string{"from-cli"}) || !reflect.DeepEqual(b.Typed(), []string{"from-cli"}) {
		t.Errorf("alice %v bob %v", a.Typed(), b.Typed())
	}
}

// A role's appId is that device's default app; roles without one use the flow's.
func TestRunMultiRoleAppID(t *testing.T) {
	a, b, drivers := pair()
	res := runMulti(t, `appId: com.shared
devices:
  alice: { appId: com.alice }
  bob: android
---
- alice: [launchApp]
- bob: [launchApp]
`, drivers)
	if res.Status != engine.Passed {
		t.Fatal(res.Err)
	}
	if !reflect.DeepEqual(a.Launched(), []string{"com.alice"}) || !reflect.DeepEqual(b.Launched(), []string{"com.shared"}) {
		t.Errorf("alice launched %v, bob %v", a.Launched(), b.Launched())
	}
}

// A device command outside a role block is refused when several devices could run it.
func TestRunMultiPlainCommandNeedsRole(t *testing.T) {
	a, b, drivers := pair()
	res := runMulti(t, twoRoles+"- inputText: hello\n", drivers)
	if res.Status != engine.Failed {
		t.Fatal("want failure")
	}
	msg := res.Err.Error()
	if !strings.Contains(msg, "multi.yaml:6: inputText: say which device runs it, e.g. `- alice: [inputText ...]`") {
		t.Errorf("err = %q", msg)
	}
	if len(a.Typed())+len(b.Typed()) != 0 {
		t.Error("the command ran somewhere")
	}
}

// Commands that do not touch a device may sit outside role blocks.
func TestRunMultiDeviceFreeTopLevel(t *testing.T) {
	_, b, drivers := pair()
	res := runMulti(t, twoRoles+`- evalScript: ${output.n = 2}
- assertTrue: ${output.n == 2}
- setClipboard: copied
- signal: go
- waitForSignal: go
- bob: [{inputText: "${output.n}"}]
`, drivers)
	if res.Status != engine.Passed {
		t.Fatal(res.Err)
	}
	if got := b.Typed(); !reflect.DeepEqual(got, []string{"2"}) {
		t.Errorf("bob typed %v", got)
	}
}

// With a single role, plain commands run on its device.
func TestRunMultiSingleRole(t *testing.T) {
	a := newFake("a", "A", "android")
	res := runMulti(t, "appId: com.app\ndevices:\n  solo: android\n---\n- inputText: direct\n- solo: [{inputText: block}]\n", map[string]driver.Driver{"solo": a})
	if res.Status != engine.Passed {
		t.Fatal(res.Err)
	}
	if got := a.Typed(); !reflect.DeepEqual(got, []string{"direct", "block"}) {
		t.Errorf("typed %v", got)
	}
}

// onFlowStart and onFlowComplete run on every device.
func TestRunMultiHooksOnEveryDevice(t *testing.T) {
	a, b, drivers := pair()
	res := runMulti(t, `appId: com.app
devices:
  alice: android
  bob: android
onFlowStart:
  - inputText: start
onFlowComplete:
  - inputText: end
---
- alice: [{inputText: middle}]
`, drivers)
	if res.Status != engine.Passed {
		t.Fatal(res.Err)
	}
	if got := a.Typed(); !reflect.DeepEqual(got, []string{"start", "middle", "end"}) {
		t.Errorf("alice typed %v", got)
	}
	if got := b.Typed(); !reflect.DeepEqual(got, []string{"start", "end"}) {
		t.Errorf("bob typed %v", got)
	}
}

// onFlowComplete runs even when the commands fail; the commands' error wins.
func TestRunMultiCompleteAfterFailure(t *testing.T) {
	a, b, drivers := pair()
	a.launchFails = -1
	res := runMulti(t, `appId: com.app
devices:
  alice: android
  bob: android
onFlowComplete:
  - inputText: cleanup
---
- alice: [launchApp]
`, drivers)
	if res.Status != engine.Failed || !strings.Contains(res.Err.Error(), "Unable to launch") {
		t.Fatalf("status %s err %v", res.Status, res.Err)
	}
	if !reflect.DeepEqual(a.Typed(), []string{"cleanup"}) || !reflect.DeepEqual(b.Typed(), []string{"cleanup"}) {
		t.Errorf("alice %v bob %v", a.Typed(), b.Typed())
	}
}

// A failing onFlowStart skips the commands; a failing onFlowComplete fails a passing flow.
func TestRunMultiHookFailures(t *testing.T) {
	t.Run("start fails", func(t *testing.T) {
		a, _, drivers := pair()
		a.launchFails = -1
		res := runMulti(t, "appId: com.app\ndevices:\n  alice: android\n  bob: android\nonFlowStart:\n  - launchApp\n---\n- bob: [{inputText: never}]\n", drivers)
		if res.Status != engine.Failed || !strings.HasPrefix(res.Err.Error(), "alice: ") {
			t.Fatalf("status %s err %v", res.Status, res.Err)
		}
		if got := drivers["bob"].(*fakeDriver).Typed(); len(got) != 0 {
			t.Errorf("commands ran after onFlowStart failed: %v", got)
		}
	})
	t.Run("complete fails", func(t *testing.T) {
		_, b, drivers := pair()
		b.launchFails = -1
		res := runMulti(t, "appId: com.app\ndevices:\n  alice: android\n  bob: android\nonFlowComplete:\n  - launchApp\n---\n- alice: [{inputText: ok}]\n", drivers)
		if res.Status != engine.Failed || !strings.HasPrefix(res.Err.Error(), "bob: ") {
			t.Fatalf("status %s err %v", res.Status, res.Err)
		}
	})
}

// A step naming a role the flow did not declare is an error (flows built in code).
func TestRunMultiUnknownRole(t *testing.T) {
	_, _, drivers := pair()
	f := parseFlow(t, "multi", twoRoles+"- alice: [back]\n")
	f.Commands = []*flow.Command{{Name: "on", On: "carol", File: "x.yaml", Line: 7}}
	res := RunMulti(context.Background(), f, drivers, baseOpts(t))
	if res.Status != engine.Failed || res.Err.Error() != `x.yaml:7: unknown device role "carol"` {
		t.Errorf("err = %v", res.Err)
	}
	f.Commands = []*flow.Command{{Name: "parallel", Parallel: map[string][]*flow.Command{"carol": nil}}}
	res = RunMulti(context.Background(), f, drivers, baseOpts(t))
	if res.Status != engine.Failed || res.Err.Error() != `unknown device role "carol"` {
		t.Errorf("parallel err = %v", res.Err)
	}
}

// A cancelled context stops the flow before its first step.
func TestRunMultiCancelled(t *testing.T) {
	a, _, drivers := pair()
	f := parseFlow(t, "multi", twoRoles+"- alice: [{inputText: x}]\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := RunMulti(ctx, f, drivers, baseOpts(t))
	if res.Status != engine.Failed || res.Err != context.Canceled {
		t.Errorf("status %s err %v", res.Status, res.Err)
	}
	if len(a.Typed()) != 0 {
		t.Error("ran after cancel")
	}
}

// A parallel block under an already-cancelled context reports the cancellation.
func TestRunMultiParallelCancelled(t *testing.T) {
	_, _, drivers := pair()
	f := parseFlow(t, "multi", twoRoles+"- parallel:\n    alice: [{inputText: x}]\n    bob: [{inputText: y}]\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runners := map[string]*engine.Runner{}
	for role, d := range drivers {
		runners[role] = engine.New(d, baseOpts(t))
	}
	if err := parallel(ctx, runners, f.Commands[0].Parallel, f); err != context.Canceled {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

// An env value that does not evaluate fails the flow at setup.
func TestRunMultiSetupError(t *testing.T) {
	_, _, drivers := pair()
	res := runMulti(t, "appId: com.app\nenv:\n  BAD: ${(}\ndevices:\n  alice: android\n  bob: android\n---\n- alice: [back]\n", drivers)
	if res.Status != engine.Failed || res.Err == nil {
		t.Fatalf("status %s err %v", res.Status, res.Err)
	}
}

// A recording left running on a device is stopped when the flow ends.
func TestRunMultiStopsRecordings(t *testing.T) {
	a, b, drivers := pair()
	res := runMulti(t, twoRoles+"- alice: [{startRecording: clip}]\n- bob: [{inputText: x}]\n", drivers)
	if res.Status != engine.Passed {
		t.Fatal(res.Err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.recStart) != 1 || a.recStop != 1 {
		t.Errorf("alice recordings started %v stopped %d", a.recStart, a.recStop)
	}
	if b.recStop != 0 {
		t.Error("bob was not recording")
	}
}

// waitForSignal times out with a clear message when the signal never comes.
func TestRunMultiSignalTimeout(t *testing.T) {
	_, _, drivers := pair()
	res := runMulti(t, twoRoles+"- bob: [{waitForSignal: {name: ready, timeout: 50}}]\n", drivers)
	if res.Status != engine.Failed || !strings.Contains(res.Err.Error(), `signal "ready" did not arrive within 50ms`) {
		t.Errorf("err = %v", res.Err)
	}
}

// A signal raised before anyone waits is remembered.
func TestRunMultiSignalBeforeWait(t *testing.T) {
	_, b, drivers := pair()
	res := runMulti(t, twoRoles+"- alice: [{signal: done}, {signal: done}]\n- bob: [{waitForSignal: done}, {inputText: ok}]\n", drivers)
	if res.Status != engine.Passed || !reflect.DeepEqual(b.Typed(), []string{"ok"}) {
		t.Errorf("status %s err %v typed %v", res.Status, res.Err, b.Typed())
	}
}

// Which commands may run outside a role block.
func TestDeviceFree(t *testing.T) {
	for name, want := range map[string]bool{
		"runScript": true, "evalScript": true, "assertTrue": true, "signal": true, "waitForSignal": true, "setClipboard": true,
		"tapOn": false, "inputText": false, "launchApp": false, "assertVisible": false, "copyTextFrom": false, "runFlow": false,
	} {
		if got := deviceFree(&flow.Command{Name: name}); got != want {
			t.Errorf("deviceFree(%s) = %v, want %v", name, got, want)
		}
	}
}

// soleRunner finds the only runner; anyRunner any one; both handle empty maps.
func TestRunnerPickers(t *testing.T) {
	r1 := engine.New(newFake("a", "A", "android"), engine.Options{})
	r2 := engine.New(newFake("b", "B", "android"), engine.Options{})
	if r, ok := soleRunner(map[string]*engine.Runner{"x": r1}); !ok || r != r1 {
		t.Error("soleRunner with one")
	}
	if _, ok := soleRunner(map[string]*engine.Runner{"x": r1, "y": r2}); ok {
		t.Error("soleRunner with two")
	}
	if _, ok := soleRunner(nil); ok {
		t.Error("soleRunner with none")
	}
	if r := anyRunner(map[string]*engine.Runner{"x": r1, "y": r2}); r != r1 && r != r2 {
		t.Error("anyRunner")
	}
	if anyRunner(nil) != nil {
		t.Error("anyRunner of nothing")
	}
}

// console.log lines inside a parallel block carry the device that logged them: the devices
// share one JavaScript engine and log at the same time.
func TestRunMultiParallelLogsTaggedByDevice(t *testing.T) {
	_, _, drivers := pair()
	logs := func(who string) string {
		var b strings.Builder
		for i := 0; i < 200; i++ {
			fmt.Fprintf(&b, "      - evalScript: ${console.log('%s %d')}\n", who, i)
		}
		return b.String()
	}
	f := parseFlow(t, "multi", twoRoles+"- parallel:\n    alice:\n"+logs("alice")+"    bob:\n"+logs("bob"))
	var mu sync.Mutex
	var wrong []string
	count := 0
	o := baseOpts(t)
	o.Reporter = engine.ReporterFunc(func(e engine.Event) {
		if e.Log == "" || e.Cmd != nil {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		count++
		if !strings.HasPrefix(e.Log, e.Device+" ") {
			wrong = append(wrong, "["+e.Device+"] "+e.Log)
		}
	})
	var res MultiResult
	within(t, 20*time.Second, func() { res = RunMulti(context.Background(), f, drivers, o) })
	if res.Status != engine.Passed {
		t.Fatal(res.Err)
	}
	if count != 400 {
		t.Errorf("%d log lines, want 400", count)
	}
	if len(wrong) > 0 {
		t.Errorf("%d lines tagged with the other device, e.g. %q", len(wrong), wrong[0])
	}
}

// A multi-device flow's devices share one trace, each line tagged with its role.
func TestRunMultiTraceTaggedByDevice(t *testing.T) {
	_, _, drivers := pair()
	f := parseFlow(t, "multi", twoRoles+"- parallel:\n    alice:\n      - inputText: a\n    bob:\n      - inputText: b\n")
	var mu sync.Mutex
	var buf strings.Builder
	o := baseOpts(t)
	o.Trace = engine.NewTrace(writerFunc(func(p []byte) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		return buf.Write(p)
	}))
	var res MultiResult
	within(t, 10*time.Second, func() { res = RunMulti(context.Background(), f, drivers, o) })
	if res.Status != engine.Passed {
		t.Fatal(res.Err)
	}
	trace := buf.String()
	for _, want := range []string{"[alice] ▶ Input text a", "[bob] ▶ Input text b", "[alice] ✓ Input text a", "[bob] ✓ Input text b"} {
		if !strings.Contains(trace, want) {
			t.Errorf("trace lacks %q:\n%s", want, trace)
		}
	}
	if strings.Contains(trace, "[alice] ▶ Input text b") || strings.Contains(trace, "[bob] ▶ Input text a") {
		t.Errorf("a step tagged with the other device:\n%s", trace)
	}
}

type writerFunc func([]byte) (int, error)

func (w writerFunc) Write(p []byte) (int, error) { return w(p) }

// --record in a multi-device flow records each role's device, from the flow's start to its
// end, each into its own file, and the result lists them role by role.
func TestRunMultiRecordsEachDevice(t *testing.T) {
	a, b, drivers := pair()
	f := parseFlow(t, "multi", twoRoles+"- alice: [{inputText: a}]\n- bob: [{inputText: b}]\n")
	o := baseOpts(t)
	o.Record = true
	var res MultiResult
	within(t, 10*time.Second, func() { res = RunMulti(context.Background(), f, drivers, o) })
	if res.Status != engine.Passed {
		t.Fatal(res.Err)
	}
	for _, c := range []struct {
		d    *fakeDriver
		file string
	}{{a, "recording-alice.mp4"}, {b, "recording-bob.mp4"}} {
		c.d.mu.Lock()
		starts, stops := c.d.recStart, c.d.recStop
		c.d.mu.Unlock()
		if len(starts) != 1 || filepath.Base(starts[0]) != c.file || stops != 1 {
			t.Errorf("%s: started %v, stopped %d times", c.file, starts, stops)
		}
	}
	if len(res.Videos) != 2 || res.Videos[0].Role != "alice" || res.Videos[1].Role != "bob" ||
		filepath.Base(res.Videos[1].Path) != "recording-bob.mp4" {
		t.Errorf("videos %+v", res.Videos)
	}
}
