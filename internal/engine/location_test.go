package engine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/flow"
)

// setLocation takes evaluated numbers; anything else is an error.
func TestSetLocation(t *testing.T) {
	f := &fakeDriver{screen: static()}
	mustPass(t, runWith(t, f, quick, "- setLocation: {latitude: '52.5', longitude: \"${13 + 0.25}\"}"))
	if want := [][2]float64{{52.5, 13.25}}; !reflect.DeepEqual(f.locs, want) {
		t.Errorf("locations %v", f.locs)
	}
	mustFail(t, runWith(t, &fakeDriver{screen: static()}, quick, "- setLocation: {latitude: '${(}', longitude: '1'}"), "JavaScript error")
	mustFail(t, runWith(t, &fakeDriver{screen: static()}, quick, "- setLocation: {latitude: '1', longitude: '${(}'}"), "JavaScript error")
	f2 := &fakeDriver{screen: static()}
	f2.fail("SetLocation", errors.New("no gps"))
	mustFail(t, runWith(t, f2, quick, "- setLocation: {latitude: '1', longitude: '2'}"), "no gps")
}

// travel sets the first point, then moves to each next one in 50 steps, timed by the speed.
func TestTravel(t *testing.T) {
	f := &fakeDriver{screen: static()}
	start := time.Now()
	mustPass(t, runWith(t, f, quick, "- travel: {points: ['0,0', '0,0.001', '0.001,0.001'], speed: 100000}"))
	if d := time.Since(start); d > time.Second {
		t.Errorf("travel of ~220 m at 100 km/s took %s", d)
	}
	if len(f.locs) != 101 {
		t.Fatalf("%d locations, want 1 + 2*50", len(f.locs))
	}
	if f.locs[0] != [2]float64{0, 0} || f.locs[50] != [2]float64{0, 0.001} || f.locs[100] != [2]float64{0.001, 0.001} {
		t.Errorf("waypoints %v %v %v", f.locs[0], f.locs[50], f.locs[100])
	}
	if mid := f.locs[25]; mid[0] != 0 || mid[1] < 0.00049 || mid[1] > 0.00051 {
		t.Errorf("halfway point %v", mid)
	}
	// A speed from ${...} is read when the step runs.
	f1 := &fakeDriver{screen: static()}
	mustPass(t, runWith(t, f1, quick, "- travel: {points: ['0,0', '0,0.001'], speed: '${50 * 2000}'}"))
	if len(f1.locs) != 51 {
		t.Errorf("%d locations, want 51", len(f1.locs))
	}
	// No points: nothing to do.
	f0 := &fakeDriver{screen: static()}
	mustPass(t, runWith(t, f0, quick, "- travel: {points: []}"))
	if len(f0.locs) != 0 {
		t.Errorf("locations %v", f0.locs)
	}
	// One point: just set it.
	f2 := &fakeDriver{screen: static()}
	mustPass(t, runWith(t, f2, quick, "- travel: {points: ['1,2']}"))
	if !reflect.DeepEqual(f2.locs, [][2]float64{{1, 2}}) {
		t.Errorf("locations %v", f2.locs)
	}
}

// travel's simulated time is real: a slow walk takes about distance/speed.
func TestTravelTiming(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{screen: static()}
	start := time.Now()
	// ~11.1 m at 100 m/s: ~111 ms.
	mustPass(t, runWith(t, f, quick, "- travel: {points: ['0,0', '0,0.0001'], speed: 100}"))
	if d := time.Since(start); d < 90*time.Millisecond || d > time.Second {
		t.Errorf("took %s, want ~111 ms", d)
	}
}

// A location the device rejects stops the travel.
func TestTravelErrors(t *testing.T) {
	f := &fakeDriver{screen: static()}
	f.fail("SetLocation", errors.New("no gps"))
	mustFail(t, runWith(t, f, quick, "- travel: {points: ['0,0', '1,1']}"), "no gps")
	if len(f.locs) != 1 {
		t.Errorf("%d locations after the first failed", len(f.locs))
	}
	// Failing on the way, not at the start.
	f2 := &fakeDriver{screen: static()}
	n := 0
	fl, err := flow.ParseBytes(t.TempDir()+"/f.yaml", []byte("appId: app\n---\n- travel: {points: ['0,0', '0,0.0001'], speed: 100000}"))
	if err != nil {
		t.Fatal(err)
	}
	stepFail := &stepFailDriver{fakeDriver: f2, failAt: 3, n: &n}
	res := New(stepFail, quick).RunFlow(context.Background(), fl)
	mustFail(t, res, "step 3 failed")
}

// stepFailDriver fails the n-th SetLocation.
type stepFailDriver struct {
	*fakeDriver
	failAt int
	n      *int
}

func (d *stepFailDriver) SetLocation(ctx context.Context, la, lo float64) error {
	*d.n++
	if *d.n == d.failAt {
		return fmt.Errorf("step %d failed", *d.n)
	}
	return nil
}

