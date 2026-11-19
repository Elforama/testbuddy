// Package look turns a screen into a short listing an AI agent can read cheaply:
// one line per element a person can see or act on, each with a stable @ref.
//
//	android 1080x2400, 9 elements
//	@1 Text "Sign in"
//	@4 Field #email hint="Email"
//	@7 Button "Next" #next disabled
//	Keyboard
//
// Rules:
//   - status/navigation bars and the keyboard's keys are dropped; the keyboard is one line;
//   - a node gets a line if it has a label, is a field, is actionable, or has a unique useful id;
//   - a label repeated by its nearest listed ancestor is dropped (React Native repeats a button's
//     text in a child Text), as are icon-glyph-only labels, a control's inner view of its own
//     kind (iOS's switch in a switch), and SF Symbol glyphs in a labelled iOS button;
//   - wrappers that only repeat what they hold are dropped (a cell around its button, a
//     segmented control around its segments), as is a caption beside the control it names;
//   - iOS's Application and full-screen Toolbar are not listed, nor its scroll bars: those
//     become the scroll area's "(more below)";
//   - a slider's or progress view's reading is its value=, not its name;
//   - an element only partly in view says "partly off-screen"; one under the keyboard, a
//     modal layer or another element says "covered by keyboard", "covered by modal",
//     "covered by @N";
//   - ids are shown only when unique on screen.
package look

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/elforama/testbuddy/internal/occlusion"
	"github.com/elforama/testbuddy/internal/ui"
)

// Element is one listed line.
type Element struct {
	Ref    int      `json:"ref"`
	Kind   string   `json:"kind"`
	Label  string   `json:"label,omitempty"`
	ID     string   `json:"id,omitempty"`
	Hint   string   `json:"hint,omitempty"`
	Value  string   `json:"value,omitempty"`
	States []string `json:"states,omitempty"`
	New    bool     `json:"new,omitempty"` // not on the screen shown before (see MarkNew)
	// Covered is what is drawn over it where a person would touch it (package occlusion):
	// "keyboard", "modal" (a dialog, a sheet, a drawer: it must go first), "@N" (a listed
	// element, such as a pinned header), or "overlay".
	Covered string `json:"covered,omitempty"`
	// Offscreen: only part of it is on screen, or in view of its scroll area.
	Offscreen bool `json:"offscreen,omitempty"`
	// Scroll is the way a scroll view scrolls, when the platform says: vertical, horizontal
	// or both.
	Scroll string `json:"scroll,omitempty"`
	// Tappable: the element itself takes taps (not only something inside or around it).
	Tappable bool     `json:"tappable,omitempty"`
	Bounds   [4]int   `json:"bounds"`
	Node     *ui.Node `json:"-"`
	key      string
	slot     string // where it sits, for an element with no id (see Diff)
	contents string // what the diff compares
}

// Listing is a screen as an agent sees it.
type Listing struct {
	Platform string     `json:"platform"`
	Width    int        `json:"width"`
	Height   int        `json:"height"`
	App      string     `json:"app,omitempty"`
	Elements []*Element `json:"elements"`
	Keyboard bool       `json:"keyboard"`
	Dialog   string     `json:"dialog,omitempty"` // a system dialog over the app
	Screen   *ui.Screen `json:"-"`
	// Blocks is the outline: elements by ref, under the groups they sit in. With a system
	// dialog over the app, its top level is one block per window.
	Blocks []*Block `json:"blocks,omitempty"`
	More   *More    `json:"more,omitempty"` // off-screen elements outside any scroll area

	hints map[*ui.Node]More   // iOS scroll bars' "more" per scroll view (count unknown: -1)
	dirs  map[*ui.Node]string // the way a scroll view scrolls, when known
}

// Refs gives elements stable numbers across reads: an element keeps its ref while its
// identity (id, else kind+label, plus rank among equals) stays on screen.
type Refs struct {
	next  int
	byKey map[string]int
}

func NewRefs() *Refs { return &Refs{byKey: map[string]int{}} }

func (r *Refs) get(key string) int {
	if ref, ok := r.byKey[key]; ok {
		return ref
	}
	r.next++
	r.byKey[key] = r.next
	return r.next
}

