package engine

import (
	"errors"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/ui"
)

func TestTakeScreenshot(t *testing.T) {
	dir := t.TempDir()
	f := &fakeDriver{screen: static(), shot: []byte("PNGDATA")}
	o := quick
	o.ArtifactsDir = dir
	abs := filepath.Join(t.TempDir(), "abs")
	mustPass(t, runWith(t, f, o, "- takeScreenshot: home\n- takeScreenshot: {path: \"sub/${'two'}\"}\n- takeScreenshot: "+abs))
	for _, p := range []string{filepath.Join(dir, "takeScreenshot", "home.png"), filepath.Join(dir, "takeScreenshot", "sub", "two.png"), abs + ".png"} {
		if b, err := os.ReadFile(p); err != nil || string(b) != "PNGDATA" {
			t.Errorf("%s: %q %v", p, b, err)
		}
	}
	if got := f.called("Screenshot"); len(got) != 3 || got[0] != "Screenshot 1 png" {
		t.Errorf("screenshot calls %q", got)
	}
}

func TestTakeScreenshotBadPaths(t *testing.T) {
	o := quick
	o.ArtifactsDir = t.TempDir()
	for _, tc := range []struct{ path, want string }{
		{"\"${''}\"", "invalid path"},
		{"dir/", "invalid path"},
		{"name.", "invalid path"},
		{"../../escape", "escapes the artifacts folder"},
	} {
		f := &fakeDriver{screen: static(), shot: []byte("x")}
		mustFail(t, runWith(t, f, o, "- takeScreenshot: "+tc.path), tc.want)
	}
	f := &fakeDriver{screen: static()}
	f.fail("Screenshot", errors.New("black screen"))
	mustFail(t, runWith(t, f, o, "- takeScreenshot: x"), "black screen")
}

func TestArtifactPathSiblingPrefix(t *testing.T) {
	o := quick
	o.ArtifactsDir = t.TempDir()
	f := &fakeDriver{screen: static(), shot: []byte("x")}
	mustFail(t, runWith(t, f, o, "- takeScreenshot: ../takeScreenshotEvil/a"), "escapes the artifacts folder")
}

func TestTakeScreenshotCropOn(t *testing.T) {
	dir := t.TempDir()
	img := pngOf(t, 800, 1600, colorWhite, imageRect(0, 200, 800, 300), colorRed)
	el := btn("Banner", "banner", 100)
	f := &fakeDriver{screen: static(el), shot: img}
	o := quick
	o.ArtifactsDir = dir
	mustPass(t, runWith(t, f, o, "- takeScreenshot: {path: crop, cropOn: {id: banner}}"))
	got := decodePNG(t, filepath.Join(dir, "takeScreenshot", "crop.png"))
	if b := got.Bounds(); b.Dx() != 800 || b.Dy() != 100 {
		t.Fatalf("cropped to %v, want 800x100", b)
	}
	if r, g, b, _ := got.At(got.Bounds().Min.X+10, got.Bounds().Min.Y+10).RGBA(); r>>8 != 255 || g != 0 || b != 0 {
		t.Errorf("cropped pixel %d,%d,%d: want the red banner", r>>8, g>>8, b>>8)
	}
}

func TestTakeScreenshotCropErrors(t *testing.T) {
	t.Parallel()
	o := quick
	o.ArtifactsDir = t.TempDir()
	thin := &ui.Node{ID: "thin", Bounds: ui.Rect{L: 10, T: 10, R: 10, B: 60}}
	f := &fakeDriver{screen: static(thin), shot: pngOf(t, 400, 800, colorWhite, imageRect(0, 0, 0, 0), colorRed)}
	mustFail(t, runWith(t, f, o, "- takeScreenshot: {path: a, cropOn: {id: nope}}"), "Element not found")
	if got := f.called("Screenshot"); len(got) != 0 {
		t.Errorf("a cropOn element that is not found takes no screenshot (issue #43), got %q", got)
	}
	mustFail(t, runWith(t, f, o, "- takeScreenshot: {path: a, cropOn: {id: thin}}"), "cropOn element has no size")
	f2 := &fakeDriver{screen: static(btn("B", "b", 0)), shot: []byte("not a png")}
	mustFail(t, runWith(t, f2, o, "- takeScreenshot: {path: a, cropOn: {id: b}}"), "png")
	mustFail(t, runWith(t, f2, o, "- takeScreenshot: {path: a, cropOn: {id: '${(}'}}"), "JavaScript error")
}

