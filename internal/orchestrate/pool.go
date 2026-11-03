package orchestrate

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/elforama/testbuddy/internal/connect"
	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/flow"
)

// Pool hands out devices to jobs. Drivers are opened on first use and kept for the run.
type Pool struct {
	// Installed reports whether an app is on a device; nil means any device will do.
	Installed func(serial, appID string) bool

	mu      sync.Mutex
	devices []device.Info
	busy    map[string]bool
	drivers map[string]driver.Driver
	opening map[string]chan struct{}
}

func NewPool(devs []device.Info) *Pool {
	return &Pool{devices: devs, busy: map[string]bool{}, drivers: map[string]driver.Driver{}, opening: map[string]chan struct{}{}}
}

func (p *Pool) Devices() []device.Info { return p.devices }

// Need is one device a job needs.
type Need struct {
	Role     string // "" for single-device flows
	Platform string
	Device   string // serial or name; "" = any
	AppID    string // the device must have this app installed ("" = no check)
}

func (n Need) fits(d device.Info) bool {
	if n.Platform != "" && string(d.Platform) != n.Platform {
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
			if p.busy[d.Serial] || taken[d.Serial] || !p.fits(n, d) {
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

// addWeb adds another browser device (at most 8). Caller holds mu.
func (p *Pool) addWeb() (device.Info, bool) {
	webs := 0
	for _, d := range p.devices {
		if d.Platform == device.Web {
			webs++
		}
	}
	if webs == 0 || webs >= 8 {
		return device.Info{}, false
	}
	d := device.WebDevice(webs + 1)
	p.devices = append(p.devices, d)
	return d, true
}

// satisfiable reports whether the pool could ever serve the needs (ignoring busy devices).
func (p *Pool) satisfiable(needs []Need) error {
	used := map[string]bool{}
	hasWeb := false
	for _, d := range p.devices {
		hasWeb = hasWeb || d.Platform == device.Web
	}
	for _, n := range needs {
		ok := false
		for _, d := range p.devices {
			if !used[d.Serial] && p.fits(n, d) {
				used[d.Serial] = true
				ok = true
				break
			}
		}
		if !ok && n.Platform == string(device.Web) && hasWeb && n.Device == "" {
			ok = true // another browser can be started
		}
		if !ok {
			what := "a device"
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

// Close closes every opened driver.
func (p *Pool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, d := range p.drivers {
		d.Close()
	}
}

// NeedsFor lists what a flow needs from the pool.
func NeedsFor(f *flow.Flow, platform string) []Need {
	app := func(id string) string {
		if strings.Contains(id, "${") || device.IsURL(id) {
			return "" // decided at run time, or a web url
		}
		return id
	}
	plat := func(id, p string) string {
		if p == "" && device.IsURL(id) {
			return string(device.Web)
		}
		return p
	}
	if len(f.Config.Devices) == 0 {
		return []Need{{Platform: plat(f.Config.AppID, platform), AppID: app(f.Config.AppID)}}
	}
	var out []Need
	for _, r := range f.Config.Devices {
		id := r.AppID
		if id == "" {
			id = f.Config.AppID
		}
		out = append(out, Need{Role: r.Name, Platform: plat(id, r.Platform), Device: r.Device, AppID: app(id)})
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
