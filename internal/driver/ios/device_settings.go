package ios

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func (d *Driver) SetLocation(ctx context.Context, lat, lon float64) error {
	_, err := d.simctl(ctx, "location", d.info.Serial, "set",
		strconv.FormatFloat(lat, 'f', -1, 64)+","+strconv.FormatFloat(lon, 'f', -1, 64))
	return err
}

func (d *Driver) SetOrientation(ctx context.Context, orientation string) error {
	to := map[string]string{"PORTRAIT": "portrait", "LANDSCAPE_LEFT": "landscapeLeft", "LANDSCAPE_RIGHT": "landscapeRight", "UPSIDE_DOWN": "upsideDown"}[strings.ToUpper(orientation)]
	if to == "" {
		to = orientation
	}
	// A cancelled run turns nothing: call bounds the request by a deadline only (issue #115).
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := d.call(ctx, "rotate", map[string]any{"to": to}, 10*time.Second); err != nil {
		return err
	}
	return ctx.Err() // cancelled while the runner turned it: report that, not success
}

var errAirplane = errors.New("airplane mode is not supported on iOS simulators (they share the Mac's network)")

func (d *Driver) SetAirplaneMode(ctx context.Context, on bool) error { return errAirplane }

func (d *Driver) ToggleAirplaneMode(ctx context.Context) error { return errAirplane }

func (d *Driver) AddMedia(ctx context.Context, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	abs := make([]string, len(paths))
	for i, p := range paths {
		abs[i] = p
		if a, err := filepath.Abs(p); err == nil {
			abs[i] = a
		}
	}
	_, err := d.simctl(ctx, append([]string{"addmedia", d.info.Serial}, abs...)...)
	return err
}

func (d *Driver) InstallApp(ctx context.Context, path string) error {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	_, err := d.simctl(ctx, "install", d.info.Serial, path)
	return err
}

func (d *Driver) Foreground(ctx context.Context) (string, error) {
	m, err := d.call(ctx, "foreground", nil, 10*time.Second)
	if err != nil {
		return "", err
	}
	var r struct {
		Pkg string `json:"pkg"`
	}
	m.Decode(&r)
	return r.Pkg, nil
}

// SetDarkMode switches the simulator's appearance (`simctl ui <udid> appearance dark|light`).
func (d *Driver) SetDarkMode(ctx context.Context, on bool) error {
	v := "light"
	if on {
		v = "dark"
	}
	_, err := d.simctl(ctx, "ui", d.info.Serial, "appearance", v)
	return err
}

// DarkMode reads the simulator's appearance: "dark" or "light".
func (d *Driver) DarkMode(ctx context.Context) (bool, error) {
	out, err := d.simctl(ctx, "ui", d.info.Serial, "appearance")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(out)) == "dark", nil
}
