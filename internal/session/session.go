// Package session is what the daemon serves to agents: persistent device connections,
// named devices (alice, bob), compact screen listings with stable refs, command runs that
// answer with what changed, event-driven waits and watches, and recording passed commands
// into a flow.
package session

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/elforama/testbuddy/internal/connect"
	"github.com/elforama/testbuddy/internal/daemon"
	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/engine"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/look"
	"github.com/elforama/testbuddy/internal/ui"
)

// Dev is one connected device.
type Dev struct {
	mu        sync.Mutex // one command at a time per device
	Info      device.Info
	Name      string // alias, e.g. "alice"
	Drv       driver.Driver
	runner    *engine.Runner
	refs      *look.Refs
	last      *look.Listing
	AppID     string
	steps     []string      // YAML of commands that passed (refs resolved)
	lastDoEnd *look.Listing // the screen when the last `do` ended
}

// Service holds the devices.
type Service struct {
	mu    sync.Mutex
	devs  map[string]*Dev   // by serial
	names map[string]string // session name -> serial; kept when the connection drops
}

func New() *Service { return &Service{devs: map[string]*Dev{}, names: map[string]string{}} }

// Register installs the daemon handlers.
func (s *Service) Register(srv *daemon.Server) {
	srv.Handle("ping", func(ctx context.Context, req daemon.Request, send func(daemon.Response)) daemon.Response {
		return daemon.Response{OK: true, Text: daemon.BuildID()}
	})
	srv.Handle("devices", s.devices)
	srv.Handle("open", s.open)
	srv.Handle("close", s.close)
	srv.Handle("look", s.look)
	srv.Handle("do", s.do)
	srv.Handle("watch", s.watch)
	srv.Handle("shot", s.shot)
	srv.Handle("log", s.log)
	srv.Handle("save", s.save)
	srv.Handle("clear", s.clear)
	srv.Handle("stop", func(ctx context.Context, req daemon.Request, send func(daemon.Response)) daemon.Response {
		go func() { time.Sleep(50 * time.Millisecond); srv.Stop() }()
		return daemon.Response{OK: true, Text: "tb daemon stopped"}
	})
}

// Common args every device command takes.
type devArgs struct {
	Device   string `json:"device"`
	Platform string `json:"platform"`
}

func fail(exit int, format string, args ...any) daemon.Response {
	return daemon.Response{Exit: exit, Text: fmt.Sprintf(format, args...)}
}

// resolve finds (and connects, if needed) the device a command is for.
func (s *Service) resolve(ctx context.Context, a devArgs) (*Dev, error) {
	s.mu.Lock()
	// by alias, serial or name among connected devices
	if a.Device != "" {
		for _, d := range s.devs {
			if d.Name == a.Device || d.Info.Serial == a.Device || strings.EqualFold(d.Info.Name, a.Device) {
				s.mu.Unlock()
				return d, nil
			}
		}
	} else if len(s.devs) == 1 && a.Platform == "" {
		for _, d := range s.devs {
			s.mu.Unlock()
			return d, nil
		}
	} else if len(s.devs) > 1 && a.Platform == "" {
		var names []string
		for _, d := range s.devs {
			names = append(names, d.label())
		}
		sort.Strings(names)
		s.mu.Unlock()
		return nil, fmt.Errorf("several devices are open (%s): say which with -d", strings.Join(names, ", "))
	}
	// A named device whose connection dropped (a `tb test` run took the device's agent):
	// connect again under the same name.
	name, want := "", a.Device
	if serial, ok := s.names[a.Device]; ok {
		name, want = a.Device, serial
	}
	s.mu.Unlock()
	info, err := device.Pick(device.List(ctx), device.Platform(a.Platform), want)
	if err != nil {
		return nil, err
	}
	return s.connect(ctx, info, name)
}

func (d *Dev) label() string {
	if d.Name != "" {
		return d.Name + " (" + d.Info.Name + ")"
	}
	return d.Info.Name + " " + d.Info.Serial
}

func (s *Service) connect(ctx context.Context, info device.Info, name string) (*Dev, error) {
	s.mu.Lock()
	if d, ok := s.devs[info.Serial]; ok {
		if name != "" {
			d.Name = name
			s.names[name] = info.Serial
		}
		s.mu.Unlock()
		return d, nil
	}
	s.mu.Unlock()
	drv, err := connect.Open(ctx, info)
	if err != nil {
		return nil, err
	}
	d := &Dev{Info: info, Name: name, Drv: drv, refs: look.NewRefs()}
	d.runner = engine.New(drv, engine.Options{ArtifactsDir: filepath.Join(daemon.Dir(), "artifacts")})
	s.mu.Lock()
	s.devs[info.Serial] = d
	if name != "" {
		s.names[name] = info.Serial
	}
	s.mu.Unlock()
	go func() { // forget a device whose agent connection drops
		ev, stop := drv.Events()
		defer stop()
		for e := range ev {
			if e.Kind == "disconnected" {
				s.mu.Lock()
				if s.devs[info.Serial] == d {
					delete(s.devs, info.Serial)
				}
				s.mu.Unlock()
				return
			}
		}
	}()
	return d, nil
}