var fieldClasses = map[string]bool{"EditText": true, "AutoCompleteTextView": true, "TextField": true, "SecureTextField": true, "SearchField": true, "TextView": false}

func isField(n *ui.Node) bool {
	if n.Editable {
		return true
	}
	return fieldClasses[n.ShortClass()]
}

func isIOSActionable(c string) bool {
	switch c {
	case "Button", "Cell", "Link", "Switch", "Toggle", "CheckBox", "Slider", "Stepper", "SegmentedControl", "Tab", "MenuItem", "PickerWheel":
		return true
	}
	return false
}

func actionable(n *ui.Node, platform string) bool {
	if platform == "web" {
		return n.Clickable
	}
	if platform == "ios" {
		return isIOSActionable(n.ShortClass())
	}
	return n.Clickable || n.LongClickable || n.Checkable
}

// webKind names a web element by its tag and role.
func webKind(n *ui.Node) string {
	c := n.Class
	role := ""
	if i := strings.IndexByte(c, '['); i >= 0 {
		role, c = strings.TrimSuffix(c[i+1:], "]"), c[:i]
	}
	tag, typ, _ := strings.Cut(c, ":")
	switch role {
	case "button", "menuitem", "tab":
		return "Button"
	case "progressbar":
		return "Progress"
	case "slider":
		return "Slider"
	case "link":
		return "Link"
	case "checkbox":
		return "CheckBox"
	case "switch":
		return "Switch"
	case "radio":
		return "Radio"
	case "heading":
		return "Heading"
	}
	switch tag {
	case "a":
		return "Link"
	case "button", "summary":
		return "Button"
	case "input":
		switch typ {
		case "checkbox":
			return "CheckBox"
		case "radio":
			return "Radio"
		case "submit", "button", "reset", "image":
			return "Button"
		case "password":
			return "SecureField"
		case "range":
			return "Slider"
		}
		return "Field"
	case "textarea":
		return "Field"
	case "select":
		return "Select"
	case "progress", "meter":
		return "Progress"
	case "h1", "h2", "h3", "h4", "h5", "h6":
		return "Heading"
	case "label", "p", "span", "li", "td", "th", "strong", "em", "b", "i", "small", "dt", "dd", "caption", "legend", "figcaption", "blockquote", "pre", "code":
		if n.Clickable {
			return "Button"
		}
		return "Text"
	}
	if n.Editable {
		return "Field"
	}
	if n.Clickable {
		return "Button"
	}
	if n.Text != "" {
		return "Text"
	}
	return "View"
}

// kind is the element's short kind name.
func kind(n *ui.Node, platform string) string {
	if platform == "web" {
		return webKind(n)
	}
	c := n.ShortClass()
	switch {
	case n.Password || c == "SecureTextField":
		return "SecureField"
	case isField(n):
		return "Field"
	}
	switch c {
	case "TextView", "StaticText", "Text":
		if actionable(n, platform) && platform == "android" {
			return "Button"
		}
		return "Text"
	case "Button", "ImageButton", "MaterialButton", "AppCompatButton":
		return "Button"
	case "CheckBox", "AppCompatCheckBox", "MaterialCheckBox":
		return "CheckBox"
	case "Switch", "SwitchCompat", "SwitchMaterial", "Toggle":
		return "Switch"
	case "RadioButton", "AppCompatRadioButton":
		return "Radio"
	case "ImageView", "Image", "Icon":
		if actionable(n, platform) {
			return "Button"
		}
		return "Image"
	case "Cell":
		return "Cell"
	case "Link":
		return "Link"
	case "Slider", "SeekBar":
		return "Slider"
	case "ProgressIndicator", "ActivityIndicator", "ProgressBar":
		return "Progress"
	case "Tab", "TabBar":
		return c
	case "WebView":
		return "Web"
	}
	if actionable(n, platform) {
		return "Button"
	}
	return "View"
}