func bandAt(top int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 400, 800))
	for y := 0; y < 800; y++ {
		for x := 0; x < 400; x++ {
			c := colorWhite
			if top >= 0 && y >= top && y < top+50 {
				c = colorRed
			}
			img.Set(x, y, c)
		}
	}
	return img
}

func isRed(img image.Image, x, y int) bool {
	c := color.RGBAModel.Convert(img.At(img.Bounds().Min.X+x, img.Bounds().Min.Y+y)).(color.RGBA)
	return c == colorRed
}

func TestTakeScreenshotCropOnFindsBeforeShooting(t *testing.T) {
	dir := t.TempDir()
	var shown atomic.Bool
	card := btn("Card", "card", 100)
	f := &fakeDriver{}
	f.screen = func() []*ui.Node {
		if shown.Load() {
			return []*ui.Node{card}
		}
		return nil
	}
	f.draw = func() image.Image {
		if shown.Load() {
			return bandAt(100)
		}
		return bandAt(-1)
	}
	go func() {
		time.Sleep(100 * time.Millisecond)
		f.record("CardShown")
		shown.Store(true)
		f.change(true)
	}()
	o := quick
	o.ArtifactsDir = dir
	mustPass(t, runWith(t, f, o, "- takeScreenshot: {path: card, cropOn: {id: card}}"))
	f.mu.Lock()
	calls := slices.Clone(f.calls)
	f.mu.Unlock()
	appeared, shot := slices.Index(calls, "CardShown"), slices.Index(calls, "Screenshot 1 png")
	if shot < 0 || appeared < 0 || shot < appeared {
		t.Fatalf("the screenshot was taken before the cropOn element was on screen: calls %q", calls)
	}
	got := decodePNG(t, filepath.Join(dir, "takeScreenshot", "card.png"))
	if b := got.Bounds(); b.Dx() != 400 || b.Dy() != 50 || !isRed(got, 10, 10) {
		t.Errorf("cropped image %v: want the 400x50 red card", b)
	}
}

func TestTakeScreenshotCropOnRetakesWhenScreenMoves(t *testing.T) {
	t.Setenv("TB_CI", "1")
	dir := t.TempDir()
	var top atomic.Int64
	top.Store(100)
	f := &fakeDriver{}
	f.screen = func() []*ui.Node { return []*ui.Node{btn("Card", "card", int(top.Load()))} }
	var shots atomic.Int64
	f.draw = func() image.Image {
		if shots.Add(1) == 1 {
			top.Store(300)
			f.change(true)
		}
		return bandAt(int(top.Load()))
	}
	o := quick
	o.ArtifactsDir = dir
	mustPass(t, runWith(t, f, o, "- takeScreenshot: {path: card, cropOn: {id: card}}"))
	if got := f.called("Screenshot"); len(got) != 2 {
		t.Errorf("screenshot calls %q: want one retake after the screen moved", got)
	}
	got := decodePNG(t, filepath.Join(dir, "takeScreenshot", "card.png"))
	if b := got.Bounds(); b.Dx() != 400 || b.Dy() != 50 || !isRed(got, 10, 10) {
		t.Errorf("cropped image %v: want the red card where it is now (y 300-350)", b)
	}
}

