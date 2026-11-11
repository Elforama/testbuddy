// Package driver defines how the engine talks to one device. Each platform implements
// Driver over a persistent, push-based connection to its on-device agent.
package driver

import (
	"context"
	"errors"
	"time"

	"github.com/elforama/testbuddy/internal/ui"
)

// SettleResult says how the UI behaved after an action.
type SettleResult string

const (
	Changed  SettleResult = "changed"   // it changed, then went quiet
	NoChange SettleResult = "no_change" // nothing changed within the no-change window
	Restless SettleResult = "timeout"   // still changing when the settle timeout ran out
)

// ActionOpts controls the wait after an input action.
type ActionOpts struct {
	// Target identifies the uniquely selected app element for iOS delivery checks.
	Target     *ActionTarget
	NoSettle   bool          // return as soon as the input is injected
	React      bool          // return once the UI starts reacting, without waiting for it to finish
	Hold       time.Duration // taps on iOS: how long the touch is held (0: the agent's default)
	Drag       bool          // swipes: a drag that does not fling (the content stops at the lift)
	NoChangeMs int           // how long to wait for the UI to start changing (default 250)
	Timeout    time.Duration // longest settle wait (default 5 s)
}

type ActionTarget struct {
	Seq    int64 // generation of the tree supplying the point
	ID     string
	Window int // app process on iOS
	Bounds ui.Rect
	// Moving: the target may still be moving, and is tapped anyway if the point is on it. Without
	// it, an iOS tap is withheld when the target moved since the agent read it just before the
	// touch (issue #103).
	Moving bool
}

type ActionResult struct {
	Probed   bool   // iOS checked for the in-app delivery probe
	Targeted bool   // iOS probe observed a completed touch on the named button's live frame
	Withheld string // "preflight" or "delivery": stale tap provably did not reach the app
	Guarded  bool   // delivery was guarded; never replay an ambiguous injected tap
	Settle   SettleResult
	InjectMs int
	SettleMs int
	Seq      int64
	// Clicked: whether a view reported a click (its handler ran) during the action; nil
	// where the platform cannot identify the recipient (including many SwiftUI gestures).
	Clicked *bool
	// ClicksBefore: the agent's count of view clicks just before the action (Android), for a
	// later look through ClickCounter: a click can be reported after the action's reply.
	ClicksBefore *int64
}

// ClickCounter is a driver whose device counts view clicks (Android): the count now, to
// compare with an action's ClicksBefore.
type ClickCounter interface {
	Clicks(ctx context.Context) (int64, error)
}

// MountedCoverWaiter observes a previously identified overlay at the next tap
// point, including a faded-out view that still routes input. It waits on the
// device's events instead of streaming whole trees to the runner.
type MountedCoverWaiter interface {
	AwaitCoverGone(ctx context.Context, window int, pkg, id, class string, x, y int, timeout time.Duration) (observed, gone bool, err error)
}

// CheckedTextTyper verifies first-key delivery on the device, avoiding full screen reads
// by the runner. Other drivers retain the runner's existing typing path.
type CheckedTextTyper interface {
	TypeTextChecked(context.Context, string, ActionOpts) (ActionResult, error)
}

// NetworkMock answers the requests whose URL matches URL (Chrome's URL pattern: * any run of
// characters, ? one character, \ escapes) and whose method is Method ("" for any): with
// Status, Headers and Body after Delay, or as a network error when Fail is set.
type NetworkMock struct {
	URL     string
	Method  string
	Status  int
	Headers [][2]string
	Body    []byte
	Delay   time.Duration
	Fail    bool
}

// NetworkMocker is a driver that can answer requests with mocks (the web driver). Mocks add
// up, the latest matching one winning, until ClearNetworkMocks.
type NetworkMocker interface {
	MockNetwork(ctx context.Context, m NetworkMock) error
	ClearNetworkMocks(ctx context.Context) error
}

// RecordingParts is a driver whose last recording may have been saved as several files, in
// order: the first at the path StartRecording was given, the others next to it (Android
// before 14 records at most 3 minutes at a time).
type RecordingParts interface {
	RecordingParts() []string
}

// ErrTypingVerificationUnavailable means no text was injected. The runner must use
// its full-screen first-key check, which also handles custom accessibility widgets.
var ErrTypingVerificationUnavailable = errors.New("focused field verification unavailable")

