package session

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	_ "image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/elforama/testbuddy/internal/daemon"
	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/look"
	"github.com/elforama/testbuddy/internal/ui"
)

type lookArgs struct {
	devArgs
	All   bool    `json:"all"`
	Shot  bool    `json:"shot"`
	JSON  bool    `json:"json"`
	Scale float64 `json:"scale"`
	Wait  bool    `json:"wait"` // let the UI settle first
}

func (s *Service) look(ctx context.Context, req daemon.Request, send func(daemon.Response)) daemon.Response {
	a, err := decode[lookArgs](req)
	if err != nil {
		return fail(2, "%v", err)
	}
	d, err := s.resolve(ctx, a.devArgs)
	if err != nil {
		return fail(2, "%v", err)
	}
	if err := s.lockOpen(d); err != nil {
		return fail(1, "%v", err)
	}
	defer d.mu.Unlock()
	if a.Wait {
		d.Drv.WaitSettled(ctx, 3*time.Second)
	}
	// With --shot, the screenshot is taken while the tree is read: the two are independent
	// requests to the device, and one after the other cost their sum (~40 ms of a look on iOS).
	var shotPath string
	var shotErr error
	var shot sync.WaitGroup
	shotCtx, cancelShot := context.WithCancel(ctx)
	defer cancelShot()
	if a.Shot {
		shot.Add(1)
		go func() {
			defer shot.Done()
			shotPath, shotErr = s.saveShotTo(shotCtx, d, a.Scale, "", true)
		}()
	}
	scr, err := readNow(ctx, d)
	if err != nil {
		cancelShot() // the look fails: its screenshot is not wanted
	}
	shot.Wait()
	if shotErr == nil && shotPath != "" {
		if err != nil {
			os.Remove(shotPath)
		}
		// After the removal: a failed look does not cost the session one of its last shots.
		pruneLookShots(shotPath)
	}
	if err != nil {
		return fail(1, "%v", err)
	}
	l := look.Build(scr, d.refs)
	l.MarkNew(d.last)
	d.last = l
	// The JavaScript dialogs tb answered since the last command (web: a timer's; issue #91),
	// after the listing.
	notes := dialogNotes(d)
	resp := daemon.Response{OK: true}
	if a.JSON {
		// The caller reads the JSON alone: the text listing is not built.
		out := struct {
			*look.Listing
			Notes     []string `json:"notes,omitempty"`
			Shot      string   `json:"shot,omitempty"`
			ShotError string   `json:"shotError,omitempty"`
		}{Listing: l, Notes: notes, Shot: shotPath}
		if shotErr != nil {
			out.ShotError = shotErr.Error()
		}
		resp.JSON, _ = json.Marshal(out)
	} else {
		resp.Text = l.Text()
		for _, n := range notes {
			resp.Text += "note: " + n + "\n"
		}
	}
	if a.All {
		resp.Text += "\nall nodes:\n" + allNodes(scr)
	}
	if a.Shot {
		if shotErr != nil {
			resp.Text += "screenshot failed: " + shotErr.Error() + "\n"
		} else {
			resp.Text += "screenshot: " + shotPath + "\n"
		}
	}
	return resp
}

// readNow reads the screen as it is now, for a caller that checks state (look, the end of
// do): past Android's node cache, which misses a view moved by translation and text set with
// no accessibility event (issues #322, #331). Steps of a test keep their cached reads.
func readNow(ctx context.Context, d *Dev) (*ui.Screen, error) {
	if fr, ok := d.Drv.(freshReader); ok {
		return fr.ScreenFresh(ctx)
	}
	return d.Drv.Screen(ctx)
}

func allNodes(scr *ui.Screen) string { return dumpNodes(scr) }

type shotArgs struct {
	devArgs
	Scale float64 `json:"scale"`
	Path  string  `json:"path"`
}

