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
	Target *ActionTarget
	// Watch: the bounds of the element an iOS tap without a Target aims at (one found by its
	// text, not its id). The probe says whether the touch went to the view with those bounds
	// (ActionResult.Reached, issue #324); the touch is never withheld for it.
	Watch *ui.Rect
	// Aim: the node an Android tap aims at, the very one of the read it was found in (issue
	// #321). The agent asks its app for it again (refresh) just before the touch goes down, and
	// says where it was then (ActionResult.AimAt).
	Aim      *Aim
	NoSettle bool // return as soon as the input is injected
	React    bool // return once the UI starts reacting, without waiting for it to finish
	// Hold: taps on iOS: how long the touch is held (0: the agent's default). Swipes on iOS: a
	// press-and-hold drag (issue #330), the finger still this long where it goes down before it
	// moves over the swipe's duration, then still a moment where it ends before it lifts.
	Hold time.Duration
	Drag bool // swipes: a drag that does not fling (the content stops at the lift)
	Rest bool // swipes on iOS: still before moving, a slow creep before lifting (CI mode; issues #127, #182)
	// InRepeat: a tap after the first of a tapOn's repeat, timed by the flow's delay: iOS does
	// not keep it apart from the tap before as it does two separate taps (issue #212).
	InRepeat   bool
	NoChangeMs int           // how long to wait for the UI to start changing (default 250)
	Timeout    time.Duration // longest settle wait (default 5 s)
}

// Aim names a node of an Android read: the read (ui.Screen.Read), the agent's id of its window,
// and its place in that window's nodes (ui.Node.Ref), whether or not it has a resource id.
type Aim struct {
	Read   int64
	Window int
	Ref    int
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
	Probed   bool // iOS checked for the in-app delivery probe
	Targeted bool // iOS probe observed a completed touch on the named button's live frame
	// Withheld: a stale tap provably did not reach the app. "preflight": the agent found the
	// point off the target before the touch; "delivery": the probe found it off the target as
	// the touch went down; "moved": the probe found the target moved since the agent read it
	// just before the touch (issue #103).
	Withheld string
	// WithheldWhy: why the agent's own check withheld the touch at "preflight" (issue #39: no
	// button with the id, more than one, disabled, another app in front, the point off its live
	// frame); "" from an older agent. LiveFrame: the button's frame as that check read it, when
	// it found one.
	WithheldWhy string
	LiveFrame   *ui.Rect
	// Cancelled: the iOS probe saw the touch reach the app on the target and end cancelled
	// (issue #131): no handler took it as a tap, and it may be sent again.
	Cancelled bool
	// Touch: how the iOS probe saw a guarded tap's touch end (issue #134): "ended" (it lifted;
	// Targeted says whether it began on the target's live frame), "cancelled", or "none" (no
	// receipt for it in time). "" where the agent armed no receipt, or is older.
	Touch   string
	Guarded bool // delivery was guarded; never replay an ambiguous injected tap
	// ChangedSinceRead: the iOS agent saw a solid UI change (not a ticker's) after the host's
	// read of the tree a guarded tap aimed from, before its touch went out (issue #149): the
	// target may have moved just before the touch, unseen. false where none was, or unknown.
	ChangedSinceRead bool
	Settle           SettleResult
	InjectMs         int
	SettleMs         int
	Seq              int64
	// AimAt: where the node of ActionOpts.Aim was just before the touch went down, as the
	// Android agent asked its app then, its cache passed by (issue #321). nil where unknown: no
	// Aim, an older agent, a read the agent no longer keeps, a node gone or no longer shown.
	AimAt *ui.Rect
	// Clicked: whether a view reported a click (its handler ran) during the action; nil
	// where the platform cannot identify the recipient (including many SwiftUI gestures).
	Clicked *bool
	// ClicksBefore: the agent's count of view clicks just before the action (Android), for a
	// later look through ClickCounter: a click can be reported after the action's reply.
	ClicksBefore *int64
	// NotInjected: the platform reported a tap's touch not injected (Android): "down" when it
	// refused the DOWN (nothing else was sent), "up" when the UP was not delivered (the
	// dispatcher dropped the gesture: issue #204). No app took such a tap as one.
	NotInjected string
	// WindowWait: how long the agent held the touch for the window under it, just added, to
	// take input (Android, issue #204).
	WindowWait time.Duration
	// FirstInWindow: the touch was the first input (touch or key) the window under its point
	// ever got (Android, issue #280); WindowAge: how long that window had been on screen when
	// the touch went down (0: unknown). A window just added may take no input while it comes
	// in (a dialog's guard, an enter transition): such a touch may have been swallowed. A
	// window that already took input, the app's own window back after a modal closed included,
	// never is "first". Windows already there when the agent started are not.
	FirstInWindow bool
	WindowAge     time.Duration
	// WindowOver: that window came in over the app (no Activity opened it: a Dialog, a bottom
	// sheet, a popup, that came while a window of the app was listed), rather than being a
	// screen (an Activity). Only such a window guards against early touches (issue #280 review).
	// An Activity of another package floating on the app's window (inset on every side within
	// it) is over the app too: Android's runtime permission dialog (issue #317).
	WindowOver bool
	// AppEntering: the touch went to an iOS app still on its way to the foreground, not yet
	// taking input (the agent's appActive: false, issue #290): iOS's counterpart of
	// FirstInWindow.
	AppEntering bool
	// Unconfirmed: the iOS agent saw no "touches completed" from the app for a swipe, which
	// testmanagerd confirmed only once its own wait for one timed out (issue #111: "Timed out
	// after waiting 5.0s for TouchEventsCompleted, event will be implicitly confirmed"). Its
	// touches may never have reached the app.
	Unconfirmed bool
	// DeliveryToken: the iOS agent's token for asking, after the settle, whether the app in
	// front got the touches of a tap or double tap (DeliveryChecker, issue #334); 0 for none.
	DeliveryToken int64
	// Reached: the iOS probe saw the touch of a tap with Watch go to the view with the watched
	// bounds, or one inside it (issue #324): it reached the target, not something over it (a
	// veil hidden from accessibility). false where it went elsewhere, or unknown (no probe, an
	// older agent, a target as big as half the screen, a button with no view of its own, as
	// SwiftUI's and Flutter's: their touches go to a hosting view).
	Reached bool
	// Typing: how Android checked typing put the keys in, when not in chunks (issue #279):
	// "acknowledged" (one at a time, each once the field showed the one before: the field
	// rewrites what is typed), "repaired" (keys the field lost were typed again). "" for
	// chunks the field showed in full, and from an older agent.
	Typing string
}

