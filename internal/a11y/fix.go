package a11y

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

// fix is how a finding can be fixed in its source: by inserting a selector (an id, testID,
// data-testid or testTag) named after what the element does.
type fix struct {
	src    string                   // the file's source when it was checked
	off    int                      // where the text goes
	base   string                   // what the element does ("close"), in any case; "" when nothing says
	role   string                   // what it is: button, field, link, ...
	sep    string                   // "_" for Android resource names, "-" otherwise
	insert func(name string) string // the text inserted at off
	added  func(name string) string // the selector added, as the report shows it
	imprt  string                   // an import the inserted code needs (Compose's testTag)
}

// FixReport is what Fix changed (or, in a dry run, would change), and what it left for a person.
type FixReport struct {
	DryRun bool
	Fixed  []Fixed
	Left   []Left
}

// Fixed is one element given a selector. Line is where it is after the fix.
type Fixed struct {
	File    string
	Line    int
	Rule    string
	Element Element
	Added   string // the selector added: android:id="@+id/close_button", testID="close-button", ...
}

// Left is a finding Fix does not change, and why.
type Left struct {
	File    string
	Line    int
	Rule    string
	Element Element
	Reason  string
}

// Fix adds the missing selector to every finding of res that can be fixed in its source, and
// writes the files (unless dryRun). Names come from what the element does (its handler, its
// icon, its link, its image) and are unique in the file. Other findings are left, with why.
// Nothing is written when a file changed since it was checked.
func Fix(res *Result, dryRun bool) (*FixReport, error) {
	rep := &FixReport{DryRun: dryRun}
	type change struct {
		path string
		src  string
	}
	var changes []change
	byFile := map[string][]Finding{}
	var order []string
	for _, f := range res.Findings {
		if _, ok := byFile[f.File]; !ok {
			order = append(order, f.File)
		}
		byFile[f.File] = append(byFile[f.File], f)
	}
	for _, path := range order {
		var fixes []Finding
		for _, f := range byFile[path] {
			if f.fix == nil {
				rep.Left = append(rep.Left, Left{f.File, f.Line, f.Rule, f.Element, leftReason(f)})
				continue
			}
			fixes = append(fixes, f)
		}
		if len(fixes) == 0 {
			continue
		}
		src := fixes[0].fix.src
		now, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if string(now) != src {
			return nil, fmt.Errorf("%s changed since it was checked: run tb a11y --fix again", path)
		}
		taken := takenNames(src)
		type edit struct {
			off  int
			text string
		}
		var edits []edit
		imprt := ""
		first := len(rep.Fixed)
		for _, f := range fixes {
			fx := f.fix
			name := uniqueName(fx, taken)
			edits = append(edits, edit{fx.off, fx.insert(name)})
			if fx.imprt != "" {
				imprt = fx.imprt
			}
			rep.Fixed = append(rep.Fixed, Fixed{f.File, f.Line, f.Rule, f.Element, fx.added(name)})
		}
		if imprt != "" && !imported(src, imprt) {
			off, text := importAt(src, imprt)
			edits = append(edits, edit{off, text})
			for k, f := range fixes {
				if f.fix.off >= off {
					rep.Fixed[first+k].Line++
				}
			}
		}
		sort.SliceStable(edits, func(i, j int) bool { return edits[i].off > edits[j].off })
		out := src
		for _, e := range edits {
			out = out[:e.off] + e.text + out[e.off:]
		}
		changes = append(changes, change{path, out})
	}
	if dryRun {
		return rep, nil
	}
	for _, c := range changes {
		mode := os.FileMode(0o644)
		if st, err := os.Stat(c.path); err == nil {
			mode = st.Mode().Perm()
		}
		if err := os.WriteFile(c.path, []byte(c.src), mode); err != nil {
			return nil, err
		}
	}
	return rep, nil
}

// leftReason says why a finding has no automatic fix.
func leftReason(f Finding) string {
	if f.manual != "" {
		return f.manual
	}
	if f.Severity == Warning {
		return "a likely problem: whether it needs a change is for a person to judge"
	}
	r, _ := RuleByID(f.Rule)
	return "fix by hand: " + r.Fix
}

