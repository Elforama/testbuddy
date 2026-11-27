package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	_ "image/jpeg"
	"log"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/elforama/testbuddy/internal/connect"
	"github.com/elforama/testbuddy/internal/daemon"
	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/devlock"
	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/engine"
	"github.com/elforama/testbuddy/internal/look"
)

type Dev struct {
	mu           sync.Mutex
	Info         device.Info
	Name         string
	owner        string
	Drv          driver.Driver
	runner       *engine.Runner
	refs         *look.Refs
	last         *look.Listing
	AppID        string
	flowApp      string
	steps        []string
	secrets      map[string]string
	hidden       map[string]bool
	lastDoEnd    *look.Listing
	lock         *devlock.Lock
	closed       bool
	agentStopped bool
	dialogMark   int
	watching     atomic.Int32
	holder       atomic.Pointer[running]

	stepsMu sync.Mutex
}

func (d *Dev) shut(stopAgent bool) error {
	r, canStop := d.Drv.(driver.Releaser)
	stop := stopAgent && canStop && !d.agentStopped
	release := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		r.Release(ctx)
		cancel()
		d.agentStopped = true
	}
	if !d.closed {
		d.closed = true
		if stop {
			release()
		} else {
			d.Drv.Close()
		}
		d.lock.Release()
		return nil
	}
	if !stop {
		return nil
	}
	l, err := devlock.TryAcquire(d.Info.Serial, "tb close (stopping tb's agent)")
	if b, busy := devlock.IsBusy(err); busy {
		return fmt.Errorf("tb's agent was left on it: the device is in use by %s", b.Owner)
	}
	defer l.Release()
	release()
	return nil
}

var queuedNoteAfter = time.Second

type running struct {
	what  string
	since time.Time
}

func describe(req daemon.Request) string {
	what := "tb " + req.Cmd
	var args map[string]any
	if json.Unmarshal(req.Args, &args) == nil {
		var cmds []string
		switch c := args["commands"].(type) {
		case string:
			for _, l := range strings.Split(c, "\n") {
				if strings.HasPrefix(l, "- ") {
					cmds = append(cmds, strings.TrimSpace(strings.TrimSuffix(l[2:], ":")))
				}
			}
			if len(cmds) == 0 {
				for _, l := range strings.Split(c, "\n") {
					if l = strings.TrimSpace(l); l != "" {
						cmds = append(cmds, l)
					}
				}
			}
		case []any:
			for _, x := range c {
				cmds = append(cmds, fmt.Sprint(x))
			}
		}
		if len(cmds) > 0 {
			what += " " + strings.Join(cmds, "; ")
		}
	}
	if r := []rune(what); len(r) > 80 {
		what = string(r[:79]) + "…"
	}
	return what
}

func (s *Service) lockOpen(d *Dev, req daemon.Request, send func(daemon.Response)) error {
	var a struct {
		NoWait bool `json:"no_wait"`
	}
	json.Unmarshal(req.Args, &a)
	return s.lockFor(d, describe(req), a.NoWait, send)
}

func (s *Service) lockFor(d *Dev, what string, noWait bool, send func(daemon.Response)) error {
	if err := s.waitLock(d, what, noWait, send); err != nil {
		return err
	}
	if d.closed {
		d.holder.Store(nil)
		d.mu.Unlock()
		return fmt.Errorf("the session on %s was closed while this command waited for it", s.labelOf(d))
	}
	return nil
}

func (s *Service) waitLock(d *Dev, what string, noWait bool, send func(daemon.Response)) error {
	if !d.mu.TryLock() {
		busy := func() string {
			if r := d.holder.Load(); r != nil {
				return fmt.Sprintf("%s (started %s ago)", r.what, time.Since(r.since).Round(time.Second))
			}
			return "another command"
		}
		if noWait {
			return fmt.Errorf("%s is running a command: %s; without --no-wait this one waits for it", s.labelOf(d), busy())
		}
		if send == nil {
			d.mu.Lock()
		} else {
			noted := make(chan struct{})
			t := time.AfterFunc(queuedNoteAfter, func() {
				send(daemon.Response{Stderr: true, Text: fmt.Sprintf("waiting for the command running on %s: %s\n", s.labelOf(d), busy())})
				close(noted)
			})
			d.mu.Lock()
			if !t.Stop() {
				<-noted
				send(daemon.Response{Stderr: true, Text: "now running " + what + "\n"})
			}
		}
	}
	d.holder.Store(&running{what: what, since: time.Now()})
	return nil
}

