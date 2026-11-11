package engine

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/elforama/testbuddy/internal/flow"
)

func (r *Runner) latLon(a, b string) (float64, float64, error) {
	sa, err := r.eval(a)
	if err != nil {
		return 0, 0, err
	}
	sb, err := r.eval(b)
	if err != nil {
		return 0, 0, err
	}
	lat, e1 := strconv.ParseFloat(strings.TrimSpace(sa), 64)
	lon, e2 := strconv.ParseFloat(strings.TrimSpace(sb), 64)
	if e1 != nil || e2 != nil {
		return 0, 0, failf("invalid location %q, %q", sa, sb)
	}
	return lat, lon, nil
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
