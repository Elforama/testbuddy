package engine

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/elforama/testbuddy/internal/flow"
)

// latLon evaluates a setLocation's latitude and longitude and checks them with the flow's
// rule (flow.Latitude, flow.Longitude), before anything is sent to the device (issue #113).
func (r *Runner) latLon(a, b string) (float64, float64, error) {
	lat, err := r.coordinate(a, "latitude", 90, flow.Latitude)
	if err != nil {
		return 0, 0, err
	}
	lon, err := r.coordinate(b, "longitude", 180, flow.Longitude)
	if err != nil {
		return 0, 0, err
	}
	return lat, lon, nil
}

func (r *Runner) coordinate(raw, name string, limit int, read func(string) (float64, error)) (float64, error) {
	s, err := r.eval(raw)
	if err != nil {
		return 0, err
	}
	v, err := read(s)
	if err != nil {
		if s != raw {
			return 0, failf("invalid %s %q (from %q): expected a number from -%d to %d", name, s, raw, limit, limit)
		}
		return 0, failf("%v", err)
	}
	return v, nil
}

func (r *Runner) travel(ctx context.Context, c *flow.Command) error {
	speed := flow.DefaultTravelSpeed
	if c.TravelSpeed != "" {
		s, err := r.eval(c.TravelSpeed)
		if err != nil {
			return err
		}
		if speed, err = flow.TravelSpeed(s); err != nil {
			if s != c.TravelSpeed {
				return fmt.Errorf("invalid speed %q (from %q): expected metres per second, a number above 0", s, c.TravelSpeed)
			}
			return err
		}
	}
	if len(c.Points) == 0 {
		return nil
	}
	if err := r.Drv.SetLocation(ctx, c.Points[0][0], c.Points[0][1]); err != nil {
		return err
	}
	for i := 1; i < len(c.Points); i++ {
		a, b := c.Points[i-1], c.Points[i]
		dist := haversine(a[0], a[1], b[0], b[1])
		step := time.Duration(dist / speed / 50 * float64(time.Second))
		for k := 1; k <= 50; k++ {
			f := float64(k) / 50
			if err := r.Drv.SetLocation(ctx, a[0]+(b[0]-a[0])*f, a[1]+(b[1]-a[1])*f); err != nil {
				return err
			}
			select { // simulated travel time is the command's purpose, not a wait on the app
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(step):
			}
		}
	}
	return nil
}

func haversine(lat1, lon1, lat2, lon2 float64) float64 {
	const R = 6371000.0
	p1, p2 := lat1*math.Pi/180, lat2*math.Pi/180
	dp, dl := (lat2-lat1)*math.Pi/180, (lon2-lon1)*math.Pi/180
	h := math.Sin(dp/2)*math.Sin(dp/2) + math.Cos(p1)*math.Cos(p2)*math.Sin(dl/2)*math.Sin(dl/2)
	return 2 * R * math.Asin(math.Sqrt(h))
}