type Service struct {
	mu       sync.Mutex
	devs     map[string]*Dev
	names    map[string]string
	closing  map[string]*Dev
	dropped  map[string]*Dev
	stopping bool
	closed   map[string]string

	openDrv func(context.Context, device.Info) (driver.Driver, error)
}

func New() *Service {
	return &Service{devs: map[string]*Dev{}, names: map[string]string{}, closing: map[string]*Dev{},
		dropped: map[string]*Dev{}, closed: map[string]string{}, openDrv: connect.Open}
}

func (s *Service) Register(srv *daemon.Server) {
	srv.Handle("ping", func(ctx context.Context, req daemon.Request, send func(daemon.Response)) daemon.Response {
		return daemon.Response{OK: true, Text: daemon.BuildID()}
	})
	for cmd, h := range s.handlers() {
		srv.Handle(cmd, h)
	}
	srv.Handle("stop", func(ctx context.Context, req daemon.Request, send func(daemon.Response)) daemon.Response {
		daemon.StopAfterReply(ctx)
		return daemon.Response{OK: true, Text: "tb daemon stopped"}
	})
	srv.OnStop(s.CloseAll)
}

func (s *Service) handlers() map[string]daemon.Handler {
	hs := map[string]daemon.Handler{"devices": s.devices, "open": s.open, "close": s.close, "look": s.look,
		"locate": s.locate, "read": s.read, "collect": s.collect, "do": s.do, "go": s.goPath, "watch": s.watch, "shot": s.shot, "log": s.log, "applog": s.applog, "save": s.save,
		"clear": s.clear, "yield": s.yield}
	for cmd, h := range hs {
		hs[cmd] = s.hiding(h)
	}
	return hs
}

func (s *Service) CloseAll(ctx context.Context) {
	s.mu.Lock()
	seen := map[*Dev]bool{}
	var devs []*Dev
	for _, m := range []map[string]*Dev{s.devs, s.closing, s.dropped} {
		for _, d := range m {
			if !seen[d] {
				seen[d] = true
				devs = append(devs, d)
			}
		}
	}
	s.devs = map[string]*Dev{}
	s.names = map[string]string{}
	s.dropped = map[string]*Dev{}
	s.stopping = true
	s.mu.Unlock()
	var wg sync.WaitGroup
	for _, d := range devs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.waitLock(d, "closing the session (the daemon is stopping)", false, nil)
			defer d.mu.Unlock()
			d.shut(false)
		}()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		log.Printf("closing the devices took too long; killing what can be killed")
		for _, d := range devs {
			if k, ok := d.Drv.(driver.Killer); ok {
				k.Kill()
			}
		}
	}
}

type devArgs struct {
	Device   string `json:"device"`
	Platform string `json:"platform"`
	Owner    string `json:"owner,omitempty"`
	Force    bool   `json:"force,omitempty"`
}

func fail(exit int, format string, args ...any) daemon.Response {
	return daemon.Response{Exit: exit, Text: fmt.Sprintf(format, args...)}
}

func (d *Dev) matches(a devArgs) bool {
	if a.Platform != "" && a.Platform != string(d.Info.Platform) {
		return false
	}
	return d.Name == a.Device || d.Info.Serial == a.Device || strings.EqualFold(d.Info.Name, a.Device)
}

