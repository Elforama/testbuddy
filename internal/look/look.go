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
//     text in a child Text), as are icon-glyph-only labels;
//   - ids are shown only when unique on screen.
package look

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/elforama/testbuddy/internal/ui"
)

// Element is one listed line.
type Element struct {
	Ref      int      `json:"ref"`
	Kind     string   `json:"kind"`
	Label    string   `json:"label,omitempty"`
	ID       string   `json:"id,omitempty"`
	Hint     string   `json:"hint,omitempty"`
	Value    string   `json:"value,omitempty"`
	States   []string `json:"states,omitempty"`
	New      bool     `json:"new,omitempty"` // not on the screen shown before (see MarkNew)
	Bounds   [4]int   `json:"bounds"`
	Node     *ui.Node `json:"-"`
	key      string
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
		}
		return "Field"
	case "textarea":
		return "Field"
	case "select":
		return "Select"
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
	rank := map[string]int{}
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
				if n.Bounds.Intersect(screenRect).Empty() {
					if element(n, s, idCount) != nil {
						offscreen = append(offscreen, n)
					}
					return true
				}
				if s.Platform == "ios" && n.ShortClass() == "Other" && n.Label() == "" && n.ID == "" {
					return true
				}
				e := element(n, s, idCount)
				if e == nil {
					return true
				}
				// Drop a label its nearest listed ancestor already shows.
				if !(e.ID != "" && usefulID(n.ID)) {
					for p := n.Parent; p != nil; p = p.Parent {
						if pe, ok := listed[p]; ok {
							if e.Label == "" || !hasLetterOrDigit(e.Label) ||
								strings.Contains(pe.Label, e.Label) || (pe.Label != "" && strings.Contains(e.Label, pe.Label) && e.Kind == "Text") {
								if e.Kind == "Text" || e.Kind == "View" || e.Kind == "Image" {
									return true
								}
							}
							break
						}
					}
				}
				key := e.Kind + "\x00" + e.Label
				if e.ID != "" {
					key = "#" + e.ID
				}
				rank[key]++
				e.key = fmt.Sprintf("%s\x00%d", key, rank[key])
				e.Ref = refs.get(e.key)
				listed[n] = e
				l.Elements = append(l.Elements, e)
				return true
			})
		}
	}
	l.Elements = dropWrappers(l.Elements, listed)
	l.buildOutline(s, offscreen)
	// Reading order: top to bottom, then left to right (rows within 8 px count as one).
	sort.SliceStable(l.Elements, func(i, j int) bool {
		a, b := l.Elements[i].Node.Bounds, l.Elements[j].Node.Bounds
		if abs(a.T-b.T) > 8 {
			return a.T < b.T
		}
		return a.L < b.L
	})
	return l
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func element(n *ui.Node, s *ui.Screen, idCount map[string]int) *Element {
	k := kind(n, s.Platform)
	label := n.Label()
	field := k == "Field" || k == "SecureField"
	if field && label == n.Hint {
		label = ""
	}
	if label == "" && n.Desc != "" {
		label = n.Desc
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
	if label == "" && !field && !act && !named {
		return nil
	}
	if !field && !hasLetterOrDigit(label) && !act && !named {
		return nil
	}
	e := &Element{Kind: k, Label: clip(label, 80), ID: id, Node: n,
		Bounds: [4]int{n.Bounds.L, n.Bounds.T, n.Bounds.R, n.Bounds.B}}
	if !named && !(field || act) {
		e.ID = ""
	}
	if field && n.Hint != "" {
		e.Hint = clip(n.Hint, 40)
	}
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
	for _, s := range e.States {
		b.WriteString(" " + s)
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
	var added, changed, removed []string
	seen := map[int]bool{}
	for _, e := range l.Elements {
		seen[e.Ref] = true
		if o, ok := old[e.Ref]; !ok {
			added = append(added, "+ "+e.Line())
		} else if o.contents != e.contents {
			changed = append(changed, "~ "+e.Line())
		}
	}
	for _, e := range prev.Elements {
		if !seen[e.Ref] {
			removed = append(removed, "- "+e.Line())
		}
	}
	if len(added) > 12 || (len(l.Elements) > 0 && len(added)*3 > len(l.Elements)*2) || prev.App != l.App {
		return "screen now:\n" + l.Text()
	}
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