func hasLetterOrDigit(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// usefulID is an id worth showing: React Native testIDs and iOS identifiers, and Android ids
// that are not generic layout ids.
func usefulID(id string) bool {
	if id == "" {
		return false
	}
	// Android layout ids ("com.app:id/frame") name views, not things a person sees.
	// React Native testIDs and iOS identifiers have no ":id/".
	return !strings.Contains(id, ":id/")
}

// ShortID strips Android's "package:id/" prefix.
func ShortID(id string) string {
	if i := strings.Index(id, ":id/"); i >= 0 {
		return id[i+4:]
	}
	return id
}

func barWindow(w *ui.Window, s *ui.Screen) bool {
	if w.Type == "app" || w.Type == "keyboard" {
		return false
	}
	b := w.Bounds
	if b.Empty() || s.Height == 0 {
		return false
	}
	thin := b.H() < s.Height/10
	return thin && (b.T <= 0 || b.B >= s.Height)
}

// Build lists the screen, assigning refs from refs.
func Build(s *ui.Screen, refs *Refs) *Listing {
	l := &Listing{Platform: s.Platform, Width: s.Width, Height: s.Height, App: s.Foreground(), Keyboard: s.Keyboard, Screen: s}
	screenRect := ui.Rect{R: s.Width, B: s.Height}

	idCount := map[string]int{}
	for _, n := range s.Nodes {
		if n.ID != "" {
			idCount[n.ID]++
		}
	}

	listed := map[*ui.Node]*Element{}
	var offscreen []*ui.Node
	for wi, w := range s.Windows {
		if w.Type == "keyboard" {
			l.Keyboard = true
			continue
		}
		if barWindow(w, s) {
			continue
		}
		if w.Type == "system" && wi == 0 && w.Package != "com.android.systemui" && len(s.Windows) > 1 {
			l.Dialog = w.Package
		}
		for _, root := range w.Roots {
			root.Walk(func(n *ui.Node) bool {
				c := n.ShortClass()
				if c == "StatusBar" || c == "Keyboard" || strings.Contains(n.ID, "inputmethod") {
					if c == "Keyboard" || strings.Contains(n.ID, "inputmethod") {
						l.Keyboard = true
					}
					return false
				}
				if n.Parent == nil && len(n.Children) > 0 && n.Label() == "" {
					return true // window root container
				}
				if s.Platform == "ios" {
					if c == "Application" {
						return true // the app's name, over the whole screen: the header has it
					}
					if l.scrollBar(n) {
						return false
					}
				}
				// Off screen, or scrolled out of its list though inside the screen (as the
				// matcher sees it): counted as out of view, not listed.
				if n.Bounds.Intersect(n.ClipRect(screenRect)).Empty() {
					if element(n, s, idCount) != nil {
						offscreen = append(offscreen, n)
					}
					return true
				}
				if s.Platform == "ios" && c == "Other" && n.Label() == "" && n.ID == "" {
					return true
				}
				e := element(n, s, idCount)
				if e == nil {
					return true
				}
				if s.Platform == "ios" && backdrop(e, s) {
					return true
				}
				var pe *Element
				for p := n.Parent; p != nil && pe == nil; p = p.Parent {
					pe = listed[p]
				}
				if pe != nil && repeats(e, pe, s.Platform) {
					return true
				}
				listed[n] = e
				l.Elements = append(l.Elements, e)
				return true
			})
		}
	}
	l.Elements = dropWrappers(l.Elements, listed)
	l.Elements = dropCaptions(l.Elements)
	// Refs once the dropped are gone, so the listed ones are numbered without gaps.
	rank := map[string]int{}
	for _, e := range l.Elements {
		key := e.Kind + "\x00" + e.Label
		if e.ID != "" {
			key = "#" + e.ID
		}
		rank[key]++
		e.key = fmt.Sprintf("%s\x00%d", key, rank[key])
		e.Ref = refs.get(e.key)
	}
	l.mark(s)
	l.buildOutline(s, offscreen)
	// Reading order: top to bottom, then left to right (rows within 8 px count as one).
	sort.SliceStable(l.Elements, func(i, j int) bool {
		a, b := l.Elements[i].Node.Bounds, l.Elements[j].Node.Bounds
		if abs(a.T-b.T) > 8 {
			return a.T < b.T
		}
		return a.L < b.L
	})
	l.slots(idCount)
	return l
}

// repeats: e, inside listed element pe, only repeats it, so it is not listed. An element with
// an id of its own is kept, except an iOS SF Symbol's (the id SwiftUI gives every glyph).
func repeats(e, pe *Element, platform string) bool {
	n := e.Node
	// A glyph in a labelled button or row (a radio's "Circle", a chevron): the button says it.
	if platform == "ios" && e.Kind == "Image" && pe.Tappable && pe.Label != "" && glyphID(e) && !stateGlyph(e, pe) {
		return true
	}
	if e.ID != "" && usefulID(n.ID) {
		return false
	}
	// The control's own inner view, of its kind and in its place (iOS's switch "0" in the
	// switch "Wi-Fi", a progress view's track). One with the same label is the control in a
	// wrapper that repeats it: dropWrappers drops the wrapper instead.
	// Never one with an id of its own (an Android ImageButton "Play" #play centred on its
	// card), nor a second control with a name of its own: only a reading (the switch's "0").
	reading := n.Value != "" && n.Text == n.Value
	if n.ID == "" && e.Kind == pe.Kind && (e.Label != pe.Label || e.Label == "") &&
		!(e.Tappable && pe.Tappable && e.Label != "" && pe.Label != "" && !reading) &&
		pe.Node.Bounds.Contains(n.Bounds.CX(), n.Bounds.CY()) &&
		n.Bounds.Contains(pe.Node.Bounds.CX(), pe.Node.Bounds.CY()) {
		return true
	}
	// A label its nearest listed ancestor already shows.
	if e.Label == "" || !hasLetterOrDigit(e.Label) ||
		strings.Contains(pe.Label, e.Label) || (pe.Label != "" && strings.Contains(e.Label, pe.Label) && e.Kind == "Text") {
		return e.Kind == "Text" || e.Kind == "View" || e.Kind == "Image"
	}
	return false
}

// glyphID: an iOS image's id is an SF Symbol's name ("chevron.forward",
// "largecircle.fill.circle") or its label again ("circle" for "Circle"): the id SwiftUI gives
// every glyph, none of its own. An image with no id, or one shared by several (a repeated
// #avatar, a photo "Sunset"), is content: kept.
func glyphID(e *Element) bool {
	id := e.Node.ID
	return id != "" && (strings.Contains(id, ".") || strings.EqualFold(id, e.Node.Label()))
}

// stateGlyph: the glyph is the only sign of its row's state (a picker's "Checkmark" on the
// chosen row, which iOS does not mark selected): kept unless the row says so itself.
func stateGlyph(e, pe *Element) bool {
	if !strings.Contains(strings.ToLower(e.Node.Label()), "checkmark") {
		return false
	}
	for _, st := range pe.States {
		if st == "selected" || st == "checked" {
			return false
		}
	}
	return true
}

// backdrop: e is an iOS container laid over the whole screen that takes no taps, named only
// by its type (the Toolbar SwiftUI lays over every screen: "Toolbar" #Toolbar): nothing a
// person sees.
func backdrop(e *Element, s *ui.Screen) bool {
	n := e.Node
	if e.Tappable || e.Kind != "View" || n.ID == "" || n.ID != n.Label() {
		return false
	}
	screen := ui.Rect{R: s.Width, B: s.Height}
	return 10*n.Bounds.Intersect(screen).Area() >= 9*screen.Area()
}

// iosScrollBar is an iOS scroll bar's label: "Vertical scroll bar, 2 pages". Known limit: the
// label is in the device's language and the agent sends no trait that marks a scroll bar, so
// on a device in another language the bars are listed as before and give no hint.
var iosScrollBar = regexp.MustCompile(`^(Vertical|Horizontal) scroll bar, (\d+) pages?$`)

// scrollBar: n is an iOS scroll bar, which is not listed: what it says (how many pages, how far
// along) is kept as its scroll view's hint of what lies out of view.
func (l *Listing) scrollBar(n *ui.Node) bool {
	m := iosScrollBar.FindStringSubmatch(n.Desc)
	if m == nil {
		return false
	}
	sv := scrollParent(n)
	pages, _ := strconv.Atoi(m[2])
	pct, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(n.Value), "%"), 64)
	if sv == nil || pages < 2 || err != nil || inAlert(sv) {
		// An alert's own scroll views say "2 pages" for a one-line title.
		return true
	}
	if l.hints == nil {
		l.hints, l.dirs = map[*ui.Node]More{}, map[*ui.Node]string{}
	}
	h := l.hints[sv]
	before, after := &h.Above, &h.Below
	dir := "vertical"
	if m[1] == "Horizontal" {
		before, after, dir = &h.Left, &h.Right, "horizontal"
	}
	if pct < 99.5 {
		*after = -1
	}
	if pct > 0.5 {
		*before = -1
	}
	l.hints[sv] = h
	if d := l.dirs[sv]; d != "" && d != dir {
		dir = "both"
	}
	l.dirs[sv] = dir
	return true
}