var selectorName = regexp.MustCompile(`(?i)\b(?:id|testid|data-testid|data-test|data-cy|testtag|accessibilityidentifier)` +
	`\s*[=(:]\s*\{?\s*["']([\w\-]+)["']|@\+?id/(\w+)`)

// takenNames are the ids in the source (ids, testIDs, data-testids, testTags): a new name
// must not reuse one.
func takenNames(src string) map[string]bool {
	taken := map[string]bool{}
	for _, m := range selectorName.FindAllStringSubmatch(src, -1) {
		taken[m[1]+m[2]] = true
	}
	return taken
}

// uniqueName is the fix's name with its role, made unique in the file (close-button,
// close-button-2, ...), and marks it taken.
func uniqueName(fx *fix, taken map[string]bool) string {
	norm := kebab
	if fx.sep == "_" {
		norm = snake
	}
	name := withRole(norm(fx.base), fx.role, fx.sep)
	try := name
	for i := 2; taken[try]; i++ {
		try = fmt.Sprintf("%s%s%d", name, fx.sep, i)
	}
	taken[try] = true
	return try
}

// withRole ends a name with its role: close + button = close-button.
func withRole(base, role, sep string) string {
	switch {
	case base == "":
		return role
	case base == role || strings.HasSuffix(base, sep+role):
		return base
	}
	return base + sep + role
}

// imported reports whether a Kotlin file imports name (directly or with its package's *).
func imported(src, name string) bool {
	pkg := name[:strings.LastIndexByte(name, '.')]
	re := regexp.MustCompile(`(?m)^\s*import\s+(` + regexp.QuoteMeta(name) + `|` + regexp.QuoteMeta(pkg) + `\.\*)\s*$`)
	return re.MatchString(src)
}

var importLine = regexp.MustCompile(`(?m)^\s*(import|package)\s.*$`)

// importAt is where an import line goes (after the last import, else the package line, else
// at the top) and the text to insert there.
func importAt(src, name string) (int, string) {
	line := "import " + name
	ms := importLine.FindAllStringIndex(src, -1)
	if len(ms) == 0 {
		return 0, line + "\n"
	}
	end := ms[len(ms)-1][1]
	if end < len(src) && src[end] == '\n' {
		return end + 1, line + "\n"
	}
	return end, "\n" + line
}

var (
	callName  = regexp.MustCompile(`([A-Za-z_$][\w$]*(?:\s*\.\s*[A-Za-z_$][\w$]*)*)\s*\(`)
	identPath = regexp.MustCompile(`^[A-Za-z_$][\w$]*(?:\.[A-Za-z_$][\w$]*)*$`)
	// jsKeywords are never a handler's name.
	jsKeywords = map[string]bool{"function": true, "if": true, "return": true, "async": true, "await": true,
		"new": true, "typeof": true, "void": true, "while": true, "for": true, "switch": true, "catch": true}
	// genericNames say nothing about what an element does.
	genericNames = map[string]bool{"press": true, "click": true, "tap": true, "pressed": true, "clicked": true,
		"tapped": true, "longpress": true, "change": true, "event": true, "handler": true, "callback": true,
		"noop": true, "fn": true, "it": true, "true": true, "false": true, "null": true, "undefined": true}
)

// handlerName is what a click handler says the element does: `{handleClose}`,
// `{() => onDelete(id)}`, `closeDialog()`, `{ viewModel.save() }` give close, delete,
// closeDialog and save. "" when it says nothing.
func handlerName(v string) string {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "{") && strings.HasSuffix(v, "}") {
		v = strings.TrimSpace(v[1 : len(v)-1])
	}
	if i := strings.Index(v, "=>"); i >= 0 {
		v = strings.TrimSpace(v[i+2:])
	}
	name := ""
	if identPath.MatchString(v) {
		name = v
	} else {
		for _, m := range callName.FindAllStringSubmatch(v, -1) {
			if !jsKeywords[m[1]] {
				name = m[1]
				break
			}
		}
	}
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		name = strings.TrimSpace(name[i+1:])
	}
	for _, p := range []string{"handle", "on"} {
		if len(name) > len(p) && strings.HasPrefix(name, p) && name[len(p)] >= 'A' && name[len(p)] <= 'Z' {
			name = name[len(p):]
			break
		}
	}
	if len(name) < 2 || genericNames[strings.ToLower(name)] {
		return ""
	}
	return strings.ToLower(name[:1]) + name[1:]
}

