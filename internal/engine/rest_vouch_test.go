package engine

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/ui"
)

type vouchingDriver struct {
	*fakeDriver
	vouches bool
	asked   atomic.Int64
}

func (v *vouchingDriver) AtRest(ctx context.Context, index int, bounds ui.Rect) bool {
	v.asked.Add(1)
	return v.vouches
}

func tapReads(t *testing.T, vouches bool) (reads, asked int64, taps [][2]int) {
	t.Helper()
	f := &fakeDriver{}
	f.screen = func() []*ui.Node {
		return []*ui.Node{{Text: "Save", Bounds: ui.Rect{L: 100, T: 100, R: 300, B: 150}, Enabled: true, Clickable: true}}
	}
	v := &vouchingDriver{fakeDriver: f, vouches: vouches}
	fl, err := flow.ParseBytes(t.TempDir()+"/f.yaml", []byte("appId: app\n---\n- tapOn: Save"))
	if err != nil {
		t.Fatal(err)
	}
	r := New(v, Options{LookupTimeout: 3 * time.Second, OptionalTimeout: time.Second})
	if res := r.RunFlow(context.Background(), fl); res.Err != nil {
		t.Fatal(res.Err)
	}
	return f.reads.Load(), v.asked.Load(), f.taps
}

func TestTapSkipsRestReadsWhenTheDriverVouches(t *testing.T) {
	t.Parallel()
	plainReads, plainAsked, plainTaps := tapReads(t, false)
	vouchedReads, vouchedAsked, vouchedTaps := tapReads(t, true)
	if plainAsked == 0 || vouchedAsked == 0 {
		t.Fatalf("the driver was asked %d and %d times, want at least once each", plainAsked, vouchedAsked)
	}
	if vouchedReads >= plainReads {
		t.Errorf("%d screen reads when the driver vouches, %d when it does not: want fewer", vouchedReads, plainReads)
	}
	if len(plainTaps) != 1 || len(vouchedTaps) != 1 || plainTaps[0] != vouchedTaps[0] {
		t.Errorf("taps %v and %v, want the same single tap either way", plainTaps, vouchedTaps)
	}
}

func TestTapKeepsRestReadsWithoutAVoucher(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	f.screen = func() []*ui.Node {
		return []*ui.Node{{Text: "Save", Bounds: ui.Rect{L: 100, T: 100, R: 300, B: 150}, Enabled: true, Clickable: true}}
	}
	mustPass(t, runWith(t, f, quick, "- tapOn: Save"))
	refused, _, _ := tapReads(t, false)
	if got := f.reads.Load(); got != refused {
		t.Errorf("%d screen reads with no voucher, %d with one that refuses: want the same", got, refused)
	}
}
