package driver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/elforama/testbuddy/internal/ui"
)

type SettleResult string

const (
	Changed  SettleResult = "changed"
	NoChange SettleResult = "no_change"
	Restless SettleResult = "timeout"
)

type ActionOpts struct {
	Target     *ActionTarget
	Watch      *ui.Rect
	Aim        *Aim
	NoSettle   bool
	React      bool
	Hold       time.Duration
	Drag       bool
	Rest       bool
	InRepeat   bool
	NoChangeMs int
	Timeout    time.Duration
}

type Aim struct {
	Read   int64
	Window int
	Ref    int
}

type ActionTarget struct {
	Seq    int64
	ID     string
	Window int
	Bounds ui.Rect
	Moving bool
}

type PressCut struct{ SeenMs, PressMs, StallMs int }

type ActionResult struct {
	Probed           bool
	Targeted         bool
	Withheld         string
	WithheldWhy      string
	LiveFrame        *ui.Rect
	Cancelled        bool
	Touch            string
	Guarded          bool
	ChangedSinceRead bool
	Settle           SettleResult
	InjectMs         int
	SettleMs         int
	Seq              int64
	AimAt            *ui.Rect
	Clicked          *bool
	ClicksBefore     *int64
	NotInjected      string
	PressCut         *PressCut
	WindowWait       time.Duration
	FirstInWindow    bool
	WindowAge        time.Duration
	WindowOver       bool
	AppEntering      bool
	Unconfirmed      bool
	DeliveryToken    int64
	Reached          bool
	Typing           string
}

const (
	Delivered    = "delivered"
	NotDelivered = "notDelivered"
	Unknown      = "unknown"
)

type DeliveryChecker interface {
	Delivery(ctx context.Context, token int64, wait time.Duration) (DeliveryVerdict, error)
}

type DeliveryVerdict struct {
	Verdict  string
	ReportMs *int
	AnswerMs []int
	Pending  bool
	Implicit bool
}

type ClickCounter interface {
	Clicks(ctx context.Context) (int64, error)
}

type MountedCoverWaiter interface {
	AwaitCoverGone(ctx context.Context, window int, pkg, id, class string, x, y int, timeout time.Duration) (observed, gone bool, err error)
}

type CheckedTextTyper interface {
	TypeTextChecked(context.Context, string, ActionOpts) (ActionResult, error)
}

type NetworkMock struct {
	URL     string
	Method  string
	Status  int
	Headers [][2]string
	Body    []byte
	Delay   time.Duration
	Fail    bool
}

type NetworkMocker interface {
	MockNetwork(ctx context.Context, m NetworkMock) error
	ClearNetworkMocks(ctx context.Context) error
}

type DialogAnswer struct {
	Cancel  bool
	Text    string
	HasText bool
}

type DialogAnswerer interface {
	AnswerDialogs(ctx context.Context, a DialogAnswer) error
}

type Dialog struct {
	Type       string
	Message    string
	Default    string
	URL        string
	Answer     string
	Background bool
	Note       string
}

type DialogLog interface {
	Dialogs(since int) ([]Dialog, int)
}

type AppLog struct {
	Crash string
	Trace []string
	Lines []string
}

func (l AppLog) Text(appID string) string {
	var b strings.Builder
	if l.Crash != "" {
		fmt.Fprintf(&b, "crashed: %s: %s\n", appID, l.Crash)
		for _, t := range l.Trace {
			b.WriteString("  " + t + "\n")
		}
	}
	for _, line := range l.Lines {
		b.WriteString(line + "\n")
	}
	return b.String()
}

func AppLogSeconds(last time.Duration) int {
	return int((last+time.Second-1)/time.Second) + 1
}

type AppLogReader interface {
	AppLog(ctx context.Context, appID string, last time.Duration) (AppLog, error)
}

type RecordingParts interface {
	RecordingParts() []string
}

type RecordingClock interface {
	RecordingStarted() time.Time
}

type RecordingMark interface {
	RecordingMark() (shown time.Time, held time.Duration, ok bool)
}

type VideoClock interface {
	VideoStarted(ctx context.Context, path string) (time.Time, bool)
}

type ExactRecordingClock interface {
	RecordingClockExact() bool
}

var ErrTypingVerificationUnavailable = errors.New("focused field verification unavailable")

var ErrNoSuchKey = errors.New("no such key")

func NoSuchKey(msg string) error { return noSuchKey(msg) }

type noSuchKey string

func (e noSuchKey) Error() string        { return string(e) }
func (e noSuchKey) Is(target error) bool { return target == ErrNoSuchKey }

type LaunchOpts struct {
	ClearState    bool
	ClearKeychain bool
	StopApp       bool
	Permissions   map[string]string
	Arguments     map[string]any
}

type Event struct {
	Kind string
	Seq  int64
	Pkg  string
}

type Releaser interface {
	Release(ctx context.Context) error
}

type Killer interface {
	Kill()
}

type Noter interface {
	Notes() []string
}

type Stall struct {
	For     time.Duration
	Device  string
	Stopped bool
	Signs   []string
}

type StallReporter interface {
	StallSince(t time.Time) Stall
}

type LostChecker interface {
	Lost() error
}

type Keeper interface {
	Keep(ctx context.Context) error
}

type Driver interface {
	Platform() string
	Serial() string
	Name() string
	Size() (w, h int)

	Screen(ctx context.Context) (*ui.Screen, error)
	Seq() int64
	Events() (<-chan Event, func())
	WaitSettled(ctx context.Context, timeout time.Duration) error
	Settle(ctx context.Context, since int64, noChangeMs int, timeout time.Duration) (SettleResult, error)

	Tap(ctx context.Context, x, y int, o ActionOpts) (ActionResult, error)
	DoubleTap(ctx context.Context, x, y int, o ActionOpts) (ActionResult, error)
	LongPress(ctx context.Context, x, y int, hold time.Duration, o ActionOpts) (ActionResult, error)
	Swipe(ctx context.Context, x1, y1, x2, y2 int, dur time.Duration, o ActionOpts) (ActionResult, error)
	TypeText(ctx context.Context, text string, o ActionOpts) (ActionResult, error)
	EraseText(ctx context.Context, n int, o ActionOpts) (ActionResult, error)
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
	WaitForApp(ctx context.Context, appID string, timeout time.Duration) error

	StartRecording(ctx context.Context, path string) error
	StopRecording(ctx context.Context) error

	Close() error
}
