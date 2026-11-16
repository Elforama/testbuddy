package engine

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/elforama/testbuddy/internal/ci"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/ui"
)

// cropShotTries bounds how many times a cropOn screenshot is taken in CI mode when the element
// moves between reading its bounds and taking the image.
const cropShotTries = 3

// screenshot takes a full-size PNG of the screen. With crop, the element is found first (it
// may still be coming in), on a settled screen, and the image cut to its bounds (issue #43).
// The image is kept when nothing changed across it (the device's change counter held), or
// when the element is read again right after it at the same bounds: something else on the
// screen changed (a spinner, a clock), not the element. Otherwise the command fails, saying the
// element moved, rather than crop pixels from another screen; in CI mode it is taken again first.
func (r *Runner) screenshot(ctx context.Context, crop *flow.Selector) ([]byte, error) {
	if crop == nil {
		return r.Drv.Screenshot(ctx, 1, "png")
	}
	sel, err := r.resolveSelector(crop)
	if err != nil {
		return nil, err
	}
	// A CI allowance (package ci): an element still settling on a slow runner can move between
	// its read and the image. In CI the screenshot is taken up to cropShotTries times; elsewhere
	// once, as before issue #43's fix.
	tries := 1
	if ci.On() {
		tries = cropShotTries
	}
	var img []byte
	var n *ui.Node
	for try := 1; ; try++ {
		var s *ui.Screen
		n, s, err = r.findStable(ctx, sel, r.lookupTimeout(false), 0)
		if err != nil {
			return nil, err
		}
		if n.Bounds.W() <= 0 || n.Bounds.H() <= 0 {
			return nil, failf("cropOn element has no size")
		}
		img, err = r.Drv.Screenshot(ctx, 1, "png")
		if err != nil {
			return nil, err
		}
		if r.Drv.Seq() == s.Seq {
			break // nothing changed from the read to the image
		}
		after := "gone"
		if n2, _, _, err := r.find(ctx, sel, 0); err == nil {
			if sameBounds(n, n2) {
				break // the screen changed elsewhere; the element is where it was read
			}
			after = n2.Bounds.String()
		}
		if tries == 1 {
			return nil, failf("cropOn element moved while the screenshot was taken: %s was at %v, then %s",
				sel.Describe(), n.Bounds, after)
		}
		if try == tries {
			return nil, failf("cropOn element kept moving while the screenshot was taken (%d tries): %s was at %v, then %s",
				tries, sel.Describe(), n.Bounds, after)
		}
		r.tracef("%s moved as the screenshot was taken (%v, then %s): taking it again", sel.Describe(), n.Bounds, after)
	}
	src, err := png.Decode(bytes.NewReader(img))
	if err != nil {
		return nil, err
	}
	w, _ := r.Drv.Size()
	scale := float64(src.Bounds().Dx()) / float64(w)
	full := cropRect(n, scale)
	rect := full.Intersect(src.Bounds())
	if rect.Empty() {
		return nil, failf("cropOn element crops to %dx%d px: %s at %v has no pixels on the screenshot",
			max(0, full.Dx()), max(0, full.Dy()), sel.Describe(), n.Bounds)
	}
	sub, ok := src.(interface {
		SubImage(image.Rectangle) image.Image
	})
	if !ok {
		return img, nil
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, sub.SubImage(rect)); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// cropRect is the element's rectangle in the screenshot's pixels. Its origin and its size
// are each taken in whole points (sub-point values truncated toward zero), then scaled to
// pixels and truncated on their own; an origin off the top or left starts at 0 and keeps the
// size, as Maestro's toInt and coerceIn do. The size comes from the element's size alone,
// never from the difference of two separately rounded edges, so one element crops to one
// size wherever its sub-point origin falls (issue #323).
//
// This is not Maestro's arithmetic on iOS: Maestro truncates each edge (its height is
// trunc(bottom) - trunc(top)), so its size still depends on position, by up to a point
// (3 px at 3x, more than assertScreenshot's 1 px tolerance covers). Only iOS sends
// sub-point bounds. Android's bounds are whole pixels. The web agent rounds each CSS edge,
// so a web crop can still be 1 px off by position; the tolerance covers that.
func cropRect(n *ui.Node, scale float64) image.Rectangle {
	// whole truncates toward zero, forgiving the float error of a value just short of an integer.
	whole := func(v float64) float64 { return math.Trunc(v + math.Copysign(1e-6, v)) }
	x, y := float64(n.Bounds.L), float64(n.Bounds.T)
	w, h := float64(n.Bounds.W()), float64(n.Bounds.H())
	if b := n.PreciseBounds; b != nil {
		x, y, w, h = whole(b[0]), whole(b[1]), whole(b[2]-b[0]), whole(b[3]-b[1])
	}
	px, py := max(0, int(whole(x*scale))), max(0, int(whole(y*scale)))
	return image.Rect(px, py, px+int(whole(w*scale)), py+int(whole(h*scale)))
}

// sameBounds: two reads of an element are at the same place, to the sub-point edges when
// the platform gives them.
func sameBounds(a, b *ui.Node) bool {
	if a.Bounds != b.Bounds || (a.PreciseBounds == nil) != (b.PreciseBounds == nil) {
		return false
	}
	return a.PreciseBounds == nil || *a.PreciseBounds == *b.PreciseBounds
}

// imageExts are the image formats a reference screenshot's path may name (Maestro's set).
var imageExts = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".heic": true, ".heif": true,
	".gif": true, ".bmp": true, ".tif": true, ".tiff": true, ".wbmp": true}

