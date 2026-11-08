package engine

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

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
	mustFail(t, runWith(t, f, o, "- takeScreenshot: {path: a, cropOn: {id: thin}}"), "cropOn element has no size")
	f2 := &fakeDriver{screen: static(btn("B", "b", 0)), shot: []byte("not a png")}
	mustFail(t, runWith(t, f2, o, "- takeScreenshot: {path: a, cropOn: {id: b}}"), "png")
	mustFail(t, runWith(t, f2, o, "- takeScreenshot: {path: a, cropOn: {id: '${(}'}}"), "JavaScript error")
}