// A travel is cut short when the run is cancelled.
func TestTravelCancelled(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{screen: static()}
	fl, err := flow.ParseBytes(t.TempDir()+"/f.yaml", []byte("appId: app\n---\n- travel: {points: ['0,0', '1,1'], speed: 1}"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	res := New(f, quick).RunFlow(ctx, fl)
	if !errors.Is(res.Err, context.DeadlineExceeded) {
		t.Errorf("err %v, want the context's", res.Err)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("cancelled travel took %s", d)
	}
}

// issue #44: travel with a speed that is not a finite number above 0 fails at once with the
// value, before any location is sent. At speed 0 the wait between points was +Inf seconds:
// on arm64 it never ended, on amd64 it was skipped; a negative speed skipped it too. A literal
// is rejected when the flow is read; a value with ${...} when the step runs.
func TestTravelBadSpeedFails(t *testing.T) {
	for _, tc := range []struct{ speed, want string }{
		{"0", `invalid speed "0"`},
		{"-1", `invalid speed "-1"`},
		{`"${0}"`, `invalid speed "0" (from "${0}")`},
		{`"${-2}"`, `invalid speed "-2" (from "${-2}")`},
		{`"${'fast'}"`, `invalid speed "fast" (from "${'fast'}")`},
		{`"${S}"`, `invalid speed "undefined" (from "${S}")`},
		{`"${''}"`, `invalid speed "" (from "${''}")`},
		{`"${1/0}"`, `invalid speed "Infinity" (from "${1/0}")`},
		{`"${0/0}"`, `invalid speed "NaN" (from "${0/0}")`},
	} {
		t.Run(tc.speed, func(t *testing.T) {
			t.Parallel()
			src := "appId: app\n---\n- travel: {points: ['0,0', '0,0.001'], speed: " + tc.speed + "}"
			fl, err := flow.ParseBytes(t.TempDir()+"/f.yaml", []byte(src))
			if err != nil { // a literal: caught when the flow is read
				if !contains(err.Error(), tc.want) {
					t.Fatalf("parse error %q, want it to contain %q", err, tc.want)
				}
				return
			}
			f := &fakeDriver{screen: static()}
			// Its own deadline, so a travel that never ends fails here instead of hanging.
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			res := New(f, quick).RunFlow(ctx, fl)
			if errors.Is(res.Err, context.DeadlineExceeded) {
				t.Fatalf("travel with speed %s did not end within 2s (%d locations sent)", tc.speed, len(f.locs))
			}
			mustFail(t, res, tc.want)
			if len(f.locs) != 0 {
				t.Errorf("%d locations sent before the failure", len(f.locs))
			}
		})
	}
}

// issue #113: setLocation with a latitude or longitude that is not a finite number in range
// (latitude from -90 to 90, longitude from -180 to 180) fails with the value, before anything
// is sent to the device. NaN and Inf were sent as they were. A literal is rejected when the
// flow is read; a value with ${...} when the step runs.
func TestSetLocationBadValueFails(t *testing.T) {
	for _, tc := range []struct{ lat, lon, want string }{
		{"NaN", "1", `invalid latitude "NaN"`},
		{"1", "-Inf", `invalid longitude "-Inf"`},
		{"91", "1", `invalid latitude "91"`},
		{"1", "180.1", `invalid longitude "180.1"`},
		{`"${0/0}"`, "1", `invalid latitude "NaN" (from "${0/0}"): expected a number from -90 to 90`},
		{`"${1/0}"`, "1", `invalid latitude "Infinity" (from "${1/0}")`},
		{`"${-1/0}"`, "1", `invalid latitude "-Infinity" (from "${-1/0}")`},
		{`"${90 + 0.5}"`, "1", `invalid latitude "90.5" (from "${90 + 0.5}")`},
		{`"${-91}"`, "1", `invalid latitude "-91" (from "${-91}")`},
		{`"${'north'}"`, "1", `invalid latitude "north" (from "${'north'}")`},
		{`"${LAT}"`, "1", `invalid latitude "undefined" (from "${LAT}")`},
		{`"${''}"`, "1", `invalid latitude "" (from "${''}")`},
		{"1", `"${0/0}"`, `invalid longitude "NaN" (from "${0/0}"): expected a number from -180 to 180`},
		{"1", `"${1/0}"`, `invalid longitude "Infinity" (from "${1/0}")`},
		{"1", `"${180 + 1}"`, `invalid longitude "181" (from "${180 + 1}")`},
		{"1", `"${-200}"`, `invalid longitude "-200" (from "${-200}")`},
	} {
		t.Run(tc.lat+","+tc.lon, func(t *testing.T) {
			t.Parallel()
			src := "appId: app\n---\n- setLocation: {latitude: " + tc.lat + ", longitude: " + tc.lon + "}"
			fl, err := flow.ParseBytes(t.TempDir()+"/f.yaml", []byte(src))
			if err != nil { // a literal: caught when the flow is read
				if !contains(err.Error(), tc.want) {
					t.Fatalf("parse error %q, want it to contain %q", err, tc.want)
				}
				return
			}
			f := &fakeDriver{screen: static()}
			mustFail(t, New(f, quick).RunFlow(context.Background(), fl), tc.want)
			if len(f.locs) != 0 {
				t.Errorf("locations sent before the failure: %v", f.locs)
			}
		})
	}
}

// issue #113: the edges of the ranges, and 0, are sent as they are, literal or from ${...}.
func TestSetLocationEdges(t *testing.T) {
	f := &fakeDriver{screen: static()}
	mustPass(t, runWith(t, f, quick, `- setLocation: {latitude: 90, longitude: 180}
- setLocation: {latitude: -90, longitude: -180}
- setLocation: {latitude: 0, longitude: 0}
- setLocation: {latitude: "${45 * 2}", longitude: "${-90 * 2}"}
- travel: {points: ["-90,180", "90,-180"], speed: 1e12}`))
	want := [][2]float64{{90, 180}, {-90, -180}, {0, 0}, {90, -180}, {-90, 180}}
	if len(f.locs) != 4+51 || !reflect.DeepEqual(f.locs[:5], want) || f.locs[len(f.locs)-1] != [2]float64{90, -180} {
		t.Errorf("locations %v", f.locs)
	}
}
