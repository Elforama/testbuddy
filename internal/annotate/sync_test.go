package annotate

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func ms(v ...int) []time.Duration {
	var out []time.Duration
	for _, x := range v {
		out = append(out, time.Duration(x)*time.Millisecond)
	}
	return out
}

// The video began 700 ms after the runner believed: each change comes 700 ms plus the app's
// reaction after the runner's touch. Offset finds the shift, to within the quickest reaction.
func TestOffsetFindsALateVideo(t *testing.T) {
	touches := ms(1000, 2500, 4000, 6200)
	changes := ms(1000+700+80, 2500+700+150, 4000+700+90, 6200+700+120, 9000) // 9000: a change no touch made
	s := Offset(touches, changes)
	if !s.OK || s.Matched != 4 || s.Shift < 700*time.Millisecond || s.Shift > 790*time.Millisecond {
		t.Errorf("%+v, want OK, 4 matched, shift 700-790 ms", s)
	}
}

// A video already in sync gets a shift no bigger than the app's quickest reaction.
func TestOffsetInSync(t *testing.T) {
	s := Offset(ms(1000, 2000, 3000), ms(1060, 2100, 3080))
	if !s.OK || s.Shift < 0 || s.Shift > 60*time.Millisecond {
		t.Errorf("%+v", s)
	}
}

// Too few touches, or touches the changes do not follow, give no verdict.
func TestOffsetUnsure(t *testing.T) {
	if s := Offset(ms(1000), ms(1100)); s.OK {
		t.Errorf("one touch: %+v", s)
	}
	if s := Offset(ms(1000, 9000, 17000, 25000), ms(1100)); s.OK {
		t.Errorf("one change for four touches: %+v", s)
	}
}

// With a real ffmpeg: Changes sees where the picture changes in a video.
func TestChangesWithFFmpeg(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("no ffmpeg")
	}
	path := filepath.Join(t.TempDir(), "v.mp4")
	// grey, then a blue band from 1.2 s, then a red screen from 2.5 s
	vf := "drawbox=x=0:y=300:w=360:h=120:color=blue:t=fill:enable='gte(t,1.2)',drawbox=x=0:y=0:w=360:h=640:color=red:t=fill:enable='gte(t,2.5)'"
	if out, err := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=c=gray:size=360x640:rate=25:duration=4",
		"-vf", vf, "-pix_fmt", "yuv420p", path).CombinedOutput(); err != nil {
		t.Skipf("ffmpeg cannot make a test video: %v %s", err, out)
	}
	got, err := Changes(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	want := ms(1200, 2500)
	if len(got) != len(want) {
		t.Fatalf("changes %v, want %v", got, want)
	}
	for i := range want {
		if d := got[i] - want[i]; d < 0 || d > 40*time.Millisecond { // within a frame
			t.Errorf("changes %v, want %v", got, want)
		}
	}
}

// CI, PR #392: an iOS simulator's video ran 780 ms ahead, with 15 of 24 touches followed by a
// clear change; a rule asking for four in five left it uncorrected, its captions and rings a
// second off. Most touches agreeing is enough; fewer than half is not.
func TestOffsetTakesMostTouchesAgreeing(t *testing.T) {
	var touches, changes []time.Duration
	for i := 0; i < 24; i++ {
		touches = append(touches, time.Duration(1000+i*1500+(i*i*137)%700)*time.Millisecond) // uneven, as real steps are
	}
	for i := 0; i < 15; i++ { // 15 of 24 touches followed by a change 780 ms (+50 ms) later
		changes = append(changes, touches[i]+830*time.Millisecond)
	}
	if s := Offset(touches, changes); !s.OK || s.Shift != 830*time.Millisecond {
		t.Errorf("15 of 24 touches agreeing: %+v, want OK at 830 ms", s)
	}
	if s := Offset(touches, changes[:11]); s.OK { // 11 of 24: under half
		t.Errorf("11 of 24 touches agreeing was taken as proof: %+v", s)
	}
}

// CI, PR #393: an iOS video began 2.4 s before the driver's start mark, and tb, taking the mark
// for the video's start, drew everything 2.4 s early. The status bar's clock changed at 2.43 s
// (the mark showing), 3.6 s (cleared), then 6.17 and 17.93 s (the app's own changes); the
// driver had held the mark about 0.4 s.
func TestFindMark(t *testing.T) {
	ci := ms(2433, 3600, 6167, 17933)
	if at, ok := FindMark(ci, 400*time.Millisecond); !ok || at != 2433*time.Millisecond {
		t.Errorf("CI's changes: %s, %v; want the mark at 2.433 s", at, ok)
	}
	// a video that begins on the mark shows only its clearing
	if at, ok := FindMark(ms(900, 6000), 400*time.Millisecond); !ok || at != 0 {
		t.Errorf("a video beginning on the mark: %s, %v; want 0", at, ok)
	}
	// changes far apart are no mark
	if at, ok := FindMark(ms(6167, 17933), 400*time.Millisecond); ok {
		t.Errorf("no mark found at %s", at)
	}
	if _, ok := FindMark(ci, 0); ok {
		t.Error("a mark found with none held")
	}
}
