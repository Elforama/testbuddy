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

	"github.com/elforama/testbuddy/internal/flow"
)

func (r *Runner) screenshot(ctx context.Context, crop *flow.Selector) ([]byte, error) {
	img, err := r.Drv.Screenshot(ctx, 1, "png")
	if err != nil {
		return nil, err
	}
	if crop == nil {
		return img, nil
	}
	sel, err := r.resolveSelector(crop)
	if err != nil {
		return nil, err
	}
	n, _, _, err := r.find(ctx, sel, r.lookupTimeout(false))
	if err != nil {
		return nil, err
	}
	if n.Bounds.W() <= 0 || n.Bounds.H() <= 0 {
		return nil, failf("cropOn element has no size")
	}
	src, err := png.Decode(bytes.NewReader(img))
	if err != nil {
		return nil, err
	}
	w, _ := r.Drv.Size()
	scale := float64(src.Bounds().Dx()) / float64(w)
	rect := image.Rect(int(float64(n.Bounds.L)*scale), int(float64(n.Bounds.T)*scale), int(float64(n.Bounds.R)*scale), int(float64(n.Bounds.B)*scale))
	if b := n.PreciseBounds; b != nil {
		rect = image.Rect(int(math.Round(b[0]*scale)), int(math.Round(b[1]*scale)),
			int(math.Round(b[2]*scale)), int(math.Round(b[3]*scale)))
	}
	sub, ok := src.(interface {
		SubImage(image.Rectangle) image.Image
	})
	if !ok {
		return img, nil
	}
	var buf bytes.Buffer
	png.Encode(&buf, sub.SubImage(rect.Intersect(src.Bounds())))
	return buf.Bytes(), nil
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
	if a.Bounds().Size() != b.Bounds().Size() {
		return failf("screenshot size %v differs from the reference %v", b.Bounds().Size(), a.Bounds().Size())
	}
	same, total := 0, 0
	ab, bb := a.Bounds(), b.Bounds()
	for y := 0; y < ab.Dy(); y++ {
		for x := 0; x < ab.Dx(); x++ {
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