// LaunchOpts mirror launchApp's options.
type LaunchOpts struct {
	ClearState    bool
	ClearKeychain bool
	StopApp       bool // stop it first (default: true)
	Permissions   map[string]string
	Arguments     map[string]any
}

// Event is something the device pushed.
type Event struct {
	Kind string // "changed", "settled", "disconnected"
	Seq  int64
	Pkg  string
}

// Releaser is a driver that can stop its on-device agent, freeing the device for other tools
// (the agent holds what only one test tool can hold at a time: Android's UiAutomation slot, an
// iOS simulator's XCTest runner). Close leaves the agent running for the next run.
type Releaser interface {
	Release(ctx context.Context) error
}

// Killer is a driver that can end at once what it started on this machine, without waiting
// on anything (web: Chrome's process, and its profile folder): for a Close that did not
// finish in time.
type Killer interface {
	Kill()
}

// Noter is a driver that did something on its own that the run's trace must show (web: it
// answered a JavaScript dialog the page opened). Notes hands each note out once.
type Noter interface {
	Notes() []string
}

// LostChecker is a driver that knows when its connection to the device is gone for good: the
// browser or agent cannot be reached and the driver's own reconnecting (if it has any) gave
// up. Lost says why, or returns nil while the driver can still be used. A run that keeps
// drivers between flows opens the device again instead of handing a lost driver out.
type LostChecker interface {
	Lost() error
}

// Keeper is a driver whose on-device agent, left without a connection, exits soon (so a
// killed run frees its devices) unless asked to stay for later runs (--keep-agents).
type Keeper interface {
	Keep(ctx context.Context) error
}

// Driver drives one device.
type Driver interface {
	Platform() string
	Serial() string
	Name() string
	Size() (w, h int)

	// Screen reads the whole screen. It never waits for the UI.
	Screen(ctx context.Context) (*ui.Screen, error)
	// Seq is the device's UI change counter, as last pushed.
	Seq() int64
	// Events delivers pushed device events until the context ends. Call cancel to stop.
	Events() (<-chan Event, func())
	// WaitSettled blocks until the UI has no pending change, or the timeout.
	WaitSettled(ctx context.Context, timeout time.Duration) error
	// Settle waits for the UI to react to something done when the change counter was since.
	Settle(ctx context.Context, since int64, noChangeMs int, timeout time.Duration) (SettleResult, error)

	Tap(ctx context.Context, x, y int, o ActionOpts) (ActionResult, error)
	DoubleTap(ctx context.Context, x, y int, o ActionOpts) (ActionResult, error)
	LongPress(ctx context.Context, x, y int, hold time.Duration, o ActionOpts) (ActionResult, error)
	Swipe(ctx context.Context, x1, y1, x2, y2 int, dur time.Duration, o ActionOpts) (ActionResult, error)
	TypeText(ctx context.Context, text string, o ActionOpts) (ActionResult, error)
	EraseText(ctx context.Context, n int, o ActionOpts) (ActionResult, error)
	// PressKey takes a flow key name (enter, back, home, backspace, volume up, ...).
	PressKey(ctx context.Context, key string, o ActionOpts) (ActionResult, error)
	HideKeyboard(ctx context.Context) error

	Screenshot(ctx context.Context, scale float64, format string) ([]byte, error)

	LaunchApp(ctx context.Context, appID string, o LaunchOpts) error
	StopApp(ctx context.Context, appID string) error
	KillApp(ctx context.Context, appID string) error
	ClearState(ctx context.Context, appID string) error
	ClearKeychain(ctx context.Context) error
	OpenLink(ctx context.Context, link, appID string, autoVerify, browser bool) error
	SetPermissions(ctx context.Context, appID string, perms map[string]string) error
	SetLocation(ctx context.Context, lat, lon float64) error
	SetOrientation(ctx context.Context, orientation string) error
	SetAirplaneMode(ctx context.Context, on bool) error
	ToggleAirplaneMode(ctx context.Context) error
	AddMedia(ctx context.Context, paths []string) error
	InstallApp(ctx context.Context, path string) error
	Foreground(ctx context.Context) (string, error)
	// WaitForApp blocks until the app owns the foreground window (event-driven).
	WaitForApp(ctx context.Context, appID string, timeout time.Duration) error

	StartRecording(ctx context.Context, path string) error
	StopRecording(ctx context.Context) error

	Close() error
}
