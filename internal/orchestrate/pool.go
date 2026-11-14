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

// Pool hands out devices to jobs. Drivers are opened on first use and kept for the run; one
// whose connection is gone for good is closed and the device opened again for the next job
// (driverForJob), and a device that cannot be opened again is left out for the rest of the run.
//
// A phone or simulator is used by one tb process at a time (devlock): the pool takes a
// device's lock the first time it hands the device out and keeps it for the run. A device
// another process holds (another `tb test`, an agent's `tb open` session) is passed over for
// a free one; a job only such devices could run fails at once, naming who holds them, or,
// with WaitForDevice, waits for them.
type Pool struct {
	// Installed reports whether an app is on a device; nil means any device will do.
	Installed func(serial, appID string) bool
	// LockAs is what other processes are told holds a device this run locked
	// (e.g. "tb test flows/"). "" = "tb test".
	LockAs string
	// KeepAgents leaves each device's tb agent running when the pool closes (faster next run);
	// otherwise Close stops them, freeing the devices for other tools.
	KeepAgents bool
	// WaitForDevice is how long a job waits for devices other processes hold (0: fail at once).
	WaitForDevice time.Duration

	mu      sync.Mutex
	devices []device.Info
	busy    map[string]bool
	drivers map[string]driver.Driver
	opening map[string]chan struct{}
	locks   map[string]*devlock.Lock // devices this run holds
	held    map[string]devlock.Owner // devices another process held when last tried
	lost    map[string]error         // devices whose connection was lost and that could not be opened again

	onAllocOrWait func()              // tests: runs as allocOrWait starts (nil = nothing)
	onLost        func(serial string) // tests: runs as driverForJob marks a device lost (nil = nothing)
}

func NewPool(devs []device.Info) *Pool {
	return &Pool{devices: devs, busy: map[string]bool{}, drivers: map[string]driver.Driver{}, opening: map[string]chan struct{}{},
		locks: map[string]*devlock.Lock{}, held: map[string]devlock.Owner{}, lost: map[string]error{}}
}

// lock takes d's device lock for the run, once (browsers need none: each run starts its own).
// false: another process holds it. A lock that cannot be taken for another reason (the lock
// folder is not writable) does not stop the run. Caller holds mu.
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