func (r *Runner) assertScreenshot(ctx context.Context, c *flow.Command) error {
	path, err := r.eval(c.Text)
	if err != nil {
		return err
	}
	// A path naming an image format keeps its name; tb reads PNG and JPEG (other formats are
	// found, then refused, below). Anything else is a name without its .png.
	ext := strings.ToLower(filepath.Ext(path))
	if !imageExts[ext] {
		path += ".png"
	}
	ref := path
	if !strings.HasPrefix(ref, "/") {
		ref = dirOf(c.File) + "/" + path
		if _, err := os.Stat(ref); err != nil && r.opts.ArtifactsDir != "" {
			ref = r.opts.ArtifactsDir + "/takeScreenshot/" + path
		}
	}
	refData, err := os.ReadFile(ref)
	if err != nil {
		return failf("reference screenshot not found: %s", ref)
	}
	thr, err := r.eval(c.Threshold)
	if err != nil {
		return err
	}
	threshold, err := strconv.ParseFloat(thr, 64)
	if err != nil {
		return failf("invalid thresholdPercentage %q", thr)
	}
	actual, err := r.screenshot(ctx, c.CropOn)
	if err != nil {
		return err
	}
	if ext != ".png" && ext != ".jpg" && ext != ".jpeg" && imageExts[ext] {
		return failf("reference screenshot %s: tb reads PNG and JPEG references only; convert it to .png", ref)
	}
	a, _, err1 := image.Decode(bytes.NewReader(refData))
	b, _, err2 := image.Decode(bytes.NewReader(actual))
	if err1 != nil || err2 != nil {
		return failf("could not decode screenshots")
	}
	// A size that differs by at most 1 px in width and in height compares the common area,
	// aligned top-left (issue #323): a crop's edges can fall a pixel from where the
	// reference's did. A bigger difference fails on size. The threshold is unchanged.
	ab, bb := a.Bounds(), b.Bounds()
	if dx, dy := ab.Dx()-bb.Dx(), ab.Dy()-bb.Dy(); dx < -1 || dx > 1 || dy < -1 || dy > 1 {
		return failf("screenshot size %v differs from the reference %v", bb.Size(), ab.Size())
	}
	same, total := 0, 0
	for y := 0; y < min(ab.Dy(), bb.Dy()); y++ {
		for x := 0; x < min(ab.Dx(), bb.Dx()); x++ {
			r1, g1, b1, _ := a.At(ab.Min.X+x, ab.Min.Y+y).RGBA()
			r2, g2, b2, _ := b.At(bb.Min.X+x, bb.Min.Y+y).RGBA()
			dr, dg, db := float64(r1)-float64(r2), float64(g1)-float64(g2), float64(b1)-float64(b2)
			dist := math.Sqrt(dr*dr+dg*dg+db*db) / (65535 * math.Sqrt(3))
			if dist <= 0.1 {
				same++
			}
			total++
		}
	}
	pct := 100 * float64(same) / float64(max(1, total))
	if pct < threshold {
		return failf("screenshot matches %.1f%% of the reference, below the threshold %.1f%%", pct, threshold)
	}
	return nil
}
