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