// allocOrWait reserves devices for needs; when only devices other processes hold could serve
// them, it waits for one to come free (checking every half second, up to WaitForDevice), then
// returns the error naming who holds them. nil, nil: the needs do not fit for another reason.
func (p *Pool) allocOrWait(ctx context.Context, needs []Need) (map[string]device.Info, error) {
	if p.onAllocOrWait != nil {
		p.onAllocOrWait()
	}
	deadline := time.Now().Add(p.WaitForDevice)
	for {
		if devs := p.tryAlloc(needs); devs != nil {
			return devs, nil
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

// heldElsewhere is the error for needs that only devices other processes hold could serve
// (nil when a device that fits is free of other processes, or none fits at all).
func (p *Pool) heldElsewhere(needs []Need) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, n := range needs {
		var holders []string
		free := false
		for _, d := range p.devices {
			if !p.fits(n, d) {
				continue
			}
			if o, ok := p.held[d.Serial]; ok && p.locks[d.Serial] == nil {
				h := fmt.Sprintf("%s (%s) is in use by %s", d.Name, d.Serial, o)
				if strings.HasPrefix(o.What, "tb session") {
					h += " (if that session is yours, close it: tb close -d " + d.Serial + ")"
				}
				holders = append(holders, h)
				continue
			}
			free = true
		}
		if !free && len(holders) > 0 {
			msg := strings.Join(holders, "; ")
			return fmt.Errorf("%s. One tb at a time per device: wait for it, use another device, or run with --wait-for-device", msg)
		}
	}
	return nil
}

func (p *Pool) Devices() []device.Info { return p.devices }

// Need is one device a job needs.
type Need struct {
	Role     string // "" for single-device flows
	Platform string
	Device   string // serial or name; "" = any
	AppID    string // the device must have this app installed ("" = no check)
	Mobile   bool   // a phone app (a known, non-URL app id): any device but a browser
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

// tryAlloc reserves free devices for every need, or nothing. Needs naming a specific
// device are served first.
func (p *Pool) tryAlloc(needs []Need) map[string]device.Info {
	p.mu.Lock()
	defer p.mu.Unlock()
	got := map[string]device.Info{}
	taken := map[string]bool{}
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
	for _, i := range order {
		n := needs[i]
		found := false
		for _, d := range p.devices {
			if p.busy[d.Serial] || taken[d.Serial] || p.lost[d.Serial] != nil || !p.fits(n, d) || !p.lock(d) {
				continue
			}
			got[n.Role] = d
			taken[d.Serial] = true
			found = true
			break
		}
		if !found && (n.Platform == string(device.Web)) && n.Device == "" {
			// browsers are made on demand
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

// maxBrowsers caps the browsers a run has open at once.
const maxBrowsers = 8

// addWeb adds another browser device (at most maxBrowsers live ones: a browser lost earlier
// in the run serves nothing and does not count, issue #101). Caller holds mu.
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
	// after the last one made: a lost browser's serial is not used again
	d := device.WebDevice(webs + 1)
	p.devices = append(p.devices, d)
	return d, true
}

// satisfiable reports whether the pool could ever serve the needs (ignoring busy devices).
// A device lost earlier in the run serves nothing: a need only such devices fit fails,
// naming them and why.
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
			ok = true // another browser can be started
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

// isLost reports whether the device is lost for the run (driverForJob could not open it again).
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

// Driver opens (once) and returns the device's driver.
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

// reopenBase bounds opening a device again after its connection was lost.
const reopenBase = 3 * time.Minute

// reopenWithin bounds opening d again after its connection was lost: reopenBase, or as long
// as opening d may take on its own when that is longer (issue #101). A simulator's runner
// started cold by xcodebuild gets ios.RunnerStartTimeout; in CI mode, opening an Android
// emulator may restart it first (android.RestartBoot, issue #217; a phone is never restarted).
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

// failureReopenWithin bounds opening a lost device again only to read a failed flow's screen
// (driverForFailure, issue #101): a device that needs longer is left to the next flow's
// reopen, with its full bound (reopenWithin).
var failureReopenWithin = 60 * time.Second

// driverForFailure is the driver to read a failed attempt's screen from on d, which the
// attempt still holds: the driver it had, or, when that one's connection is gone for good, the
// device opened again within failureReopenWithin (note says so). A device the attempt never
// had a driver for (its open failed, the Started hook crashed first), or one lost for the run,
// is not opened here: nil, "", nil. A reopen that fails leaves the dead driver in place and the
// device not lost: the next job's driverForJob opens it again with its full bound and decides.
// An Android emulator is not restarted for this (android.WithoutRestart).
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
	// the attempt holds d: nothing else opens it meanwhile
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

// driverForJob is Driver for a job about to run on d (which the job holds). A driver whose
// connection is gone for good (driver.LostChecker) is not handed out again: it is closed and
// the device opened again, within reopenWithin(d), and note says so for the run's output and the
// flow's trace ("" when nothing happened). A device that cannot be opened again is lost for
// the run: the error says why, and the flows left go to other devices (tryAlloc, satisfiable).
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
			return nil, "", err // the run is ending: the device is not to blame
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

// Close closes every opened driver and frees the devices this run held.
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
			k.Keep(ctx) // else it exits a minute after this run lets go of it
		}
		cancel()
		d.Close()
	}
	for s, l := range p.locks {
		l.Release()
		delete(p.locks, s)
	}
}

// NeedsFor lists what a flow needs from the pool. env is what the run passes the flow
// (-e values, then MAESTRO_ shell variables): with the flow's own env (and a role's) it
// resolves ${NAME} in appId/url first, so `url: ${URL}` is placed on a browser and
// `appId: ${APP_ID}` on a phone that has the app. A value that still holds ${...} after
// that is decided at run time and puts no limit on the device.
func NeedsFor(f *flow.Flow, platform string, env []flow.KV) []Need {
	scope := vars{}.define(env).define(f.Config.Env)
	need := func(role, p, dev, raw string, web bool, v vars) Need {
		n := Need{Role: role, Platform: p, Device: dev}
		id := v.resolve(raw)
		if strings.Contains(id, "${") {
			if web && n.Platform == "" {
				n.Platform = string(device.Web)
			}
			return n // decided at run time
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

// CheckApps finds which of the jobs' apps are installed on which devices (in parallel,
// once per pair) and sets the pool's Installed lookup.
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
		return // one device: let the flow say what is wrong
	}
	// If no device has an app, keep every device eligible so the flow fails with the
	// platform's own message instead of "no device".
	found := map[string]bool{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, d := range p.devices {
		for app := range apps {
			wg.Add(1)
			go func(d device.Info, app string) {
				defer wg.Done()
				ok := device.AppInstalled(ctx, d, app)
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
