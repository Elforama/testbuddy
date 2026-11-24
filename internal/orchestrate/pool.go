package orchestrate

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/elforama/testbuddy/internal/ci"
	"github.com/elforama/testbuddy/internal/connect"
	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/devlock"
	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/driver/android"
	"github.com/elforama/testbuddy/internal/driver/ios"
	"github.com/elforama/testbuddy/internal/flow"
)

type Pool struct {
	Installed     func(serial, appID string) bool
	LockAs        string
	KeepAgents    bool
	WaitForDevice time.Duration
	Reclaim       func(ctx context.Context, serial string) Reclaimed
	Prefer        string

	mu       sync.Mutex
	devices  []device.Info
	busy     map[string]bool
	drivers  map[string]driver.Driver
	opening  map[string]chan struct{}
	locks    map[string]*devlock.Lock
	held     map[string]devlock.Owner
	lost     map[string]error
	asked    map[string]asked
	reclaims map[string]int

	onAllocOrWait func()
	onLost        func(serial string)
}

func NewPool(devs []device.Info) *Pool {
	return &Pool{devices: devs, busy: map[string]bool{}, drivers: map[string]driver.Driver{}, opening: map[string]chan struct{}{},
		locks: map[string]*devlock.Lock{}, held: map[string]devlock.Owner{}, lost: map[string]error{}, asked: map[string]asked{}, reclaims: map[string]int{}}
}

func (p *Pool) lock(d device.Info) bool {
	if d.Platform == device.Web || p.locks[d.Serial] != nil {
		return true
	}
	what := p.LockAs
	if what == "" {
		what = "tb test"
	}
	l, err := devlock.TryAcquire(d.Serial, what)
	if b, busy := devlock.IsBusy(err); busy {
		p.held[d.Serial] = b.Owner
		return false
	}
	delete(p.held, d.Serial)
	if err == nil {
		p.locks[d.Serial] = l
	}
	return true
}

func (p *Pool) allocOrWait(ctx context.Context, needs []Need) (map[string]device.Info, error) {
	if p.onAllocOrWait != nil {
		p.onAllocOrWait()
	}
	deadline := time.Now().Add(p.WaitForDevice)
	for {
		if devs := p.tryAlloc(needs); devs != nil {
			return devs, nil
		}
		rctx, cancel := ctx, context.CancelFunc(func() {})
		if p.WaitForDevice > 0 {
			until := deadline
			if soon := time.Now().Add(2 * time.Second); until.Before(soon) {
				until = soon
			}
			rctx, cancel = context.WithDeadline(ctx, until)
		}
		p.mu.Lock()
		pending := p.preferPending()
		p.mu.Unlock()
		got := p.reclaimOwn(rctx, needs)
		cancel()
		p.mu.Lock()
		settled := pending && !p.preferPending()
		p.mu.Unlock()
		if got || settled {
			continue
		}
		err := p.heldElsewhere(needs)
		if err == nil {
			return nil, nil
		}
		if !time.Now().Before(deadline) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(min(500*time.Millisecond, time.Until(deadline))):
		}
	}
}

func (p *Pool) heldElsewhere(needs []Need) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	claimed := map[string]bool{}
	for _, i := range needOrder(needs) {
		n := needs[i]
		var holders []string
		free := ""
		for _, d := range p.devices {
			if !p.fits(n, d) || claimed[d.Serial] {
				continue
			}
			if o, ok := p.held[d.Serial]; ok && p.locks[d.Serial] == nil {
				holders = append(holders, p.heldBy(d, o))
				continue
			}
			free = d.Serial
			break
		}
		if free != "" {
			claimed[free] = true
			continue
		}
		if n.Platform == string(device.Web) && n.Device == "" {
			continue
		}
		if len(holders) > 0 {
			msg := strings.Join(holders, "; ")
			return fmt.Errorf("%s. One tb at a time per device: wait for it, use another device, or run with --wait-for-device", msg)
		}
	}
	return nil
}