func (s *Service) openDev(a devArgs) (*Dev, error) {
	if a.Device != "" {
		for _, d := range s.devs {
			if d.matches(a) {
				if a.denies(d) {
					return nil, errForeign(d)
				}
				return d, nil
			}
		}
		return nil, nil
	}
	var found []*Dev
	for _, d := range onPlatform(s.devs, a.Platform) {
		if !d.foreign(a.Owner) {
			found = append(found, d)
		}
	}
	if len(found) > 1 {
		var names []string
		for _, d := range found {
			names = append(names, d.label())
		}
		sort.Strings(names)
		return nil, fmt.Errorf("several devices are open (%s): say which with -d", strings.Join(names, ", "))
	}
	if len(found) == 1 {
		return found[0], nil
	}
	return nil, nil
}

func onPlatform(m map[string]*Dev, platform string) []*Dev {
	var found []*Dev
	for _, d := range m {
		if platform == "" || platform == string(d.Info.Platform) {
			found = append(found, d)
		}
	}
	return found
}

func (s *Service) resolveDev(ctx context.Context, a devArgs) (*Dev, error) {
	a.Force = false
	s.mu.Lock()
	if d, err := s.openDev(a); d != nil || err != nil {
		s.mu.Unlock()
		return d, err
	}
	name, want := "", a.Device
	if serial, ok := s.names[a.Device]; ok {
		name, want = a.Device, serial
		if d := s.dropped[serial]; d != nil && d.Name == name && a.denies(d) {
			err := errForeign(d)
			s.mu.Unlock()
			return nil, err
		}
	}
	closedOn, wasClosed := s.closed[a.Device]
	s.mu.Unlock()
	devs := device.List(ctx)
	info, err := device.PickOne(devs, device.Platform(a.Platform), want)
	if err != nil {
		if a.Device != "" && name == "" && !named(devs, a.Device) {
			if wasClosed {
				return nil, fmt.Errorf("session %q (on %s) was closed: open it again with tb open -d %s --name %s", a.Device, closedOn, closedOn, a.Device)
			}
			var serials []string
			for _, d := range devs {
				if d.Platform != device.Web {
					serials = append(serials, d.Serial)
				}
			}
			list := "no phone or simulator connected"
			if len(serials) > 0 {
				list = strings.Join(serials, ", ")
			}
			return nil, fmt.Errorf("no session or connected device is named %q. Sessions end with tb close or after 30 minutes idle: open one with tb open -d DEVICE --name %s (devices: %s)", a.Device, a.Device, list)
		}
		return nil, err
	}
	return s.connect(ctx, info, name, a.Owner)
}

func named(devs []device.Info, want string) bool {
	for _, d := range devs {
		if d.Serial == want || strings.EqualFold(d.Name, want) {
			return true
		}
	}
	return false
}

func (d *Dev) label() string {
	if d.Name != "" {
		return d.Name + " (" + d.Info.Name + ")"
	}
	return d.Info.Name + " " + d.Info.Serial
}

func (d *Dev) foreign(owner string) bool { return d.owner != "" && d.owner != owner }

type foreignError struct{ msg string }

func (e *foreignError) Error() string { return e.msg }

func errForeign(d *Dev) error {
	if d.Name != "" {
		return &foreignError{fmt.Sprintf("%s is session %q of another agent: use another device", d.Info.Serial, d.Name)}
	}
	return &foreignError{fmt.Sprintf("%s has a session of another agent: use another device", d.Info.Serial)}
}

func (a devArgs) denies(d *Dev) bool { return d.foreign(a.Owner) && !a.Force }

func (s *Service) labelOf(d *Dev) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return d.label()
}

func (s *Service) nameOf(d *Dev) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return d.Name
}

