package web

import (
	"context"
	"errors"
)

func (d *Driver) SetLocation(ctx context.Context, lat, lon float64) error {
	return d.page.call(ctx, "Emulation.setGeolocationOverride", map[string]any{"latitude": lat, "longitude": lon, "accuracy": 10}, nil)
}

func (d *Driver) SetOrientation(ctx context.Context, orientation string) error { return nil }

func (d *Driver) SetAirplaneMode(ctx context.Context, on bool) error {
	d.mu.Lock()
	d.offline = on
	d.mu.Unlock()
	return d.page.call(ctx, "Network.emulateNetworkConditions", map[string]any{
		"offline": on, "latency": 0, "downloadThroughput": -1, "uploadThroughput": -1}, nil)
}

func (d *Driver) ToggleAirplaneMode(ctx context.Context) error {
	d.mu.Lock()
	on := !d.offline
	d.mu.Unlock()
	return d.SetAirplaneMode(ctx, on)
}

func (d *Driver) SetDarkMode(ctx context.Context, on bool) error {
	v := "light"
	if on {
		v = "dark"
	}
	return d.page.call(ctx, "Emulation.setEmulatedMedia", map[string]any{
		"features": []map[string]string{{"name": "prefers-color-scheme", "value": v}}}, nil)
}

func (d *Driver) DarkMode(ctx context.Context) (bool, error) {
	var dark bool
	err := d.eval(ctx, "matchMedia('(prefers-color-scheme: dark)').matches", &dark)
	return dark, err
}

func (d *Driver) AddMedia(ctx context.Context, paths []string) error {
	return errors.New("addMedia is not supported on web")
}

func (d *Driver) InstallApp(ctx context.Context, path string) error {
	return errors.New("installing apps is not supported on web")
}

func (d *Driver) StartRecording(ctx context.Context, path string) error {
	return errors.New("screen recording is not supported on web yet")
}

func (d *Driver) StopRecording(ctx context.Context) error { return nil }
