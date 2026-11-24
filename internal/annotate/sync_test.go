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

func TestOffsetFindsALateVideo(t *testing.T) {
	touches := ms(1000, 2500, 4000, 6200)
	changes := ms(1000+700+80, 2500+700+150, 4000+700+90, 6200+700+120, 9000)
	s := Offset(touches, changes)
	if !s.OK || s.Matched != 4 || s.Shift < 700*time.Millisecond || s.Shift > 790*time.Millisecond {
		t.Errorf("%+v, want OK, 4 matched, shift 700-790 ms", s)
	}
}

func TestOffsetInSync(t *testing.T) {
	s := Offset(ms(1000, 2000, 3000), ms(1060, 2100, 3080))
	if !s.OK || s.Shift < 0 || s.Shift > 60*time.Millisecond {
		t.Errorf("%+v", s)
	}
}

func TestOffsetUnsure(t *testing.T) {
	if s := Offset(ms(1000), ms(1100)); s.OK {
		t.Errorf("one touch: %+v", s)
	}
	if s := Offset(ms(1000, 9000, 17000, 25000), ms(1100)); s.OK {
		t.Errorf("one change for four touches: %+v", s)
	}
}

func TestChangesWithFFmpeg(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("no ffmpeg")
	}
	path := filepath.Join(t.TempDir(), "v.mp4")
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
		if d := got[i] - want[i]; d < 0 || d > 40*time.Millisecond {
			t.Errorf("changes %v, want %v", got, want)
		}
	}
}

func TestOffsetTakesMostTouchesAgreeing(t *testing.T) {
	var touches, changes []time.Duration
	for i := 0; i < 24; i++ {
		touches = append(touches, time.Duration(1000+i*1500+(i*i*137)%700)*time.Millisecond)
	}
	for i := 0; i < 15; i++ {
		changes = append(changes, touches[i]+830*time.Millisecond)
	}
	if s := Offset(touches, changes); !s.OK || s.Shift != 830*time.Millisecond {
		t.Errorf("15 of 24 touches agreeing: %+v, want OK at 830 ms", s)
	}
	if s := Offset(touches, changes[:11]); s.OK {
		t.Errorf("11 of 24 touches agreeing was taken as proof: %+v", s)
	}
}

func TestFindMark(t *testing.T) {
	ci := ms(2433, 3600, 6167, 17933)
	if at, ok := FindMark(ci, 400*time.Millisecond); !ok || at != 2433*time.Millisecond {
		t.Errorf("CI's changes: %s, %v; want the mark at 2.433 s", at, ok)
	}
	if at, ok := FindMark(ms(900, 6000), 400*time.Millisecond); !ok || at != 0 {
		t.Errorf("a video beginning on the mark: %s, %v; want 0", at, ok)
	}
	if at, ok := FindMark(ms(6167, 17933), 400*time.Millisecond); ok {
		t.Errorf("no mark found at %s", at)
	}
	if _, ok := FindMark(ci, 0); ok {
		t.Error("a mark found with none held")
	}
}