func inAlert(n *ui.Node) bool {
	for p := n; p != nil; p = p.Parent {
		if p.Class == "Alert" {
			return true
		}
	}
	return false
}

// scrollDir is the way scroll view n scrolls, when known: from iOS's scroll bars, or an
// Android list's size (one column: vertical; one row: horizontal).
func (l *Listing) scrollDir(n *ui.Node) string {
	if d := l.dirs[n]; d != "" {
		return d
	}
	if c := n.Coll; c != nil {
		switch {
		case c.Cols == 1 && c.Rows > 1:
			return "vertical"
		case c.Rows == 1 && c.Cols > 1:
			return "horizontal"
		}
	}
	return ""
}

// mark notes on each element what the reader cannot see from its line alone: that it is only
// partly in view, or covered (package occlusion's rule, which the runner's taps follow), and
// the way it scrolls.
func (l *Listing) mark(s *ui.Screen) {
	byNode := make(map[*ui.Node]*Element, len(l.Elements))
	for _, e := range l.Elements {
		byNode[e.Node] = e
	}
	for _, e := range l.Elements {
		n := e.Node
		if n.Scrollable {
			e.Scroll = l.scrollDir(n)
			continue // its frame is its view
		}
		v := occlusion.VisiblePart(s, n)
		// Cut on one side (not a region larger than the screen, such as iOS's "dismiss popup").
		b := n.Bounds
		e.Offscreen = !v.Empty() && ((10*v.W() < 9*b.W() && !(b.L < v.L && b.R > v.R)) ||
			(10*v.H() < 9*b.H() && !(b.T < v.T && b.B > v.B)))
		cover, modal := occlusion.Covered(s, n)
		switch {
		case cover == nil:
		case strings.HasPrefix(cover.Class, "Keyboard") || (cover.Window >= 0 && cover.Window < len(s.Windows) && s.Windows[cover.Window].Type == "keyboard"):
			e.Covered = "keyboard"
		case modal:
			e.Covered = "modal"
		default:
			e.Covered = coverName(s, n, v, cover, e, byNode)
		}
	}
}

