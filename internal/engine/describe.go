package engine

import (
	"fmt"
	"strings"

	"github.com/elforama/testbuddy/internal/flow"
)

func selDesc(s *flow.Selector) string {
	if s == nil {
		return "element"
	}
	var parts []string
	if s.Text != "" {
		parts = append(parts, fmt.Sprintf("%q", s.Text))
	}
	if s.ID != "" {
		parts = append(parts, "id: "+s.ID)
	}
	if s.CSS != "" {
		parts = append(parts, "css: "+s.CSS)
	}
	if s.Index != "" {
		parts = append(parts, "index: "+s.Index)
	}
	for _, r := range []struct {
		n string
		s *flow.Selector
	}{{"below", s.Below}, {"above", s.Above}, {"left of", s.LeftOf}, {"right of", s.RightOf},
		{"containing child", s.ContainsChild}, {"child of", s.ChildOf}} {
		if r.s != nil {
			parts = append(parts, r.n+" "+selDesc(r.s))
		}
	}
	for _, f := range []struct {
		n string
		b *bool
	}{{"enabled", s.Enabled}, {"selected", s.Selected}, {"checked", s.Checked}, {"focused", s.Focused}} {
		if f.b != nil {
			parts = append(parts, fmt.Sprintf("%s: %v", f.n, *f.b))
		}
	}
	if len(parts) == 0 {
		return "element"
	}
	return strings.Join(parts, ", ")
}

// describe is the human description of a command, in the format's usual wording.
func (r *Runner) describe(c *flow.Command) string {
	if c.Label != "" {
		if c.LabelRaw {
			return c.Label
		}
		if v, err := r.eval(c.Label); err == nil {
			return v
		}
		return c.Label
	}
	if c.AppID == "" && r.appID != "" {
		switch c.Name {
		case "launchApp", "stopApp", "killApp", "clearState":
			cp := *c
			cp.AppID = r.appID
			return Describe(&cp)
		}
	}
	return Describe(c)
}

// Describe describes a command without evaluating anything.
func Describe(c *flow.Command) string {
	switch c.Name {
	case "tapOn":
		if c.Selector == nil {
			return "Tap on point (" + c.Point + ")"
		}
		return "Tap on " + selDesc(c.Selector)
	case "doubleTapOn":
		return "Double tap on " + selDesc(c.Selector)
	case "longPressOn":
		return "Long press on " + selDesc(c.Selector)
	case "assertVisible":
		return "Assert that " + selDesc(c.Selector) + " is visible"
	case "assertNotVisible":
		return "Assert that " + selDesc(c.Selector) + " is not visible"
	case "assertTrue":
		return "Assert that " + describeCondition(c.Cond)
	case "extendedWaitUntil":
		return "Wait until " + describeCondition(c.Cond)
	case "inputText":
		return "Input text " + c.Text
	case "eraseText":
		return fmt.Sprintf("Erase %d characters", c.Count)
	case "copyTextFrom":
		return "Copy text from " + selDesc(c.Selector)
	case "setClipboard":
		return "Set clipboard to " + c.Text
	case "pasteText":
		return "Paste text"
	case "swipe":
		switch {
		case c.From != nil:
			return fmt.Sprintf("Swipe %s from %s", c.Direction, selDesc(c.From))
		case c.Direction != "":
			return "Swipe " + c.Direction
		default:
			return fmt.Sprintf("Swipe from (%s) to (%s)", c.Start, c.End)
		}
	case "scroll":
		return "Scroll vertically"
	case "scrollUntilVisible":
		if c.In != nil {
			return fmt.Sprintf("Scroll %s in the list of %s until %s is visible", c.Direction, selDesc(c.In), selDesc(c.Selector))
		}
		return fmt.Sprintf("Scroll %s until %s is visible", c.Direction, selDesc(c.Selector))
	case "scrollUntilVisibleIn":
		return fmt.Sprintf("Scroll to %s in the list of %s", selDesc(c.Selector), selDesc(c.In))
	case "back":
		return "Press back"
	case "hideKeyboard":
		return "Hide keyboard"
	case "pressKey":
		return "Press " + c.Text + " key"
	case "launchApp":
		return "Launch app " + quoteOr(c.AppID, "")
	case "stopApp":
		return "Stop app " + quoteOr(c.AppID, "")
	case "killApp":
		return "Kill app " + quoteOr(c.AppID, "")
	case "clearState":
		return "Clear state of " + quoteOr(c.AppID, "app")
	case "clearKeychain":
		return "Clear keychain"
	case "setPermissions":
		return "Set permissions"
	case "openLink":
		return "Open " + c.Text
	case "runFlow":
		if c.SubFlow != nil {
			s := "Run " + c.SubFlow.Name()
			if c.When != nil {
				s += " when " + describeCondition(c.When)
			}
			return s
		}
		if c.When != nil {
			return "Run flow when " + describeCondition(c.When)
		}
		return "Run flow"
	case "runScript":
		return "Run " + baseName(c.SubFile)
	case "evalScript":
		return "Run " + c.Text
	case "repeat":
		switch {
		case c.Times != "" && c.While != nil:
			return fmt.Sprintf("Repeat while %s (up to %s times)", describeCondition(c.While), c.Times)
		case c.Times != "":
			return "Repeat " + c.Times + " times"
		case c.While != nil:
			return "Repeat while " + describeCondition(c.While)
		}
		return "Repeat"
	case "retry":
		return "Retry up to " + orDefault(c.MaxRetries, "1") + " times"
	case "takeScreenshot":
		return "Take screenshot " + c.Text
	case "startRecording":
		return "Start recording " + c.Text
	case "stopRecording":
		return "Stop recording"
	case "assertScreenshot":
		return "Assert screenshot " + c.Text
	case "waitForAnimationToEnd":
		return "Wait for animation to end"
	case "setLocation":
		return fmt.Sprintf("Set location (%s, %s)", c.Latitude, c.Longitude)
	case "travel":
		return fmt.Sprintf("Travel through %d points", len(c.Points))
	case "setOrientation":
		return "Set orientation " + c.Text
	case "addMedia":
		return fmt.Sprintf("Add %d media files", len(c.Files))
	case "setAirplaneMode":
		return fmt.Sprintf("Set airplane mode %s", onOff(c.Enabled))
	case "toggleAirplaneMode":
		return "Toggle airplane mode"
	case "mockNetwork":
		return describeMock(c.Mock)
	case "clearNetworkMocks":
		return "Clear network mocks"
	case "answerDialogs":
		return describeDialogs(c.Dialogs)
	case "assertDialog":
		return describeAssertDialog(c.DialogCheck)
	case "setDarkMode":
		return fmt.Sprintf("Set dark mode %s", onOff(c.Enabled))
	case "toggleDarkMode":
		return "Toggle dark mode"
	case "assertDarkMode":
		return "Assert dark mode"
	case "assertLightMode":
		return "Assert light mode"
	case "assertWithAI", "assertNoDefectsWithAI", "extractTextWithAI":
		return c.Name + " " + c.Text
	case "signal":
		return "Signal " + c.Signal
	case "waitForSignal":
		return "Wait for signal " + c.Signal
	case "on":
		return "On " + c.On
	case "parallel":
		return "In parallel: " + strings.Join(c.ParallelOrder, ", ")
	}
	if strings.HasPrefix(c.Name, "inputRandom") {
		return "Input random " + strings.ToLower(c.RandomKind)
	}
	return c.Name
}

func quoteOr(s, def string) string {
	if s == "" {
		return def
	}
	return fmt.Sprintf("%q", s)
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func baseName(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}