func (p *Pool) heldBy(d device.Info, o devlock.Owner) string {
	if !isSession(o) {
		return fmt.Sprintf("%s (%s) is in use by %s", d.Name, d.Serial, o)
	}
	a, ok := p.asked[d.Serial]
	switch {
	case ok && a.owner == o.PID && a.Busy:
		return fmt.Sprintf("%s (%s) is held by your own tb session %s, which is running a command (tb do, tb watch): wait for it to end, or stop it and run tb close -d %s",
			d.Name, d.Serial, a.Label, d.Serial)
	case ok && a.owner == o.PID && !a.OK && !a.Unknown:
		return fmt.Sprintf("%s (%s) is in use by %s: a session of another tb daemon (another agent's, or one with another TB_HOME), not yours to close",
			d.Name, d.Serial, o)
	}
	return fmt.Sprintf("%s (%s) is in use by %s (if that session is yours, close it: tb close -d %s)", d.Name, d.Serial, o, d.Serial)
}

func (p *Pool) PassedOver(n Need, chosen string) (passed []string, others int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, d := range p.devices {
		if d.Serial == chosen || d.Platform == device.Web || !n.fits(d) {
			continue
		}
		if !p.fits(n, d) {
			passed = append(passed, d.Serial+" (no "+n.AppID+" installed)")
			continue
		}
		others++
		why := "not tried: an earlier one fit"
		switch o, held := p.held[d.Serial]; {
		case held && p.locks[d.Serial] == nil:
			why = "in use by " + o.String()
			if a, ok := p.asked[d.Serial]; ok && a.owner == o.PID && a.Busy {
				why = "held by your own tb session " + a.Label + ", busy"
			}
		case p.lost[d.Serial] != nil:
			why = "its connection was lost"
		case p.busy[d.Serial]:
			why = "running another flow of this run"
		}
		passed = append(passed, d.Serial+" ("+why+")")
	}
	return passed, others
}

func isSession(o devlock.Owner) bool { return strings.HasPrefix(o.What, "tb session") }

type Reclaimed struct {
	OK      bool
	Busy    bool
	Unknown bool
	Label   string
}

type asked struct {
	Reclaimed
	owner int
}

const maxReclaims = 3

func (p *Pool) reclaimOwn(ctx context.Context, needs []Need) bool {
	if p.Reclaim == nil {
		return false
	}
	p.mu.Lock()
	claimed := map[string]bool{}
	var groups [][]string
	for _, i := range needOrder(needs) {
		n := needs[i]
		var held []string
		free := ""
		if s := p.Prefer; s != "" && !claimed[s] && p.lost[s] == nil && p.locks[s] == nil && p.preferPending() {
			if d, ok := p.deviceBySerial(s); ok && p.fits(n, d) {
				claimed[s] = true
				groups = append(groups, []string{s})
				continue
			}
		}
		for _, d := range p.devices {
			if !p.fits(n, d) || claimed[d.Serial] || p.lost[d.Serial] != nil {
				continue
			}
			if _, tried := p.held[d.Serial]; !tried {
				p.lock(d)
			}
			o, ok := p.held[d.Serial]
			if !ok || p.locks[d.Serial] != nil {
				free = d.Serial
				break
			}
			a, done := p.asked[d.Serial]
			mine := done && a.owner == o.PID
			if isSession(o) && !(mine && !a.Busy && !a.Unknown && !a.OK) && p.reclaims[d.Serial] < maxReclaims {
				held = append(held, d.Serial)
			}
		}
		if free != "" {
			claimed[free] = true
			continue
		}
		if n.Platform == string(device.Web) && n.Device == "" {
			continue
		}
		if len(held) == 0 {
			p.mu.Unlock()
			return false
		}
		groups = append(groups, held)
	}
	owners := map[string]int{}
	for _, g := range groups {
		for _, s := range g {
			owners[s] = p.held[s].PID
		}
	}
	p.mu.Unlock()
	got := false
	taken := map[string]bool{}
	for _, g := range groups {
		for _, s := range g {
			if taken[s] {
				continue
			}
			r := p.Reclaim(ctx, s)
			p.mu.Lock()
			p.asked[s] = asked{Reclaimed: r, owner: owners[s]}
			if r.OK {
				delete(p.held, s)
				p.reclaims[s]++
			}
			p.mu.Unlock()
			if r.OK {
				got, taken[s] = true, true
				break
			}
		}
	}
	return got
}