func TestTakeScreenshotCropOnMovedOutsideCI(t *testing.T) {
	t.Setenv("TB_CI", "0")
	var top atomic.Int64
	top.Store(100)
	f := &fakeDriver{}
	f.screen = func() []*ui.Node { return []*ui.Node{btn("Card", "card", int(top.Load()))} }
	var shots atomic.Int64
	f.draw = func() image.Image {
		if shots.Add(1) == 1 {
			top.Store(300)
			f.change(true)
		}
		return bandAt(int(top.Load()))
	}
	o := quick
	o.ArtifactsDir = t.TempDir()
	mustFail(t, runWith(t, f, o, "- takeScreenshot: {path: card, cropOn: {id: card}}"),
		`cropOn element moved while the screenshot was taken: id: card was at [0,100][400,150], then [0,300][400,350]`)
	if got := f.called("Screenshot"); len(got) != 1 {
		t.Errorf("screenshot calls %q: want one", got)
	}
}

func TestTakeScreenshotCropOnScreenNeverStillElementStill(t *testing.T) {
	dir := t.TempDir()
	f := &fakeDriver{screen: static(btn("Card", "card", 100))}
	f.draw = func() image.Image {
		f.change(true)
		return bandAt(100)
	}
	o := quick
	o.ArtifactsDir = dir
	mustPass(t, runWith(t, f, o, "- takeScreenshot: {path: card, cropOn: {id: card}}"))
	if got := f.called("Screenshot"); len(got) != 1 {
		t.Errorf("screenshot calls %q: want one", got)
	}
	got := decodePNG(t, filepath.Join(dir, "takeScreenshot", "card.png"))
	if b := got.Bounds(); b.Dx() != 400 || b.Dy() != 50 || !isRed(got, 10, 10) {
		t.Errorf("cropped image %v: want the 400x50 red card", b)
	}
}

func TestTakeScreenshotCropOnElementNeverStill(t *testing.T) {
	t.Setenv("TB_CI", "1")
	var top atomic.Int64
	top.Store(100)
	f := &fakeDriver{}
	f.screen = func() []*ui.Node { return []*ui.Node{btn("Card", "card", int(top.Load()))} }
	f.draw = func() image.Image {
		img := bandAt(int(top.Load()))
		top.Add(10)
		f.change(true)
		return img
	}
	o := quick
	o.ArtifactsDir = t.TempDir()
	mustFail(t, runWith(t, f, o, "- takeScreenshot: {path: card, cropOn: {id: card}}"), "cropOn element kept moving")
	if got := f.called("Screenshot"); len(got) != cropShotTries {
		t.Errorf("screenshot calls %q: want %d tries", got, cropShotTries)
	}
}

func TestCropOnSizeIndependentOfSubPointOrigin(t *testing.T) {
	t.Parallel()
	pb := func(l, t, r, b float64) *[4]float64 { return &[4]float64{l, t, r, b} }
	for _, tc := range []struct {
		name         string
		size         [2]int
		img          [2]int
		bounds       ui.Rect
		precise      *[4]float64
		wantW, wantH int
	}{
		{"3x edges low", [2]int{402, 874}, [2]int{1206, 2622}, ui.Rect{L: 41, T: 283, R: 361, B: 446}, pb(41, 283.3333, 361, 446.4333), 960, 489},
		{"3x edges high", [2]int{402, 874}, [2]int{1206, 2622}, ui.Rect{L: 41, T: 283, R: 361, B: 447}, pb(41, 283.4667, 361, 446.5667), 960, 489},
		{"3x fraction above half", [2]int{402, 874}, [2]int{1206, 2622}, ui.Rect{L: 41, T: 283, R: 361, B: 447}, pb(41, 283, 361, 446.6), 960, 489},
		{"2x", [2]int{400, 800}, [2]int{800, 1600}, ui.Rect{L: 10, T: 100, R: 111, B: 151}, pb(10, 100.25, 110.5, 150.75), 200, 100},
		{"1.25x whole bounds", [2]int{400, 800}, [2]int{500, 1000}, ui.Rect{L: 3, T: 3, R: 8, B: 8}, nil, 6, 6},
		{"3x negative origin", [2]int{402, 874}, [2]int{1206, 2622}, ui.Rect{L: -11, T: -21, R: 90, B: 80}, pb(-10.5, -20.5, 90, 80), 300, 300},
	} {
		dir := t.TempDir()
		el := &ui.Node{ID: "el", Bounds: tc.bounds, PreciseBounds: tc.precise}
		f := &fakeDriver{screen: static(el), size: tc.size,
			shot: pngOf(t, tc.img[0], tc.img[1], colorWhite, imageRect(0, 0, 0, 0), colorRed)}
		o := quick
		o.ArtifactsDir = dir
		mustPass(t, runWith(t, f, o, "- takeScreenshot: {path: crop, cropOn: {id: el}}"))
		got := decodePNG(t, filepath.Join(dir, "takeScreenshot", "crop.png")).Bounds()
		if got.Dx() != tc.wantW || got.Dy() != tc.wantH {
			t.Errorf("%s: cropped to %dx%d, want %dx%d", tc.name, got.Dx(), got.Dy(), tc.wantW, tc.wantH)
		}
	}
}

