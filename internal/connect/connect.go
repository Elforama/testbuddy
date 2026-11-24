package connect

import (
	"context"
	"fmt"

	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/driver/android"
	"github.com/elforama/testbuddy/internal/driver/ios"
	"github.com/elforama/testbuddy/internal/driver/web"
)

func init() {
	device.WebAvailable = func() bool { return web.ChromePath() != "" }
}

var openers = map[device.Platform]func(context.Context, device.Info) (driver.Driver, error){
	device.Android: func(ctx context.Context, i device.Info) (driver.Driver, error) { return android.Open(ctx, i) },
	device.IOS:     func(ctx context.Context, i device.Info) (driver.Driver, error) { return ios.Open(ctx, i) },
	device.Web:     func(ctx context.Context, i device.Info) (driver.Driver, error) { return web.Open(ctx, i) },
}

func Open(ctx context.Context, info device.Info) (driver.Driver, error) {
	open, ok := openers[info.Platform]
	if !ok {
		return nil, fmt.Errorf("%s devices are not supported yet", info.Platform)
	}
	return open(ctx, info)
}

func Override(p device.Platform, open func(context.Context, device.Info) (driver.Driver, error)) (restore func()) {
	old, had := openers[p]
	openers[p] = open
	return func() {
		if had {
			openers[p] = old
		} else {
			delete(openers, p)
		}
	}
}
