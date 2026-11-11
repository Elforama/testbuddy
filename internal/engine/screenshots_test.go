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

// takeScreenshot writes the device's PNG under the artifacts folder (or at an absolute path).
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

// Screenshot paths that are empty, folders, or outside the artifacts folder are refused.
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

// A path that climbs out of the command's folder into a sibling folder with the same prefix
// is refused like any other escape.
func TestArtifactPathSiblingPrefix(t *testing.T) {
	o := quick
	o.ArtifactsDir = t.TempDir()
	f := &fakeDriver{screen: static(), shot: []byte("x")}
	mustFail(t, runWith(t, f, o, "- takeScreenshot: ../takeScreenshotEvil/a"), "escapes the artifacts folder")
}

// cropOn cuts the screenshot to the element, scaled from points to the image's pixels.
func TestTakeScreenshotCropOn(t *testing.T) {
	dir := t.TempDir()
	// The device image is 2x the screen's points (800x1600).
	img := pngOf(t, 800, 1600, colorWhite, imageRect(0, 200, 800, 300), colorRed)
	el := btn("Banner", "banner", 100) // points 0,100-400,150 = pixels 0,200-800,300
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

// cropOn errors: element missing, element without size, a screenshot that is no PNG.
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

// bandAt draws a 400x800 white screen with a red 50-point band at top (none when top < 0).
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

// isRed reports whether the pixel at x,y of img (relative to its bounds) is red.
func isRed(img image.Image, x, y int) bool {
	c := color.RGBAModel.Convert(img.At(img.Bounds().Min.X+x, img.Bounds().Min.Y+y)).(color.RGBA)
	return c == colorRed
}

// issue #43: with cropOn, the screenshot was taken before the element was looked up, so
// the image could show the screen from before the element appeared (and the crop used
// bounds from a later screen). The element is found first now, then the screenshot taken.
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

// issue #43: when the element moves between reading its bounds and taking the screenshot, in CI
// mode the bounds are read again and the screenshot retaken, so both come from the same screen.
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
			// The card moves down as the first screenshot is taken: its pixels are new.
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

// Outside CI the screenshot is taken once: an element that moved as it was taken fails the
// command, saying so, instead of a crop of pixels from another screen.
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

// issue #43: a screen that never stops changing elsewhere (a spinner, a clock) moves the
// change counter on every screenshot; the element itself stays put, so the first screenshot
// is good: one screenshot, cropped to the element.
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

// issue #43: an element that moves on every screenshot fails the command after a bounded
// number of tries (in CI mode), naming the element and its last two bounds, instead of
// cropping mismatched pixels.
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