// kebab turns any name into lower-case words joined by "-": openSettings, ic_close and
// HTTPServer give open-settings, ic-close and http-server. Letters outside ASCII are dropped.
func kebab(s string) string {
	var words []string
	var cur []byte
	flush := func() {
		if len(cur) > 0 {
			words = append(words, strings.ToLower(string(cur)))
			cur = nil
		}
	}
	upper := func(c byte) bool { return c >= 'A' && c <= 'Z' }
	lower := func(c byte) bool { return c >= 'a' && c <= 'z' || c >= '0' && c <= '9' }
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !upper(c) && !lower(c) {
			flush()
			continue
		}
		if upper(c) && len(cur) > 0 {
			prev := cur[len(cur)-1]
			// fooBar, and the last capital of HTTPServer
			if lower(prev) || (i+1 < len(s) && s[i+1] >= 'a' && s[i+1] <= 'z' && upper(prev)) {
				flush()
			}
		}
		cur = append(cur, c)
	}
	flush()
	return strings.Join(words, "-")
}

// snake is kebab with "_": an Android resource name.
func snake(s string) string { return strings.ReplaceAll(kebab(s), "-", "_") }

// resourceName is an image resource's name without its kind prefix: @drawable/ic_close and
// R.drawable.ic_close give close.
func resourceName(v string) string {
	v = strings.TrimSpace(v)
	if i := strings.LastIndexAny(v, "/."); i >= 0 {
		v = v[i+1:]
	}
	for _, p := range []string{"ic_", "btn_", "img_", "icon_", "image_"} {
		if strings.HasPrefix(v, p) && len(v) > len(p) {
			return v[len(p):]
		}
	}
	return v
}

// hrefName is the last part of a link's path: /settings/ gives settings, #top gives top.
func hrefName(v string) string {
	v = strings.TrimSpace(v)
	if strings.ContainsAny(v, "{}$") {
		return ""
	}
	if strings.HasPrefix(v, "#") {
		return v[1:]
	}
	if i := strings.IndexAny(v, "?#"); i >= 0 {
		v = v[:i]
	}
	v = strings.TrimRight(v, "/")
	if i := strings.LastIndexByte(v, '/'); i >= 0 {
		v = v[i+1:]
	} else if strings.Contains(v, ":") {
		return ""
	}
	if i := strings.LastIndexByte(v, '.'); i > 0 {
		v = v[:i]
	}
	return v
}

// iconName is the name of an icon inside t: <Icon name="close"/> gives close, <CloseIcon/> Close.
func iconName(t *tag) string {
	found := ""
	t.walk(func(d *tag) bool {
		if found != "" {
			return false
		}
		if d == t || d.name == "" || isLower(d.name[0]) {
			return true
		}
		if n := tagString(d, "name"); n != "" {
			found = n
			return false
		}
		switch n := d.name; {
		case strings.HasSuffix(n, "Icon") && len(n) > 4:
			found = n[:len(n)-4]
		case strings.HasPrefix(n, "Icon") && len(n) > 4 && n[4] >= 'A' && n[4] <= 'Z':
			found = n[4:]
		}
		return found == ""
	})
	return found
}

// tagNameEnd is the offset just past the name of the tag whose '<' is at off.
func tagNameEnd(src string, off int) int {
	i := off + 1
	for i < len(src) && isNameChar(src[i]) {
		i++
	}
	return i
}

// attrFix is a fix that inserts ` attr="NAME"` (with format giving the value) after a tag's name.
func attrFix(src string, off int, attr, format, base, role, sep string) *fix {
	value := func(name string) string { return attr + `="` + fmt.Sprintf(format, name) + `"` }
	return &fix{off: tagNameEnd(src, off), base: base, role: role, sep: sep,
		insert: func(name string) string { return " " + value(name) },
		added:  value}
}