func (s *Service) connect(ctx context.Context, info device.Info, name, owner string) (*Dev, error) {
	s.mu.Lock()
	if d := s.dropped[info.Serial]; d != nil && d.foreign(owner) {
		err := errForeign(d)
		s.mu.Unlock()
		return nil, err
	}
	if name != "" {
		if serial, ok := s.names[name]; ok {
			if d := s.dropped[serial]; d != nil && d.Name == name && d.foreign(owner) {
				err := errForeign(d)
				s.mu.Unlock()
				return nil, err
			}
		}
	}
	if d, ok := s.devs[info.Serial]; ok {
		if d.foreign(owner) {
			err := errForeign(d)
			s.mu.Unlock()
			return nil, err
		}
		if info.Headless && !d.Info.Headless {
			s.mu.Unlock()
			return nil, fmt.Errorf("the browser session is already open; close it before opening it headless")
		}
		if name != "" && d.Name != "" && d.Name != name {
			s.mu.Unlock()
			return nil, fmt.Errorf("%s (%s) is already open as session %q. One session per device: use -d %s, or close it first (tb close -d %s)",
				info.Name, info.Serial, d.Name, d.Name, d.Name)
		}
		if name != "" {
			d.Name = name
			s.names[name] = info.Serial
			delete(s.closed, name)
		}
		s.mu.Unlock()
		return d, nil
	}
	s.mu.Unlock()
	var lock *devlock.Lock
	if info.Platform != device.Web {
		label := info.Serial
		if name != "" {
			label = name
		}
		l, err := devlock.TryAcquire(info.Serial, fmt.Sprintf("tb session %q (tb open/do/look)", label))
		if b, busy := devlock.IsBusy(err); busy {
			s.mu.Lock()
			old := s.closing[info.Serial]
			s.mu.Unlock()
			if old != nil {
				return nil, fmt.Errorf("%s (%s) is still closing: its connection dropped while a command ran on it. Try again in a moment", info.Name, info.Serial)
			}
			return nil, fmt.Errorf("%s (%s) is in use by %s. One tb at a time per device: wait for it to finish, or use another device", info.Name, info.Serial, b.Owner)
		}
		lock = l
	}
	drv, err := s.openDrv(ctx, info)
	if err != nil {
		lock.Release()
		return nil, err
	}
	ev, stopEvents := drv.Events()
	d := &Dev{Info: info, Name: name, owner: owner, Drv: drv, refs: look.NewRefs(), lock: lock}
	d.runner = engine.New(drv, engine.Options{ArtifactsDir: filepath.Join(daemon.Dir(), "artifacts")})
	markDialogs(d)
	s.mu.Lock()
	if s.stopping {
		s.mu.Unlock()
		stopEvents()
		drv.Close()
		lock.Release()
		return nil, fmt.Errorf("the tb daemon is stopping")
	}
	s.devs[info.Serial] = d
	delete(s.dropped, info.Serial)
	if name != "" {
		s.names[name] = info.Serial
		delete(s.closed, name)
	}
	s.mu.Unlock()
	go func() {
		dropped := false
		for e := range ev {
			if e.Kind == "disconnected" {
				dropped = true
				break
			}
		}
		stopEvents()
		if !dropped {
			return
		}
		s.mu.Lock()
		if s.devs[info.Serial] == d {
			delete(s.devs, info.Serial)
			s.dropped[info.Serial] = d
		}
		s.closing[info.Serial] = d
		s.mu.Unlock()
		s.waitLock(d, "closing the session (its connection dropped)", false, nil)
		d.shut(false)
		d.mu.Unlock()
		s.mu.Lock()
		if s.closing[info.Serial] == d {
			delete(s.closing, info.Serial)
		}
		s.mu.Unlock()
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

func (s *Service) devices(ctx context.Context, req daemon.Request, send func(daemon.Response)) daemon.Response {
	a, _ := decode[devArgs](req)
	all := device.List(ctx)
	s.mu.Lock()
	open := map[string]string{}
	foreign := map[string]bool{}
	for k, d := range s.devs {
		open[k] = d.Name
		foreign[k] = d.foreign(a.Owner)
	}
	closing := map[string]bool{}
	for k := range s.closing {
		closing[k] = true
	}
	s.mu.Unlock()
	var b strings.Builder
	type jdev struct {
		device.Info
		Session string         `json:"session,omitempty"`
		Open    bool           `json:"open"`
		Others  bool           `json:"others,omitempty"`
		InUse   *devlock.Owner `json:"in_use,omitempty"`
		Free    *bool          `json:"free,omitempty"`
		Closing bool           `json:"closing,omitempty"`
	}
	var out []jdev
	for _, d := range all {
		jd := jdev{Info: d}
		line := d.String()
		if name, ok := open[d.Serial]; ok {
			jd.Open = true
			jd.Session = name
			if name != "" {
				line += "  session: " + name
			} else {
				line += "  connected"
			}
			if foreign[d.Serial] {
				jd.Others = true
				line += " (another agent's)"
			}
		} else if closing[d.Serial] {
			jd.Closing = true
			line += "  closing (this daemon's session; its connection dropped)"
		} else if d.Platform != device.Web {
			switch o, st := devlock.Probe(d.Serial); st {
			case devlock.Held:
				jd.InUse = &o
				jd.Free = new(bool)
				line += "  in use: " + o.String()
			case devlock.Free:
				free := true
				jd.Free = &free
				line += "  free"
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
	Name     string `json:"name"`
	App      string `json:"app"`
	Headless bool   `json:"headless"`
}

func (s *Service) open(ctx context.Context, req daemon.Request, send func(daemon.Response)) daemon.Response {
	a, err := decode[openArgs](req)
	if err != nil {
		return fail(2, "%v", err)
	}
	info, err := device.PickOne(device.List(ctx), device.Platform(a.Platform), a.Device)
	if err != nil {
		if a.Device == "" {
			return fail(2, "%v", err)
		}
		if d, err2 := s.resolve(ctx, a.devArgs); err2 == nil {
			info = d.Info
		} else {
			return fail(2, "%v", err2)
		}
	}
	if a.Headless && info.Platform != device.Web {
		return fail(2, "--headless requires a web session")
	}
	if info.Platform == device.Web && a.App != "" && !device.IsURL(a.App) {
		return fail(2, "web sessions need a URL for --app")
	}
	info.Headless = info.Headless || a.Headless
	s.mu.Lock()
	for _, d := range s.devs {
		if a.Name != "" && d.Name == a.Name && d.Info.Serial != info.Serial {
			s.mu.Unlock()
			return fail(2, "the name %q is already used by %s", a.Name, d.Info.Name)
		}
	}
	s.mu.Unlock()
	t0 := time.Now()
	d, err := s.connect(ctx, info, a.Name, a.Owner)
	if err != nil {
		return fail(1, "%v", err)
	}
	who := d.Info.String()
	if name := s.nameOf(d); name != "" {
		who = name + ": " + who
	}
	connected := fmt.Sprintf("connected %s in %s\n", who, time.Since(t0).Round(time.Millisecond))
	if a.App == "" {
		return daemon.Response{OK: true, Text: connected + s.frontLine(ctx, d) + s.pathsLine(ctx, d, req.Cwd)}
	}
	if err := s.lockOpen(d, req, send); err != nil {
		return fail(1, "%v", err)
	}
	hadApp, hadFlowApp := d.AppID, d.flowApp
	d.stepsMu.Lock()
	recorded := len(d.steps) > 0
	d.stepsMu.Unlock()
	switch {
	case !recorded:
		d.flowApp = a.App
	case d.flowApp == "":
		d.flowApp = sessionApp(ctx, d)
	}
	d.AppID = a.App
	d.last = nil
	d.mu.Unlock()
	resp := s.launch(ctx, d, a, req, send)
	if resp.Exit != 0 && s.keepApp(d, hadApp, hadFlowApp) {
		if !strings.HasSuffix(resp.Text, "\n") {
			resp.Text += "\n"
		}
		resp.Text += fmt.Sprintf("the session is open, but %s did not launch: fix that, then tb open --app again\n", a.App)
	}
	resp.Text = connected + resp.Text + s.pathsLine(ctx, d, req.Cwd)
	return resp
}

func (s *Service) launch(ctx context.Context, d *Dev, a openArgs, req daemon.Request, send func(daemon.Response)) daemon.Response {
	var wait struct {
		NoWait bool `json:"no_wait"`
	}
	json.Unmarshal(req.Args, &wait)
	req.Cmd = "do"
	req.Args, _ = json.Marshal(map[string]any{"device": d.Info.Serial, "owner": a.Owner, "no_wait": wait.NoWait,
		"app": a.App, "commands": "- launchApp\n"})
	return s.do(ctx, req, send)
}

func (s *Service) keepApp(d *Dev, app, flowApp string) bool {
	s.waitLock(d, "open", false, nil)
	defer d.mu.Unlock()
	s.mu.Lock()
	open := s.devs[d.Info.Serial] == d && !d.closed
	s.mu.Unlock()
	if open {
		if app != "" {
			d.AppID = app
		}
		d.flowApp = flowApp
	}
	return open
}

func (s *Service) frontLine(ctx context.Context, d *Dev) string {
	if !d.mu.TryLock() {
		return ""
	}
	front, err := d.Drv.Foreground(ctx)
	d.mu.Unlock()
	if err != nil || front == "" {
		return ""
	}
	return fmt.Sprintf("in front: %s (nothing was launched: --app does that)\n", front)
}

func (s *Service) close(ctx context.Context, req daemon.Request, send func(daemon.Response)) daemon.Response {
	a, err := decode[devArgs](req)
	if err != nil {
		return fail(2, "%v", err)
	}
	if a.Device == "all" {
		s.mu.Lock()
		var devs []*Dev
		serials := map[string]bool{}
		left := 0
		for _, m := range []map[string]*Dev{s.devs, s.dropped} {
			for _, d := range onPlatform(m, a.Platform) {
				if a.denies(d) {
					left++
					continue
				}
				devs = append(devs, d)
				serials[d.Info.Serial] = true
			}
		}
		s.mu.Unlock()
		if a.Platform != "" && len(devs) == 0 {
			return daemon.Response{OK: true, Text: fmt.Sprintf("no %s session is open: nothing to close\n", a.Platform)}
		}
		notes := make([]string, len(devs))
		var wg sync.WaitGroup
		for i, d := range devs {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if label, err := s.closeDev(ctx, d); err != nil && !errors.Is(err, errCloseCancelled) {
					notes[i] = label + ": " + err.Error()
				}
			}()
		}
		wg.Wait()
		s.mu.Lock()
		for name, serial := range s.names {
			_, open := s.devs[serial]
			_, dropped := s.dropped[serial]
			if !open && !dropped && (a.Platform == "" || serials[serial]) {
				delete(s.names, name)
				s.closed[name] = serial
			}
		}
		s.mu.Unlock()
		if ctx.Err() != nil {
			return fail(1, "tb close was cancelled: the sessions it had not closed yet are left open\n")
		}
		text := "closed all devices\n"
		if a.Platform != "" {
			text = fmt.Sprintf("closed all %s sessions\n", a.Platform)
		}
		for _, n := range notes {
			if n != "" {
				text += "  " + n + "\n"
			}
		}
		if left > 0 {
			text += fmt.Sprintf("  left %d session(s) of other agents open (tb close all --force closes them too)\n", left)
		}
		return daemon.Response{OK: true, Text: text}
	}
	s.mu.Lock()
	d, err := s.openDev(a)
	if d == nil && err == nil {
		d, err = s.droppedDev(a)
	}
	s.mu.Unlock()
	if err != nil {
		return fail(2, "%v", err)
	}
	if d == nil {
		if a.Device == "" {
			return daemon.Response{OK: true, Text: "no tb session is open: nothing to close\n"}
		}
		devs := device.List(ctx)
		info, err := device.Pick(devs, device.Platform(a.Platform), a.Device)
		if err != nil {
			s.mu.Lock()
			on, wasClosed := s.closed[a.Device]
			s.mu.Unlock()
			switch {
			case wasClosed:
				return fail(2, "session %q (on %s) was closed already: nothing to close", a.Device, on)
			case !named(devs, a.Device):
				return fail(2, "no session or connected device is named %q: nothing to close", a.Device)
			}
			return fail(2, "%v", err)
		}
		s.mu.Lock()
		d = s.dropped[info.Serial]
		if d != nil && a.denies(d) {
			err := errForeign(d)
			s.mu.Unlock()
			return fail(2, "%v", err)
		}
		s.mu.Unlock()
		if d == nil {
			return daemon.Response{OK: true, Text: fmt.Sprintf("no tb session is open on %s %s: nothing to close\n", info.Name, info.Serial)}
		}
	}
	label, err := s.closeDev(ctx, d)
	if errors.Is(err, errCloseCancelled) {
		return fail(1, "tb close was cancelled: %s is left open\n", label)
	}
	text := "closed " + label + "\n"
	if err != nil {
		text += "  " + err.Error() + "\n"
	}
	return daemon.Response{OK: true, Text: text}
}

var errCloseCancelled = errors.New("tb close was cancelled")

type YieldArgs struct {
	Serial string `json:"serial"`
	Owner  string `json:"owner,omitempty"`
}

type Yielded struct {
	Yielded bool   `json:"yielded"`
	Busy    bool   `json:"busy,omitempty"`
	Label   string `json:"label,omitempty"`
}

func (s *Service) yield(ctx context.Context, req daemon.Request, send func(daemon.Response)) daemon.Response {
	a, err := decode[YieldArgs](req)
	if err != nil {
		return fail(2, "%v", err)
	}
	answer := func(y Yielded) daemon.Response {
		b, _ := json.Marshal(y)
		return daemon.Response{OK: true, JSON: b}
	}
	s.mu.Lock()
	d := s.devs[a.Serial]
	if d == nil {
		if c := s.closing[a.Serial]; c != nil {
			label := c.label()
			s.mu.Unlock()
			return answer(Yielded{Busy: true, Label: label})
		}
	}
	s.mu.Unlock()
	if d == nil || d.foreign(a.Owner) {
		return answer(Yielded{})
	}
	if d.watching.Load() > 0 || !d.mu.TryLock() {
		return answer(Yielded{Busy: true, Label: s.labelOf(d)})
	}
	defer d.mu.Unlock()
	if d.watching.Load() > 0 {
		return answer(Yielded{Busy: true, Label: s.labelOf(d)})
	}
	d.holder.Store(&running{what: "tb test (taking the device)", since: time.Now()})
	s.mu.Lock()
	label := d.label()
	if s.devs[a.Serial] == d {
		delete(s.devs, a.Serial)
		s.dropped[a.Serial] = d
	}
	s.mu.Unlock()
	d.shut(false)
	return answer(Yielded{Yielded: true, Label: label})
}

func (s *Service) droppedDev(a devArgs) (*Dev, error) {
	if a.Device == "" {
		var found []*Dev
		for _, d := range onPlatform(s.dropped, a.Platform) {
			if !d.foreign(a.Owner) {
				found = append(found, d)
			}
		}
		if len(found) > 1 {
			var names []string
			for _, d := range found {
				names = append(names, d.label())
			}
			sort.Strings(names)
			return nil, fmt.Errorf("several sessions whose connection dropped are left (%s): say which with -d", strings.Join(names, ", "))
		}
		if len(found) == 1 {
			return found[0], nil
		}
		return nil, nil
	}
	serial := a.Device
	if sr, ok := s.names[a.Device]; ok {
		serial = sr
	}
	d := s.dropped[serial]
	if d == nil || (a.Platform != "" && a.Platform != string(d.Info.Platform)) {
		return nil, nil
	}
	if a.denies(d) {
		return nil, errForeign(d)
	}
	return d, nil
}

func (s *Service) closeDev(ctx context.Context, d *Dev) (string, error) {
	s.waitLock(d, "tb close", false, nil)
	defer d.mu.Unlock()
	s.mu.Lock()
	label := d.label()
	if ctx.Err() != nil {
		s.mu.Unlock()
		return label, errCloseCancelled
	}
	if s.devs[d.Info.Serial] == d {
		delete(s.devs, d.Info.Serial)
	}
	if s.dropped[d.Info.Serial] == d {
		delete(s.dropped, d.Info.Serial)
	}
	if d.Name != "" && s.names[d.Name] == d.Info.Serial {
		delete(s.names, d.Name)
		s.closed[d.Name] = d.Info.Serial
	}
	s.mu.Unlock()
	return label, d.shut(true)
}