func decode[T any](req daemon.Request) (T, error) {
	var v T
	if len(req.Args) > 0 {
		if err := json.Unmarshal(req.Args, &v); err != nil {
			return v, err
		}
	}
	return v, nil
}

// ---- devices / open / close ----

func (s *Service) devices(ctx context.Context, req daemon.Request, send func(daemon.Response)) daemon.Response {
	all := device.List(ctx)
	s.mu.Lock()
	open := map[string]*Dev{}
	for k, d := range s.devs {
		open[k] = d
	}
	s.mu.Unlock()
	var b strings.Builder
	type jdev struct {
		device.Info
		Session string `json:"session,omitempty"`
		Open    bool   `json:"open"`
	}
	var out []jdev
	for _, d := range all {
		jd := jdev{Info: d}
		line := d.String()
		if o := open[d.Serial]; o != nil {
			jd.Open = true
			jd.Session = o.Name
			if o.Name != "" {
				line += "  session: " + o.Name
			} else {
				line += "  connected"
			}
		}
		out = append(out, jd)
		b.WriteString(line + "\n")
	}
	if len(all) == 0 {
		b.WriteString("no devices: start an Android emulator or boot an iOS simulator\n")
	}
	js, _ := json.Marshal(out)
	return daemon.Response{OK: true, Text: b.String(), JSON: js}
}

type openArgs struct {
	devArgs
	Name string `json:"name"`
	App  string `json:"app"`
}

func (s *Service) open(ctx context.Context, req daemon.Request, send func(daemon.Response)) daemon.Response {
	a, err := decode[openArgs](req)
	if err != nil {
		return fail(2, "%v", err)
	}
	info, err := device.Pick(device.List(ctx), device.Platform(a.Platform), a.Device)
	if err != nil {
		// maybe it is an alias of an open device
		if d, err2 := s.resolve(ctx, a.devArgs); err2 == nil {
			info = d.Info
		} else {
			return fail(2, "%v", err)
		}
	}
	s.mu.Lock()
	for _, d := range s.devs {
		if a.Name != "" && d.Name == a.Name && d.Info.Serial != info.Serial {
			s.mu.Unlock()
			return fail(2, "the name %q is already used by %s", a.Name, d.Info.Name)
		}
	}
	s.mu.Unlock()
	t0 := time.Now()
	d, err := s.connect(ctx, info, a.Name)
	if err != nil {
		return fail(1, "%v", err)
	}
	if a.App != "" {
		d.AppID = a.App
	}
	who := d.Info.String()
	if d.Name != "" {
		who = d.Name + ": " + who
	}
	return daemon.Response{OK: true, Text: fmt.Sprintf("connected %s in %s\n", who, time.Since(t0).Round(time.Millisecond))}
}

func (s *Service) close(ctx context.Context, req daemon.Request, send func(daemon.Response)) daemon.Response {
	a, err := decode[devArgs](req)
	if err != nil {
		return fail(2, "%v", err)
	}
	if a.Device == "all" {
		s.mu.Lock()
		for k, d := range s.devs {
			d.Drv.Close()
			delete(s.devs, k)
		}
		s.names = map[string]string{}
		s.mu.Unlock()
		return daemon.Response{OK: true, Text: "closed all devices\n"}
	}
	d, err := s.resolve(ctx, a)
	if err != nil {
		return fail(2, "%v", err)
	}
	s.mu.Lock()
	delete(s.devs, d.Info.Serial)
	if d.Name != "" {
		delete(s.names, d.Name)
	}
	s.mu.Unlock()
	d.Drv.Close()
	return daemon.Response{OK: true, Text: "closed " + d.label() + "\n"}
}

// ---- look ----

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

// ---- shot ----

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

// ---- do ----

type doArgs struct {
	devArgs
	Commands string            `json:"commands"` // YAML: one command or a list
	App      string            `json:"app"`
	Env      map[string]string `json:"env"`
}