// Delivery verdicts (DeliveryChecker, issue #334).
const (
	Delivered    = "delivered"
	NotDelivered = "notDelivered" // the system completed the touches and the app went on without them
	Unknown      = "unknown"      // no proof either way (a stalled app, no word in time)
)

// DeliveryChecker is a device (iOS) that can say, after an action's settle, whether the app in
// front got the touches of the action with this token (ActionResult.DeliveryToken): Delivered,
// NotDelivered, or Unknown by wait.
type DeliveryChecker interface {
	Delivery(ctx context.Context, token int64, wait time.Duration) (DeliveryVerdict, error)
}

// DeliveryVerdict is the agent's verdict on a tap's touches and what it rests on (for the
// trace): when SpringBoard reported them completed, in ms after the lift (nil: no report), and
// how long each of the app's answers to the agent's asks to go idle took, in ms.
type DeliveryVerdict struct {
	Verdict  string
	ReportMs *int
	AnswerMs []int
	// Pending: the wait ran out before the agent decided (Verdict is then Unknown, and the
	// evidence what it had seen so far).
	Pending bool
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

// DialogAnswer is how the page's JavaScript dialogs are answered (answerDialogs, issue #91).
// The zero value is tb's default: OK, a prompt() with its own default text. An alert() has
// only OK, and a beforeunload "Leave site?" is always left, whatever the answer.
type DialogAnswer struct {
	Cancel  bool   // Cancel: confirm() returns false, prompt() returns null
	Text    string // what prompt() returns (OK with this text), when HasText
	HasText bool
}

// DialogAnswerer is a driver whose JavaScript dialogs a flow can choose the answer to (the
// web driver). The answer holds for every dialog, in every tab, until the next call.
type DialogAnswerer interface {
	AnswerDialogs(ctx context.Context, a DialogAnswer) error
}

// Dialog is a JavaScript dialog the page opened, as tb answered it (assertDialog, issue #91).
type Dialog struct {
	Type       string // alert, confirm, prompt or beforeunload
	Message    string
	Default    string // a prompt's default text
	URL        string // the document that opened it (a frame's own, for a frame)
	Answer     string // how tb answered it: "OK", "Cancel", `OK with "Ada"`, "left the page"
	Background bool   // it opened in a tab the flow was not driving
	Note       string // the line the run's trace got for it (driver.Noter)
}

// DialogLog is a driver that keeps the JavaScript dialogs it answered (the web driver).
// Dialogs returns those opened since mark, a value an earlier call returned (0 is the
// driver's start), oldest first, and the mark for the next call.
type DialogLog interface {
	Dialogs(since int) ([]Dialog, int)
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

// ErrNoSuchKey means the platform has no such key (iOS's back): nothing was sent. Only such a
// pressKey does nothing on iOS with a hint (issue #213); any other failure of a press fails
// the step (issue #286).
var ErrNoSuchKey = errors.New("no such key")

// NoSuchKey is ErrNoSuchKey, said as msg.
func NoSuchKey(msg string) error { return noSuchKey(msg) }

type noSuchKey string

func (e noSuchKey) Error() string        { return string(e) }
func (e noSuchKey) Is(target error) bool { return target == ErrNoSuchKey }

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

// Stall is how long a device went without answering, and what showed it (issue #237).
type Stall struct {
	For     time.Duration // 0: no stall
	Device  string        // what stalled: "the simulator"
	Stopped bool          // it gave up on a read (it stopped answering), not only answered slowly
	Signs   []string      // "an accessibility timeout", "app main thread held", ...
}

// StallReporter is a driver that can tell when its device stopped answering for a long while
// (an iOS simulator starved by a busy host: screen reads that take tens of seconds or time
// out), so a step that failed meanwhile can say so instead of only "not found".
// StallSince is the stalls' time from t on (overlaps counted once).
type StallReporter interface {
	StallSince(t time.Time) Stall
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
