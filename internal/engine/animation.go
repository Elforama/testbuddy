package engine

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
)

// Accessibility events cannot see animations that only repaint pixels. Sample small
// screenshots for this explicit mobile command after the usual event-driven settle.
func waitForAnimation(ctx context.Context, d driver.Driver, timeout time.Duration) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	_ = d.WaitSettled(ctx, timeout)
	if d.Platform() != "android" && d.Platform() != "ios" {
		return
	}

	const stableFor = 250 * time.Millisecond
	var reference image.Image
	var stableSince time.Time
	ticker := time.NewTicker(80 * time.Millisecond)
	defer ticker.Stop()
	for ctx.Err() == nil {
		data, err := d.Screenshot(ctx, 0.15, "png")
		if err != nil {
			return
		}
		frame, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			return
		}
		now := time.Now()
		if reference == nil || frameChanged(reference, frame) {
			reference, stableSince = frame, now
		} else if now.Sub(stableSince) >= stableFor {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Ignore small pixel differences and incidental changes such as the clock or caret.
func frameChanged(before, after image.Image) bool {
	bounds := before.Bounds()
	if bounds != after.Bounds() {
		return true
	}
	minimum := max(1, bounds.Dx()*bounds.Dy()/100)
	changed := 0
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r1, g1, b1, _ := before.At(x, y).RGBA()
			r2, g2, b2, _ := after.At(x, y).RGBA()
			if channelDelta(r1, r2)+channelDelta(g1, g2)+channelDelta(b1, b2) > 12*257 {
				changed++
				if changed >= minimum {
					return true
				}
			}
		}
	}
	return false
}

func channelDelta(a, b uint32) uint32 {
	return max(a, b) - min(a, b)
}
