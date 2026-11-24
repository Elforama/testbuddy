package flow

type KV struct {
	Key   string
	Value string
}

type Flow struct {
	Path     string
	Dir      string
	Config   Config
	Commands []*Command

	DuplicateKeys []DuplicateKey
}

type DuplicateKey struct {
	Key   string
	Line  int
	First int
}

func (f *Flow) Name() string {
	if f.Config.Name != "" {
		return f.Config.Name
	}
	return baseNoExt(f.Path)
}

func (f *Flow) FreshLaunch() *Command {
	steps := f.Commands
	if len(f.Config.OnFlowStart) > 0 {
		steps = f.Config.OnFlowStart
	}
	if len(steps) == 0 {
		return nil
	}
	c := steps[0]
	if c.Name != "launchApp" || c.Optional || (c.StopApp != nil && !*c.StopApp) {
		return nil
	}
	return c
}

type Config struct {
	AppID          string
	URL            string
	Name           string
	Tags           []string
	Env            []KV
	OnFlowStart    []*Command
	OnFlowComplete []*Command
	Properties     map[string]string
	PropertyOrder  []string
	Ext            map[string]any
	ExtLines       map[string]int

	Devices []*DeviceRole
}

func (c *Config) Web() bool { return c.URL != "" }

type DeviceRole struct {
	Name     string
	Platform string
	Device   string
	AppID    string
	Env      []KV
}

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

	Optional bool
}

func (s *Selector) HasCriteria() bool {
	if s == nil {
		return false
	}
	return s.Text != "" || s.ID != "" || s.Index != "" || s.CSS != "" || s.Width != nil || s.Height != nil ||
		s.Enabled != nil || s.Selected != nil || s.Checked != nil || s.Focused != nil || len(s.Traits) > 0 ||
		s.Below != nil || s.Above != nil || s.LeftOf != nil || s.RightOf != nil || s.ContainsChild != nil ||
		s.ChildOf != nil || len(s.ContainsDescendants) > 0
}

type Condition struct {
	Platform   string
	Visible    *Selector
	NotVisible *Selector
	True       *string
	Label      string
	Timeout    string
}

type Command struct {
	Name     string
	File     string
	Line     int
	Label    string
	Optional bool
	LabelRaw bool

	Selector *Selector

	Point                 string
	Repeat                int
	Delay                 int
	RetryTapIfNoChange    *bool
	WaitUntilVisible      bool
	WaitToSettleTimeoutMs int
	LongPress             bool

	Cond    *Condition
	Timeout string

	Text string

	RandomKind string
	Length     int

	Count int

	Direction string
	Start     string
	End       string
	From      *Selector
	Duration  int

	To   *Selector
	Hold int

	In               *Selector
	Speed            string
	VisibilityPct    int
	CenterElement    bool
	ScrollTimeoutSet bool

	AppID         string
	ClearState    bool
	ClearKeychain bool
	StopApp       *bool
	Permissions   []KV
	Arguments     []ArgKV

	AutoVerify bool
	Browser    bool

	SubFile    string
	SubFlow    *Flow
	Commands   []*Command
	Env        []KV
	When       *Condition
	Script     string
	Times      string
	While      *Condition
	MaxRetries string

	CropOn    *Selector
	Threshold string

	Latitude, Longitude string
	Points              [][2]float64
	TravelSpeed         string

	Files []string

	Enabled bool

	OutputVariable string

	Mock *NetworkMock

	Dialogs *DialogChoice

	DialogCheck *DialogCheck

	On            string
	Parallel      map[string][]*Command
	ParallelOrder []string
	Signal        string
}

type NetworkMock struct {
	URL          string
	Method       string
	Status       int
	Body         string
	BodyFile     string
	BodyFromFile bool
	Headers      []KV
	Delay        int
	Fail         bool
}

type DialogChoice struct {
	Cancel  bool
	Button  string
	Text    string
	HasText bool
}

type DialogCheck struct {
	Text    string
	HasText bool
	Type    string
}

type ArgKV struct {
	Key   string
	Value any
}

func (c *Command) Mutating() bool {
	switch c.Name {
	case "assertVisible", "assertNotVisible", "assertTrue", "extendedWaitUntil", "assertScreenshot",
		"assertWithAI", "assertNoDefectsWithAI", "extractTextWithAI", "copyTextFrom", "setClipboard",
		"clearKeychain", "setPermissions", "takeScreenshot", "startRecording", "stopRecording",
		"assertDarkMode", "assertLightMode", "defineVariables", "signal", "waitForSignal",
		"runFlow", "repeat", "retry", "scrollUntilVisibleIn",
		"mockNetwork", "clearNetworkMocks",
		"answerDialogs", "assertDialog":
		return false
	}
	return true
}

var HeaderKeys = []string{
	"appId", "url", "name", "tags", "env", "properties", "onFlowStart", "onFlowComplete",
	"jsEngine", "androidWebViewHierarchy",
	"devices",
}

var ignoredKeys = map[string]string{
	"androidWebViewHierarchy": "supported value is devtools",
}

func (c *Config) AndroidWebViewDevTools() bool {
	return c.Ext["androidWebViewHierarchy"] == "devtools"
}

func (c *Config) Ignored() []string {
	var out []string
	for k, why := range ignoredKeys {
		if k == "androidWebViewHierarchy" && c.AndroidWebViewDevTools() {
			continue
		}
		if _, ok := c.Ext[k]; ok {
			out = append(out, k+" is ignored: "+why)
		}
	}
	return out
}

func (c *Command) RetriesTapIfNoChange() bool {
	return c.RetryTapIfNoChange != nil && *c.RetryTapIfNoChange
}

func (c *Command) ForbidsTapReplay() bool {
	return c.RetryTapIfNoChange != nil && !*c.RetryTapIfNoChange
}