// coverName names cover, over element e: the listed element at the point it covers (a pinned
// header's cell holds its title; a toolbar holds several buttons), else the first listed one
// it holds, else the listed element it sits in, else "overlay".
func coverName(s *ui.Screen, n *ui.Node, v ui.Rect, cover *ui.Node, e *Element, byNode map[*ui.Node]*Element) string {
	pt := v.Intersect(cover.Bounds)
	if pt.Empty() {
		pt = v
	}
	x, y := pt.CX(), pt.CY()
	var at, first *Element
	cover.Walk(func(d *ui.Node) bool {
		if ce := byNode[d]; ce != nil && ce != e {
			if first == nil {
				first = ce
			}
			if d.Bounds.Contains(x, y) {
				at = ce // deepest last, in pre-order
			}
		}
		return true
	})
	if at == nil {
		at = first
	}
	for c := cover.Parent; c != nil && at == nil; c = c.Parent {
		if ce := byNode[c]; ce != nil && ce != e {
			at = ce
		}
	}
	if at == nil {
		return "overlay"
	}
	return fmt.Sprintf("@%d", at.Ref)
}

// sameThing: g and e, in the same slot, are one element that changed: under a container with
// an id, or with the same first word ("Color: none", "Color: Green"). Without either, a tab
// switch would turn "- Inbox" and "+ Settings" into "~ Settings".
func sameThing(g, e *Element) bool {
	if !strings.HasPrefix(e.slot, "\x00") {
		return true // anchored
	}
	a, b := strings.Fields(g.Label), strings.Fields(e.Label)
	return len(a) > 0 && len(b) > 0 && a[0] == b[0]
}