func (p *Pool) preferPending() bool {
	o, held := p.held[p.Prefer]
	if !held || !isSession(o) || p.reclaims[p.Prefer] >= maxReclaims || p.Reclaim == nil {
		return false
	}
	a, done := p.asked[p.Prefer]
	return !done || a.owner != o.PID || a.OK
}

func (p *Pool) deviceBySerial(serial string) (device.Info, bool) {
	for _, d := range p.devices {
		if d.Serial == serial {
			return d, true
		}
	}
	return device.Info{}, false
}

func needOrder(needs []Need) []int {
	order := make([]int, 0, len(needs))
	for i, n := range needs {
		if n.Device != "" {
			order = append(order, i)
		}
	}
	for i, n := range needs {
		if n.Device == "" && n.Platform != "" {
			order = append(order, i)
		}
	}
	for i, n := range needs {
		if n.Device == "" && n.Platform == "" {
			order = append(order, i)
		}
	}
	return order
}

func (p *Pool) Devices() []device.Info { return p.devices }

type Need struct {
	Role     string
	Platform string
	Device   string
	AppID    string
	Mobile   bool
}

func (n Need) fits(d device.Info) bool {
	if n.Platform != "" && string(d.Platform) != n.Platform {
		return false
	}
	if n.Mobile && d.Platform == device.Web {
		return false
	}
	if n.Device != "" && d.Serial != n.Device && !strings.EqualFold(d.Name, n.Device) {
		return false
	}
	return true
}

func (p *Pool) fits(n Need, d device.Info) bool {
	if !n.fits(d) {
		return false
	}
	if n.AppID != "" && p.Installed != nil && !p.Installed(d.Serial, n.AppID) {
		return false
	}
	return true
}

func (p *Pool) tryAlloc(needs []Need) map[string]device.Info {
	p.mu.Lock()
	defer p.mu.Unlock()
	got := map[string]device.Info{}
	taken := map[string]bool{}
	for _, i := range needOrder(needs) {
		n := needs[i]
		found := false
		for _, d := range p.devices {
			if p.busy[d.Serial] || taken[d.Serial] || p.lost[d.Serial] != nil || !p.fits(n, d) {
				continue
			}
			if !p.lock(d) {
				if d.Serial == p.Prefer && p.preferPending() {
					break
				}
				continue
			}
			got[n.Role] = d
			taken[d.Serial] = true
			found = true
			break
		}
		if !found && (n.Platform == string(device.Web)) && n.Device == "" {
			if d, ok := p.addWeb(); ok {
				got[n.Role] = d
				taken[d.Serial] = true
				found = true
			}
		}
		if !found {
			return nil
		}
	}
	for s := range taken {
		p.busy[s] = true
	}
	return got
}

const maxBrowsers = 8

func (p *Pool) addWeb() (device.Info, bool) {
	webs, live := 0, 0
	for _, d := range p.devices {
		if d.Platform == device.Web {
			webs++
			if p.lost[d.Serial] == nil {
				live++
			}
		}
	}
	if webs == 0 || live >= maxBrowsers {
		return device.Info{}, false
	}
	d := device.WebDevice(webs + 1)
	p.devices = append(p.devices, d)
	return d, true
}

