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
	mustFail(t, runWith(t, &fakeDriver{screen: static()}, quick, "- setLocation: {latitude: north, longitude: '1'}"), `invalid location "north", "1"`)
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