// saveShotTo saves a screenshot to path, or with none to the shots folder: named after the
// session, with "look-" before the name for look's own (forLook), of which the folder keeps
// the last keptShots. A screenshot asked for (tb screenshot) is never removed.
func (s *Service) saveShotTo(ctx context.Context, d *Dev, scale float64, path string, forLook bool) (string, error) {
	auto := scale <= 0
	if auto {
		// ~500 px wide: enough to read, cheap for a model
		w, _ := d.Drv.Size()
		scale = 1
		switch {
		case w <= 0:
		case d.Info.Platform == device.IOS:
			scale = min(1, 500/(float64(w)*3)) // iOS sizes are points; screenshots are ~3x
		default:
			// Android sizes are pixels; the web's are CSS pixels, shot at 1x
			scale = min(1, 500/float64(w))
		}
	}
	img, err := d.Drv.Screenshot(ctx, scale, "png")
	if err != nil {
		return "", err
	}
	// The scale is a guess at the device's pixels per unit (the web's is right only at a device
	// pixel ratio of 1: headful Chrome on a Retina screen gives twice as many): the image is
	// brought to 500 px wide on every platform. One already narrow enough is not decoded.
	if auto || (d.Info.Platform == device.IOS && scale < 1) {
		img = downscalePNG(img, 500)
	}
	if path == "" {
		dir := filepath.Join(daemon.Dir(), "shots")
		os.MkdirAll(dir, 0o755)
		name := s.nameOf(d)
		if name == "" {
			name = d.Info.Serial
		}
		prefix := name + "-"
		if forLook {
			prefix = "look-" + prefix
		}
		path = filepath.Join(dir, prefix+time.Now().Format("150405.000")+".png")
	}
	if err := os.WriteFile(path, img, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// keptShots is how many screenshots of one session the shots folder keeps.
const keptShots = 20

// pruneLookShots prunes the session's look screenshots, named as the one at path
// ("look-alice-150405.000.png"), to the last keptShots.
func pruneLookShots(path string) {
	base := filepath.Base(path)
	i := strings.LastIndexByte(base, '-')
	if !strings.HasPrefix(base, "look-") || i < 0 {
		return
	}
	pruneShots(filepath.Dir(path), base[:i+1], keptShots)
}

// pruneShots removes all but the newest keep of the screenshots in dir named prefix and a
// time ("look-alice-150405.000.png").
func pruneShots(dir, prefix string, keep int) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type shot struct {
		path string
		at   time.Time
	}
	var shots []shot
	for _, e := range ents {
		rest, ok := strings.CutPrefix(e.Name(), prefix)
		// Only this session's: "look-alice-b-150405.000.png" is another's.
		if !ok || !strings.HasSuffix(rest, ".png") || strings.Contains(rest, "-") {
			continue
		}
		if info, err := e.Info(); err == nil {
			shots = append(shots, shot{filepath.Join(dir, e.Name()), info.ModTime()})
		}
	}
	if len(shots) <= keep {
		return
	}
	sort.Slice(shots, func(i, j int) bool { return shots[i].at.After(shots[j].at) })
	for _, sh := range shots[keep:] {
		os.Remove(sh.path)
	}
}

// downscalePNG shrinks an image to at most width pixels wide (box average).
func downscalePNG(data []byte, width int) []byte {
	// The header has the size: an image already narrow enough is not decoded.
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(data)); err != nil || cfg.Width <= width {
		return data
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil || src.Bounds().Dx() <= width {
		return data
	}
	b := src.Bounds()
	f := float64(b.Dx()) / float64(width)
	h := int(float64(b.Dy()) / f)
	dst := image.NewRGBA(image.Rect(0, 0, width, h))
	for y := 0; y < h; y++ {
		for x := 0; x < width; x++ {
			x0, y0 := b.Min.X+int(float64(x)*f), b.Min.Y+int(float64(y)*f)
			x1, y1 := b.Min.X+int(float64(x+1)*f), b.Min.Y+int(float64(y+1)*f)
			var r, g, bl, a, n uint32
			for yy := y0; yy < y1; yy++ {
				for xx := x0; xx < x1; xx++ {
					cr, cg, cb, ca := src.At(xx, yy).RGBA()
					r, g, bl, a, n = r+cr, g+cg, bl+cb, a+ca, n+1
				}
			}
			if n == 0 {
				n = 1
			}
			i := dst.PixOffset(x, y)
			dst.Pix[i], dst.Pix[i+1], dst.Pix[i+2], dst.Pix[i+3] = uint8(r/n>>8), uint8(g/n>>8), uint8(bl/n>>8), uint8(a/n>>8)
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, dst)
	return buf.Bytes()
}

func (s *Service) shot(ctx context.Context, req daemon.Request, send func(daemon.Response)) daemon.Response {
	a, err := decode[shotArgs](req)
	if err != nil {
		return fail(2, "%v", err)
	}
	d, err := s.resolve(ctx, a.devArgs)
	if err != nil {
		return fail(2, "%v", err)
	}
	if err := s.lockOpen(d); err != nil {
		return fail(1, "%v", err)
	}
	defer d.mu.Unlock()
	path := a.Path
	if path != "" && !filepath.IsAbs(path) {
		path = filepath.Join(req.Cwd, path)
	}
	p, err := s.saveShotTo(ctx, d, a.Scale, path, false)
	if err != nil {
		return fail(1, "%v", err)
	}
	return daemon.Response{OK: true, Text: p + "\n"}
}