func (p *Pool) satisfiable(needs []Need) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	used := map[string]bool{}
	hasWeb := false
	for _, d := range p.devices {
		hasWeb = hasWeb || d.Platform == device.Web
	}
	for _, n := range needs {
		ok := false
		var lost []string
		for _, d := range p.devices {
			if err := p.lost[d.Serial]; err != nil {
				if p.fits(n, d) {
					lost = append(lost, fmt.Sprintf("%s (%s): %v", d.Name, d.Serial, err))
				}
				continue
			}
			if !used[d.Serial] && p.fits(n, d) {
				used[d.Serial] = true
				ok = true
				break
			}
		}
		if !ok && n.Platform == string(device.Web) && hasWeb && n.Device == "" {
			ok = true
		}
		if !ok && len(lost) > 0 {
			who := "the device that fits it was lost earlier in this run: "
			if len(lost) > 1 {
				who = "the devices that fit it were lost earlier in this run: "
			}
			if n.Role != "" {
				return fmt.Errorf("role %q cannot run: %s%s", n.Role, who, strings.Join(lost, "; "))
			}
			return fmt.Errorf("cannot run: %s%s", who, strings.Join(lost, "; "))
		}
		if !ok {
			what := "a device"
			if n.Mobile {
				what = "an Android device or iOS simulator"
			}
			if n.Platform != "" {
				what = "an " + n.Platform + " device"
				switch n.Platform {
				case "ios":
					what = "an iOS simulator"
				case "web":
					what = "a browser (install Google Chrome or set TB_CHROME)"
				}
			}
			if n.Device != "" {
				what = fmt.Sprintf("device %q", n.Device)
			}
			if n.AppID != "" {
				what += " with " + n.AppID + " installed"
			}
			if n.Role != "" {
				return fmt.Errorf("role %q needs %s, and none is free among the connected devices", n.Role, what)
			}
			return fmt.Errorf("needs %s, and none is connected", what)
		}
	}
	return nil
}

func (p *Pool) isLost(serial string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lost[serial] != nil
}

func (p *Pool) release(devs map[string]device.Info) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, d := range devs {
		delete(p.busy, d.Serial)
	}
}

func (p *Pool) Driver(ctx context.Context, d device.Info) (driver.Driver, error) {
	p.mu.Lock()
	if drv := p.drivers[d.Serial]; drv != nil {
		p.mu.Unlock()
		return drv, nil
	}
	if ch, ok := p.opening[d.Serial]; ok {
		p.mu.Unlock()
		<-ch
		return p.Driver(ctx, d)
	}
	ch := make(chan struct{})
	p.opening[d.Serial] = ch
	p.mu.Unlock()

	drv, err := connect.Open(ctx, d)

	p.mu.Lock()
	delete(p.opening, d.Serial)
	if err == nil {
		p.drivers[d.Serial] = drv
	}
	p.mu.Unlock()
	close(ch)
	return drv, err
}

const reopenBase = 3 * time.Minute

func reopenWithin(d device.Info) time.Duration {
	switch d.Platform {
	case device.IOS:
		return max(reopenBase, ios.RunnerStartTimeout+time.Minute)
	case device.Android:
		if ci.On() && d.Emulator {
			return reopenBase + android.RestartBoot
		}
	}
	return reopenBase
}

var failureReopenWithin = 60 * time.Second

func (p *Pool) driverForFailure(ctx context.Context, d device.Info) (drv driver.Driver, note string, err error) {
	p.mu.Lock()
	dead := p.drivers[d.Serial]
	if dead == nil || p.lost[d.Serial] != nil {
		p.mu.Unlock()
		return nil, "", nil
	}
	var why error
	if lc, ok := dead.(driver.LostChecker); ok {
		why = lc.Lost()
	}
	p.mu.Unlock()
	if why == nil {
		return dead, "", nil
	}
	octx, cancel := context.WithTimeout(android.WithoutRestart(ctx), failureReopenWithin)
	defer cancel()
	drv, err = connect.Open(octx, d)
	if err != nil {
		return nil, "", fmt.Errorf("the connection to %s (%s) was lost (%v), and opening it again within %s failed: %v",
			d.Name, d.Serial, why, failureReopenWithin, err)
	}
	p.mu.Lock()
	p.drivers[d.Serial] = drv
	p.mu.Unlock()
	dead.Close()
	return drv, fmt.Sprintf("the connection to %s (%s) was lost (%v): tb closed it and opened it again", d.Name, d.Serial, why), nil
}

