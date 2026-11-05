// Package flow parses flow files (and Test Buddy's multi-device extension) into
// commands. Values interpolated at run time (${...}) are kept as raw strings;
// the engine evaluates them just before each command runs.
package flow

// KV is an ordered key/value pair (env blocks evaluate in order).
type KV struct {
	Key   string
	Value string
}

// Flow is one parsed flow file.
type Flow struct {
	Path     string // absolute path ("" for inline flows)
	Dir      string // directory relative paths resolve against
	Config   Config
	Commands []*Command
}

// Name is the flow's display name: config name, else the file name without extension.
func (f *Flow) Name() string {
	if f.Config.Name != "" {
		return f.Config.Name
	}
	return baseNoExt(f.Path)
}

// Config is the flow's header document.
type Config struct {
	AppID          string // the app to drive; a web flow's url: (which wins over appId)
	URL            string // the header's url:, raw (may hold ${...}); set means a web flow
	Name           string
	Tags           []string
	Env            []KV
	OnFlowStart    []*Command
	OnFlowComplete []*Command
	Properties     map[string]string
	PropertyOrder  []string
	Ext            map[string]any

	// Test Buddy multi-device extension: named device roles. Empty for single-device flows.
	Devices []*DeviceRole
}

// Web reports whether the header used url:, which makes this a web flow whatever the
// value turns out to be once ${...} is resolved.
func (c *Config) Web() bool { return c.URL != "" }

// DeviceRole is one device a multi-device flow drives.
type DeviceRole struct {
	Name     string
	Platform string // "android", "ios" or "" (any)
	Device   string // serial, UDID or device name; "" = any free device
	AppID    string
	Env      []KV
}

// Selector is the flow format's element selector. String fields may hold ${...}.
type Selector struct {
	Text  string
	ID    string
	Index string
	CSS   string

	Width, Height, Tolerance *int
	Enabled, Selected        *bool
	Checked, Focused         *bool
	Traits                   []string

	Below, Above, LeftOf, RightOf *Selector
	ContainsChild, ChildOf        *Selector
	ContainsDescendants           []*Selector

	Optional bool // selector-level optional (makes the command optional, no shorter timeout)
}

// HasCriteria reports whether any filtering key is set.
func (s *Selector) HasCriteria() bool {
	if s == nil {
		return false
	}
	return s.Text != "" || s.ID != "" || s.Index != "" || s.CSS != "" || s.Width != nil || s.Height != nil ||
		s.Enabled != nil || s.Selected != nil || s.Checked != nil || s.Focused != nil || len(s.Traits) > 0 ||
		s.Below != nil || s.Above != nil || s.LeftOf != nil || s.RightOf != nil || s.ContainsChild != nil ||
		s.ChildOf != nil || len(s.ContainsDescendants) > 0
}

// Condition is a `when` / `while` / assertion condition.
type Condition struct {
	Platform   string
	Visible    *Selector
	NotVisible *Selector
	True       *string
	Label      string
	Timeout    string // extension: ms to wait for visible/notVisible
}

// Command is one flow step. Name is the YAML key; the payload fields used depend on it.
type Command struct {
	Name     string
	File     string // file it was written in
	Line     int
	Label    string
	Optional bool
	// LabelInterpolated is false for the few commands whose label the format does not evaluate.
	LabelRaw bool

	Selector *Selector

	// tapOn / doubleTapOn / longPressOn
	Point                 string
	Repeat                int
	Delay                 int
	RetryTapIfNoChange    bool
	WaitUntilVisible      bool
	WaitToSettleTimeoutMs int
	LongPress             bool

	// assertTrue / extendedWaitUntil / when
	Cond    *Condition
	Timeout string

	// inputText, setClipboard, openLink(link), evalScript(script), setOrientation,
	// takeScreenshot/startRecording/assertScreenshot (path), assertWithAI(assertion), pressKey(key)
	Text string

	// inputRandom*
	RandomKind string
	Length     int

	// eraseText
	Count int

	// swipe
	Direction string
	Start     string
	End       string
	From      *Selector
	Duration  int

	// scrollUntilVisible
	In               *Selector // scrollUntilVisibleIn's list: drag across the list holding this element
	Speed            string
	VisibilityPct    int
	CenterElement    bool
	ScrollTimeoutSet bool

	// launchApp / stopApp / killApp / clearState / setPermissions
	AppID         string
	ClearState    bool
	ClearKeychain bool
	StopApp       *bool
	Permissions   []KV
	Arguments     []ArgKV

	// openLink
	AutoVerify bool
	Browser    bool

	// runFlow / retry / runScript / repeat
	SubFile    string // absolute
	SubFlow    *Flow  // parsed sub-flow file
	Commands   []*Command
	Env        []KV
	When       *Condition
	Script     string // runScript source
	Times      string
	While      *Condition
	MaxRetries string

	// takeScreenshot / assertScreenshot
	CropOn    *Selector
	Threshold string

	// setLocation / travel
	Latitude, Longitude string
	Points              [][2]float64
	TravelSpeed         float64

	// addMedia
	Files []string

	// setAirplaneMode / setDarkMode
	Enabled bool

	// extractTextWithAI
	OutputVariable string

	// Test Buddy multi-device extension
	On            string                // `on: <role>` with Commands
	Parallel      map[string][]*Command // `parallel: {role: [commands]}`
	ParallelOrder []string
	Signal        string // `signal: name` / `waitForSignal: name`
}

// ArgKV is one launch argument; Value keeps its YAML type (string, bool, int64, float64).
type ArgKV struct {
	Key   string
	Value any
}

// Mutating reports whether the command counts as a device interaction for the
// lookup-timeout clock (lookups after a recent interaction get a shorter timeout).
func (c *Command) Mutating() bool {
	switch c.Name {
	case "assertVisible", "assertNotVisible", "assertTrue", "extendedWaitUntil", "assertScreenshot",
		"assertWithAI", "assertNoDefectsWithAI", "extractTextWithAI", "copyTextFrom", "setClipboard",
		"clearKeychain", "setPermissions", "takeScreenshot", "startRecording", "stopRecording",
		"assertDarkMode", "assertLightMode", "defineVariables", "signal", "waitForSignal",
		// Composites act only through their commands, which count for themselves: a runFlow
		// whose condition held but whose commands only checked the screen changed nothing.
		"runFlow", "repeat", "retry", "scrollUntilVisibleIn":
		return false
	}
	return true
}

// ignoredKeys are header keys tb accepts (so the flow runs) but does not act on: why, for
// `tb check`.
var ignoredKeys = map[string]string{
	"androidWebViewHierarchy": "tb reads Android WebViews through the accessibility tree only, so elements hidden from accessibility (aria-hidden) are not found",
}

// Ignored are notes on the header keys tb accepts but does not act on.
func (c *Config) Ignored() []string {
	var out []string
	for k, why := range ignoredKeys {
		if _, ok := c.Ext[k]; ok {
			out = append(out, k+" is ignored: "+why)
		}
	}
	return out
}