func TestCropOnHairlineFails(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	el := &ui.Node{ID: "line", Bounds: ui.Rect{L: 0, T: 100, R: 402, B: 101}, PreciseBounds: &[4]float64{0, 100.4, 402, 100.6}}
	f := &fakeDriver{screen: static(el), size: [2]int{402, 874},
		shot: pngOf(t, 1206, 2622, colorWhite, imageRect(0, 0, 0, 0), colorRed)}
	o := quick
	o.ArtifactsDir = dir
	mustFail(t, runWith(t, f, o, "- takeScreenshot: {path: line, cropOn: {id: line}}"), "cropOn element crops to 1206x0 px")
	if _, err := os.Stat(filepath.Join(dir, "takeScreenshot", "line.png")); err == nil {
		t.Errorf("a crop of no pixels wrote a file")
	}
}

func TestAssertScreenshotOnePixelSizeTolerance(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	shot := pngOf(t, 40, 80, colorWhite, imageRect(0, 79, 40, 80), colorRed)
	writeFile(t, dir, "shorter.png", string(pngOf(t, 40, 79, colorWhite, imageRect(0, 0, 0, 0), colorRed)))
	writeFile(t, dir, "taller.png", string(pngOf(t, 41, 81, colorWhite, imageRect(0, 80, 41, 81), colorBlue)))
	writeFile(t, dir, "short3.png", string(pngOf(t, 40, 77, colorWhite, imageRect(0, 0, 0, 0), colorRed)))
	writeFile(t, dir, "halfoff.png", string(pngOf(t, 40, 79, colorWhite, imageRect(0, 0, 40, 40), colorBlue)))
	whole := &ui.Node{ID: "whole", Bounds: ui.Rect{L: 0, T: 0, R: 400, B: 800}}
	run := func(cmd string) FlowResult {
		return runSrc(t, &fakeDriver{screen: static(whole), shot: shot}, quick, dir, "appId: app\n---\n- "+cmd)
	}
	mustPass(t, run("assertScreenshot: {path: shorter, thresholdPercentage: 95, cropOn: {id: whole}}"))
	mustPass(t, run("assertScreenshot: {path: taller, thresholdPercentage: 95, cropOn: {id: whole}}"))
	mustFail(t, run("assertScreenshot: {path: short3, cropOn: {id: whole}}"), "screenshot size (40,80) differs from the reference (40,77)")
	mustFail(t, run("assertScreenshot: {path: halfoff, cropOn: {id: whole}}"), "below the threshold 95.0%")
	mustFail(t, run("assertScreenshot: {path: shorter, thresholdPercentage: 95}"), "screenshot size (40,80) differs from the reference (40,79)")
}