// slots places each element with no id for the diff: by the nearest container with an id
// on screen, its kind, and its rank among those (in reading order).
func (l *Listing) slots(idCount map[string]int) {
	rank := map[string]int{}
	for _, e := range l.Elements {
		if e.ID != "" {
			continue
		}
		anchor := ""
		for p := e.Node.Parent; p != nil; p = p.Parent {
			if p.ID != "" && idCount[p.ID] == 1 {
				anchor = p.ID
				break
			}
		}
		k := anchor + "\x00" + e.Kind
		e.slot = fmt.Sprintf("%s\x00%d", k, rank[k])
		rank[k]++
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// valueShownAsText: on iOS an element with no title is read with its accessibility value as
// its text (a switch's "0", a quantity stepper's "1", a scroll bar's "0%"), and its label (the
// name a person hears) as its description. Such an element is named by its label; its value is
// shown apart. Fields keep their value as their text: it is what was typed.
func valueShownAsText(n *ui.Node, platform string, field bool) bool {
	return platform == "ios" && !field && !n.Editable && n.Text != "" && n.Text == n.Value && n.Desc != "" && n.Desc != n.Text
}

func element(n *ui.Node, s *ui.Screen, idCount map[string]int) *Element {
	k := kind(n, s.Platform)
	label := n.Label()
	field := k == "Field" || k == "SecureField"
	if field && label == n.Hint {
		label = ""
	}
	value := ""
	if valueShownAsText(n, s.Platform, field) {
		label = n.Desc
		// A switch's or checkbox's value is its checked state, already listed as such.
		if !(n.Checkable || k == "Switch" || k == "CheckBox" || k == "Radio") {
			value = n.Text
		}
	}
	if label == "" && n.Desc != "" {
		label = n.Desc
	}
	// A slider or a progress view with no name reads its position as its text: that is its
	// value (an iOS slider "50%"), not its name.
	if k == "Slider" || k == "Progress" {
		switch {
		case value != "":
		case s.Platform == "web" && n.Value != "":
			// The page reader sends a range input's position as its value.
			value = n.Value
			if label == n.Value {
				label = ""
			}
		case n.Desc == "" && n.Text != "" && (n.Text == n.Value || s.Platform != "ios"):
			label, value = "", n.Text
		case n.State != "":
			value = n.State
		}
	}
	act := actionable(n, s.Platform)
	// An actionable node without a label takes its inner texts (as iOS does for buttons).
	if label == "" && act && !field {
		var parts []string
		for _, c := range n.Children {
			c.Walk(func(d *ui.Node) bool {
				if t := d.Label(); t != "" && len(parts) < 4 {
					parts = append(parts, t)
				}
				return len(parts) < 4
			})
		}
		label = strings.Join(parts, ", ")
	}
	id := ""
	if n.ID != "" && idCount[n.ID] == 1 {
		id = ShortID(n.ID)
	}
	named := id != "" && usefulID(n.ID) && !(s.Platform == "web" && k == "View")
	if label == "" && !field && !act && !named && value == "" {
		return nil
	}
	if !field && !hasLetterOrDigit(label) && !act && !named && value == "" {
		return nil
	}
	e := &Element{Kind: k, Label: clip(label, 80), ID: id, Node: n, Tappable: act,
		Bounds: [4]int{n.Bounds.L, n.Bounds.T, n.Bounds.R, n.Bounds.B}}
	if !named && !(field || act) {
		e.ID = ""
	}
	if field && n.Hint != "" {
		e.Hint = clip(n.Hint, 40)
	}
	e.Value = clip(value, 40)
	if n.Checked {
		e.States = append(e.States, "checked")
	} else if n.Checkable || k == "Switch" || k == "CheckBox" || k == "Radio" {
		e.States = append(e.States, "unchecked")
	}
	if n.Selected {
		e.States = append(e.States, "selected")
	}
	if n.Focused && field {
		e.States = append(e.States, "focused")
	}
	if !n.Enabled && (act || k == "Button" || field) {
		e.States = append(e.States, "disabled")
	}
	if n.Scrollable {
		e.States = append(e.States, "scrollable")
	}
	if n.Error != "" {
		e.States = append(e.States, fmt.Sprintf("error=%q", clip(n.Error, 60)))
	}
	e.contents = e.line(false)
	return e
}

func (e *Element) line(withRef bool) string {
	var b strings.Builder
	if withRef {
		fmt.Fprintf(&b, "@%d ", e.Ref)
	}
	b.WriteString(e.Kind)
	if e.Label != "" {
		fmt.Fprintf(&b, " %q", e.Label)
	}
	if e.ID != "" {
		b.WriteString(" #" + e.ID)
	}
	if e.Hint != "" {
		fmt.Fprintf(&b, " hint=%q", e.Hint)
	}
	if e.Value != "" {
		fmt.Fprintf(&b, " value=%q", e.Value)
	}
	for _, s := range e.States {
		b.WriteString(" " + s)
	}
	if e.Offscreen {
		b.WriteString(" partly off-screen")
	}
	if e.Covered != "" {
		b.WriteString(" covered by " + e.Covered)
	}
	return b.String()
}

// Line is the element's listing line, with its ref.
func (e *Element) Line() string { return e.line(true) }

// Text renders the whole listing.
func (l *Listing) Text() string {
	var b strings.Builder
	app := ""
	if l.App != "" {
		app = " " + l.App
	}
	fmt.Fprintf(&b, "%s %dx%d%s, %d elements\n", l.Platform, l.Width, l.Height, app, len(l.Elements))
	if l.Dialog != "" {
		fmt.Fprintf(&b, "system dialog: %s\n", l.Dialog)
	}
	if !l.writeOutline(&b) {
		for _, e := range l.Elements {
			if e.New {
				b.WriteByte('*')
			}
			b.WriteString(e.Line())
			b.WriteByte('\n')
		}
	}
	if l.Keyboard {
		b.WriteString("Keyboard\n")
	}
	return b.String()
}

// ByRef finds a listed element.
func (l *Listing) ByRef(ref int) *Element {
	for _, e := range l.Elements {
		if e.Ref == ref {
			return e
		}
	}
	return nil
}

// Diff describes what changed from prev to l: "+" new, "~" changed, "-" gone. When most of
// the screen is new it returns the whole listing instead (cheaper to read than a diff), with
// new elements marked "*".
func Diff(prev, l *Listing) string {
	l.MarkNew(prev)
	if prev == nil {
		return "screen now:\n" + l.Text()
	}
	old := map[int]*Element{}
	for _, e := range prev.Elements {
		old[e.Ref] = e
	}
	seen := map[int]bool{}
	for _, e := range l.Elements {
		seen[e.Ref] = true
	}
	// An element with no id is known by its label: one whose text changed (a status line)
	// is new and the old one gone. Where it was (its slot: the container with an id it sits
	// in, its kind and rank there) pairs them up as one changed element.
	gone := map[string]*Element{}
	for _, e := range prev.Elements {
		if !seen[e.Ref] && e.slot != "" {
			gone[e.slot] = e
		}
	}
	var added, changed, removed []string
	newCount := 0
	paired := map[*Element]bool{}
	for _, e := range l.Elements {
		o, ok := old[e.Ref]
		switch {
		case ok && o.contents != e.contents:
			changed = append(changed, "~ "+e.Line())
		case ok:
		default:
			newCount++
			if g := gone[e.slot]; e.slot != "" && g != nil && !paired[g] && sameThing(g, e) {
				paired[g] = true
				changed = append(changed, "~ "+e.Line())
			} else {
				added = append(added, "+ "+e.Line())
			}
		}
	}
	for _, e := range prev.Elements {
		if !seen[e.Ref] && !paired[e] {
			removed = append(removed, "- "+e.Line())
		}
	}
	if newCount > 12 || (len(l.Elements) > 0 && newCount*3 > len(l.Elements)*2) || prev.App != l.App {
		return "screen now:\n" + l.Text()
	}
	changed = append(changed, moreChanges(prev, l)...)
	var b strings.Builder
	for _, s := range added {
		b.WriteString(s + "\n")
	}
	for _, s := range changed {
		b.WriteString(s + "\n")
	}
	for i, s := range removed {
		if i == 8 {
			fmt.Fprintf(&b, "- (%d more)\n", len(removed)-8)
			break
		}
		b.WriteString(s + "\n")
	}
	if prev.Keyboard != l.Keyboard {
		if l.Keyboard {
			b.WriteString("+ Keyboard\n")
		} else {
			b.WriteString("- Keyboard\n")
		}
	}
	if b.Len() == 0 {
		return "screen unchanged\n"
	}
	return b.String()
}

// Similar lists up to 5 elements whose labels share a word of 4+ letters with text.
func (l *Listing) Similar(text string) []*Element {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	var out []*Element
	for _, e := range l.Elements {
		lab := strings.ToLower(e.Label + " " + e.ID)
		for _, w := range words {
			if len([]rune(w)) >= 4 && strings.Contains(lab, w) {
				out = append(out, e)
				break
			}
		}
		if len(out) == 5 {
			break
		}
	}
	return out
}

// dropWrappers removes a container line (a list cell, a view) that only repeats the label of
// the single listed element inside it, as iOS cells do ("General, General" around a
// "General" button). The inner element is the one worth acting on.
func dropWrappers(els []*Element, listed map[*ui.Node]*Element) []*Element {
	kids := map[*Element][]*Element{}
	for _, e := range els {
		for p := e.Node.Parent; p != nil; p = p.Parent {
			if pe, ok := listed[p]; ok {
				kids[pe] = append(kids[pe], e)
				break
			}
		}
	}
	drop := map[*Element]bool{}
	for _, e := range els {
		if e.ID != "" || (e.Kind != "Cell" && e.Kind != "View" && e.Kind != "Button") {
			continue
		}
		k := kids[e]
		if len(k) > 1 && e.Node.Label() == "" && allTappable(k) {
			// Named only by what it holds, all of it listed and tappable (an iOS segmented
			// control "S, M, L" around its segments): the parts are what to act on.
			var parts []string
			for _, c := range k[:min(len(k), 4)] {
				parts = append(parts, c.Label)
			}
			if e.Label == clip(strings.Join(parts, ", "), 80) {
				drop[e] = true
			}
			continue
		}
		if len(k) != 1 || k[0].Label == "" {
			continue
		}
		rest := strings.ReplaceAll(e.Label, k[0].Label, "")
		if strings.Trim(rest, ", ") == "" {
			drop[e] = true
		}
	}
	if len(drop) == 0 {
		return els
	}
	out := els[:0]
	for _, e := range els {
		if !drop[e] {
			out = append(out, e)
		}
	}
	return out
}

func allTappable(els []*Element) bool {
	for _, e := range els {
		if !e.Tappable || e.Label == "" {
			return false
		}
	}
	return true
}

// dropCaptions removes a text with no id beside a control with an id that has the same
// label: the caption SwiftUI draws for a control it names ("Pick a date" beside the date
// picker "Pick a date"). A flow finds the control by its id; the text adds nothing.
func dropCaptions(els []*Element) []*Element {
	byLabel := map[string][]*Element{}
	for _, e := range els {
		if e.ID != "" && e.Label != "" {
			byLabel[e.Label] = append(byLabel[e.Label], e)
		}
	}
	if len(byLabel) == 0 {
		return els
	}
	out := els[:0]
	for _, e := range els {
		if e.Kind == "Text" && e.ID == "" && caption(e, byLabel[e.Label]) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// caption: text e sits in the same row as one of controls.
func caption(e *Element, controls []*Element) bool {
	a := e.Node.Bounds
	for _, c := range controls {
		b := c.Node.Bounds
		if c != e && overlap(a.T, a.B, b.T, b.B)*2 > min(a.H(), b.H()) {
			return true
		}
	}
	return false
}