func (p *Pool) driverForJob(ctx context.Context, d device.Info) (drv driver.Driver, note string, err error) {
	p.mu.Lock()
	if err := p.lost[d.Serial]; err != nil {
		p.mu.Unlock()
		return nil, "", err
	}
	dead := p.drivers[d.Serial]
	var why error
	if lc, ok := dead.(driver.LostChecker); ok {
		why = lc.Lost()
	}
	if why == nil {
		p.mu.Unlock()
		drv, err := p.Driver(ctx, d)
		return drv, "", err
	}
	delete(p.drivers, d.Serial)
	p.mu.Unlock()
	dead.Close()

	octx, cancel := context.WithTimeout(ctx, reopenWithin(d))
	defer cancel()
	drv, err = p.Driver(octx, d)
	if err != nil {
		if ctx.Err() != nil {
			return nil, "", err
		}
		err = fmt.Errorf("the connection to the device was lost (%v), and opening it again failed: %v", why, err)
		p.mu.Lock()
		p.lost[d.Serial] = err
		p.mu.Unlock()
		if p.onLost != nil {
			p.onLost(d.Serial)
		}
		return nil, "", err
	}
	return drv, fmt.Sprintf("the connection to %s (%s) was lost (%v): tb closed it and opened it again", d.Name, d.Serial, why), nil
}

func (p *Pool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, d := range p.drivers {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if r, ok := d.(driver.Releaser); ok && !p.KeepAgents {
			r.Release(ctx)
			cancel()
			continue
		}
		if k, ok := d.(driver.Keeper); ok && p.KeepAgents {
			k.Keep(ctx)
		}
		cancel()
		d.Close()
	}
	for s, l := range p.locks {
		l.Release()
		delete(p.locks, s)
	}
}

func NeedsFor(f *flow.Flow, platform string, env []flow.KV) []Need {
	scope := vars{}.define(env).define(f.Config.Env)
	need := func(role, p, dev, raw string, web bool, v vars) Need {
		n := Need{Role: role, Platform: p, Device: dev}
		id := v.resolve(raw)
		if strings.Contains(id, "${") {
			if web && n.Platform == "" {
				n.Platform = string(device.Web)
			}
			return n
		}
		web = web || device.IsURL(id)
		switch {
		case web:
			if n.Platform == "" {
				n.Platform = string(device.Web)
			}
		case id != "":
			n.AppID = id
			n.Mobile = n.Platform == ""
		}
		return n
	}
	if len(f.Config.Devices) == 0 {
		return []Need{need("", platform, "", f.Config.AppID, f.Config.Web(), scope)}
	}
	var out []Need
	for _, r := range f.Config.Devices {
		id, web := r.AppID, false
		if id == "" {
			id, web = f.Config.AppID, f.Config.Web()
		}
		out = append(out, need(r.Name, r.Platform, r.Device, id, web, scope.define(r.Env)))
	}
	return out
}

var AppInstalled = device.AppInstalled

func (p *Pool) CheckApps(ctx context.Context, jobs []*Job) {
	apps := map[string]bool{}
	for _, j := range jobs {
		for _, n := range j.Needs {
			if n.AppID != "" {
				apps[n.AppID] = true
			}
		}
	}
	if len(apps) == 0 || len(p.devices) < 2 {
		return
	}
	found := map[string]bool{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, d := range p.devices {
		for app := range apps {
			wg.Add(1)
			go func(d device.Info, app string) {
				defer wg.Done()
				ok := AppInstalled(ctx, d, app)
				mu.Lock()
				found[d.Serial+"\x00"+app] = ok
				mu.Unlock()
			}(d, app)
		}
	}
	wg.Wait()
	anywhere := map[string]bool{}
	for k, ok := range found {
		if ok {
			anywhere[k[strings.IndexByte(k, 0)+1:]] = true
		}
	}
	p.Installed = func(serial, app string) bool {
		ok, known := found[serial+"\x00"+app]
		return ok || !known || !anywhere[app]
	}
}