func (s *Service) do(ctx context.Context, req daemon.Request, send func(daemon.Response)) daemon.Response {
	a, err := decode[doArgs](req)
	if err != nil {
		return fail(2, "%v", err)
	}
	d, err := s.resolve(ctx, a.devArgs)
	if err != nil {
		return fail(2, "%v", err)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if a.App != "" {
		d.AppID = a.App
	}
	var lastScreen = d.last
	text := a.Commands
	if lastScreen != nil {
		text, err = look.ResolveRefs(text, lastScreen, lastScreen.Screen)
	} else {
		text, err = look.ResolveRefs(text, nil, nil)
	}
	if err != nil {
		return fail(2, "%v", err)
	}
	cmds, err := flow.ParseCommands(req.Cwd, []byte(text))
	if err != nil {
		return fail(2, "%v", err)
	}
	items, _ := splitItems(text)
	appID := d.AppID
	if appID == "" {
		if fg, err := d.Drv.Foreground(ctx); err == nil {
			appID = fg
		}
	}
	var out strings.Builder
	exit := 0
	// Agent think time is not the app's: lookups count from now.
	runner := d.runner
	idx := 0
	var env []flow.KV
	for k, v := range a.Env {
		env = append(env, flow.KV{Key: k, Value: v})
	}
	runner.SetEnv(env)
	runErr := runner.RunCommands(ctx, cmds, appID, func(st engine.StepResult) {
		mark := "ok"
		switch st.Status {
		case engine.Failed:
			mark = "FAILED"
			exit = 1
		case engine.Warned:
			mark = "warned"
		case engine.Skipped:
			mark = "skipped"
		}
		fmt.Fprintf(&out, "%s %s  %s\n", mark, st.Desc, fmtDur(st.Duration))
		if st.Err != "" {
			fmt.Fprintf(&out, "  %s\n", strings.ReplaceAll(st.Err, "\n", "\n  "))
		}
		if st.Status != engine.Failed && idx < len(items) {
			d.steps = append(d.steps, items[idx])
		}
		idx++
	})
	var nf *engine.NotFound
	// Report the screen as it settles, not mid-change (an app that just launched has no
	// content yet). Answers at once when nothing is pending.
	d.Drv.WaitSettled(ctx, 3*time.Second)
	scr, serr := d.Drv.Screen(ctx)
	if serr == nil {
		now := look.Build(scr, d.refs)
		if errors.As(runErr, &nf) && nf.Query != "" {
			if sim := now.Similar(nf.Query); len(sim) > 0 {
				var parts []string
				for _, e := range sim {
					parts = append(parts, e.Line())
				}
				fmt.Fprintf(&out, "  similar: %s\n", strings.Join(parts, "; "))
			}
		}
		out.WriteString(look.Diff(d.last, now))
		d.last = now
		d.lastDoEnd = now
	}
	return daemon.Response{OK: exit == 0, Exit: exit, Text: out.String()}
}

// splitItems splits command YAML into one YAML snippet per top-level list item.
func splitItems(text string) ([]string, error) {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	var items []string
	var cur []string
	isList := false
	for _, l := range lines {
		if strings.HasPrefix(l, "- ") || l == "-" {
			isList = true
			if len(cur) > 0 {
				items = append(items, strings.Join(cur, "\n"))
			}
			cur = []string{l}
			continue
		}
		cur = append(cur, l)
	}
	if len(cur) > 0 {
		items = append(items, strings.Join(cur, "\n"))
	}
	if !isList {
		// a single command without a dash: make it a list item
		body := strings.Join(lines, "\n  ")
		return []string{"- " + body}, nil
	}
	return items, nil
}

func fmtDur(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

// ---- recording ----

func (s *Service) log(ctx context.Context, req daemon.Request, send func(daemon.Response)) daemon.Response {
	a, _ := decode[devArgs](req)
	d, err := s.resolve(ctx, a)
	if err != nil {
		return fail(2, "%v", err)
	}
	if len(d.steps) == 0 {
		return daemon.Response{OK: true, Text: "no commands recorded yet\n"}
	}
	return daemon.Response{OK: true, Text: strings.Join(d.steps, "\n") + "\n"}
}

type saveArgs struct {
	devArgs
	Path string `json:"path"`
	App  string `json:"app"`
}

func (s *Service) save(ctx context.Context, req daemon.Request, send func(daemon.Response)) daemon.Response {
	a, err := decode[saveArgs](req)
	if err != nil {
		return fail(2, "%v", err)
	}
	d, err := s.resolve(ctx, a.devArgs)
	if err != nil {
		return fail(2, "%v", err)
	}
	app := a.App
	if app == "" {
		app = d.AppID
	}
	if app == "" {
		if fg, err := d.Drv.Foreground(ctx); err == nil {
			app = fg
		}
	}
	path := a.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(req.Cwd, path)
	}
	body := fmt.Sprintf("appId: %s\n---\n%s\n", app, strings.Join(d.steps, "\n"))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fail(1, "%v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return fail(1, "%v", err)
	}
	return daemon.Response{OK: true, Text: fmt.Sprintf("saved %d commands to %s\n", len(d.steps), path)}
}

func (s *Service) clear(ctx context.Context, req daemon.Request, send func(daemon.Response)) daemon.Response {
	a, _ := decode[devArgs](req)
	d, err := s.resolve(ctx, a)
	if err != nil {
		return fail(2, "%v", err)
	}
	d.steps = nil
	return daemon.Response{OK: true, Text: "recording cleared\n"}
}
