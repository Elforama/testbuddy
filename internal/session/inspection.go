package session

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	"image/png"
	"os"
	"path/filepath"
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
	d.mu.Lock()
	defer d.mu.Unlock()
	if a.Wait {
		d.Drv.WaitSettled(ctx, 3*time.Second)
	}
	scr, err := d.Drv.Screen(ctx)
	if err != nil {
		return fail(1, "%v", err)
	}
	l := look.Build(scr, d.refs)
	l.MarkNew(d.last)
	d.last = l
	resp := daemon.Response{OK: true, Text: l.Text()}
	if a.All {
		resp.Text += "\nall nodes:\n" + allNodes(scr)
	}
	if a.JSON {
		resp.JSON, _ = json.Marshal(l)
	}
	if a.Shot {
		path, err := s.saveShot(ctx, d, a.Scale)
		if err != nil {
			resp.Text += "screenshot failed: " + err.Error() + "\n"
		} else {
			resp.Text += "screenshot: " + path + "\n"
		}
	}
	return resp
}

func allNodes(scr *ui.Screen) string { return dumpNodes(scr) }

type shotArgs struct {
	devArgs
	Scale float64 `json:"scale"`
	Path  string  `json:"path"`
}

func (s *Service) saveShot(ctx context.Context, d *Dev, scale float64) (string, error) {
	return s.saveShotTo(ctx, d, scale, "")
}

func (s *Service) saveShotTo(ctx context.Context, d *Dev, scale float64, path string) (string, error) {
	if scale <= 0 {
		// ~500 px wide: enough to read, cheap for a model
		w, _ := d.Drv.Size()
		scale = 1
		if d.Info.Platform == device.Android && w > 0 {
			scale = min(1, 500/float64(w))
		} else if w > 0 {
			scale = min(1, 500/(float64(w)*3)) // iOS sizes are points; screenshots are ~3x
		}
	}
	img, err := d.Drv.Screenshot(ctx, scale, "png")
	if err != nil {
		return "", err
	}
	if d.Info.Platform == device.IOS && scale < 1 {
		img = downscalePNG(img, 500)
	}
	if path == "" {
		dir := filepath.Join(daemon.Dir(), "shots")
		os.MkdirAll(dir, 0o755)
		name := d.Name
		if name == "" {
			name = d.Info.Serial
		}
		path = filepath.Join(dir, fmt.Sprintf("%s-%s.png", name, time.Now().Format("150405.000")))
	}
	if err := os.WriteFile(path, img, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// downscalePNG shrinks an image to at most width pixels wide (box average).
func downscalePNG(data []byte, width int) []byte {
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
	d.mu.Lock()
	defer d.mu.Unlock()
	path := a.Path
	if path != "" && !filepath.IsAbs(path) {
		path = filepath.Join(req.Cwd, path)
	}
	p, err := s.saveShotTo(ctx, d, a.Scale, path)
	if err != nil {
		return fail(1, "%v", err)
	}
	return daemon.Response{OK: true, Text: p + "\n"}
}
