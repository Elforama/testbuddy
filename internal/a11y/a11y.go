// Package a11y checks app source code for UI that tb cannot detect or tap.
//
// tb reads apps only through what the platform exposes for accessibility: Android
// accessibility nodes, iOS XCUITest snapshots, and a walk of the web page's DOM. Code that
// hides elements from that tree, or leaves an interactive element with nothing to find it
// by (no text, id or description), makes flows fail or fall back to tapping by position.
// These checks are static (no device) so they can run locally and in CI.
package a11y

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Severity: Error is an element tb cannot see or select; Warning is a likely problem.
type Severity string

const (
	Error   Severity = "error"
	Warning Severity = "warning"
)

// Element is the element a finding is about, in tb look's terms.
type Element struct {
	Kind  string `json:"kind"`            // Button, ImageButton, IconButton, ...
	Label string `json:"label,omitempty"` // its text or description, when the code sets one
	ID    string `json:"id,omitempty"`    // its id / testTag / testID / identifier
}

// Line is the element as tb look would show it: Kind "label" #id.
func (e Element) Line() string {
	var b strings.Builder
	b.WriteString(e.Kind)
	if e.Label != "" {
		fmt.Fprintf(&b, " %q", e.Label)
	}
	if e.ID != "" {
		b.WriteString(" #" + e.ID)
	}
	return b.String()
}

// Finding is one element tb cannot (or likely cannot) detect or tap. Why, and the fix, are
// its rule's (see Rules).
type Finding struct {
	File     string   `json:"file"`
	Line     int      `json:"line"`
	Rule     string   `json:"rule"`
	Severity Severity `json:"severity"`
	Element  Element  `json:"element"`

	fix    *fix   // how --fix adds the missing selector; nil when it cannot
	manual string // why it is left for a person, when the rule's own fix does not say
}

// Rule is one check: what is wrong with the elements it finds, and how to fix them.
type Rule struct {
	ID       string   `json:"-"`
	Severity Severity `json:"severity"`
	Platform string   `json:"platform"`
	Problem  string   `json:"problem"`
	Fix      string   `json:"fix"`
}

// Rules lists every check.
var Rules = []Rule{
	{"android-no-selector", Error, "Android views",
		"image button or clickable image with no id or contentDescription: tb can tap it only by position",
		`add android:id="@+id/…" (tb's id: selector) or android:contentDescription`},
	{"android-no-id", Warning, "Android views",
		"button or field with no id, text or hint in the layout: unless code sets its text, tb can tap it only by position",
		`add android:id="@+id/…"`},
	{"compose-testtag-hidden", Error, "Jetpack Compose",
		"testTag invisible to tb: the project never sets testTagsAsResourceId = true",
		"once, on each screen's root (e.g. in setContent): Modifier.semantics { testTagsAsResourceId = true }"},
	{"compose-no-label", Error, "Jetpack Compose",
		"icon-only button or clickable image with contentDescription = null and no testTag: tb can tap it only by position",
		`Icon(…, contentDescription = "Close"), or Modifier.testTag("…") on the button`},
	{"compose-hidden", Warning, "Jetpack Compose",
		"clearAndSetSemantics {}, invisibleToUser() or hideFromAccessibility() hides it and everything inside from tb",
		"remove it if people interact with anything inside, or set a contentDescription in the block"},
	{"ios-hidden", Warning, "SwiftUI / UIKit",
		"accessibilityHidden(true) or accessibilityElementsHidden = true hides it and everything inside from tb",
		"remove it if people interact with anything inside"},
	{"ios-children-ignored", Warning, "SwiftUI",
		".accessibilityElement(children: .ignore) hides the texts and identifiers inside: tb sees one element with no label",
		`add .accessibilityIdentifier("…") or .accessibilityLabel("…") to the same view, or use children: .combine`},
	{"rn-hidden", Warning, "React Native",
		`importantForAccessibility="no-hide-descendants", accessibilityElementsHidden or aria-hidden hides it and everything inside from tb`,
		"remove it if people interact with anything inside"},
	{"rn-nested-testid", Warning, "React Native",
		"testID inside a touchable: iOS reports the touchable as one element, so tb cannot see this testID on iOS",
		"move the testID to the touchable, or set accessible={false} on the touchable"},
	{"rn-no-selector", Error, "React Native",
		"touchable with no testID, accessibilityLabel or text inside: tb can tap it only by position",
		`add testID="…" (tb's id: selector) or accessibilityLabel="…"`},
	{"web-no-selector", Error, "Web (HTML, JSX, Vue, Svelte)",
		"no text and no id, aria-label, name, title or data-testid (tb skips images and icons, so alt text does not count): tb can tap it only by position",
		`add aria-label="…" (screen readers need it too) or data-testid="…"`},
	{"flutter-hidden", Warning, "Flutter",
		"ExcludeSemantics hides its child from tb",
		"remove it if people interact with anything inside"},
	{"flutter-no-label", Error, "Flutter",
		"IconButton or FloatingActionButton with no tooltip, text or semanticLabel: tb can tap it only by position",
		`add tooltip: "…" (it becomes the button's label)`},
}

// RuleByID returns the rule with this id.
func RuleByID(id string) (Rule, bool) {
	for _, r := range Rules {
		if r.ID == id {
			return r, true
		}
	}
	return Rule{}, false
}

func ruleSeverity(id string) Severity {
	if r, ok := RuleByID(id); ok {
		return r.Severity
	}
	return Warning
}

// Options control a check.
type Options struct {
	// Root is searched for project-wide settings (Compose's testTagsAsResourceId). Empty: the
	// checked paths themselves.
	Root string
	// Lines keeps, for each file in it, only the findings on those lines (1-based): the
	// lines a change added. Files not in it keep all their findings.
	Lines map[string]map[int]bool
}

// Result is a check's outcome.
type Result struct {
	Files    int       `json:"files"`
	Findings []Finding `json:"findings"`
}

// Used lists the rules the findings refer to, in Rules order: the legend.
func (r *Result) Used() []Rule {
	seen := map[string]bool{}
	for _, f := range r.Findings {
		seen[f.Rule] = true
	}
	var out []Rule
	for _, rule := range Rules {
		if seen[rule.ID] {
			out = append(out, rule)
		}
	}
	return out
}

// Errors counts findings of severity Error.
func (r *Result) Errors() int {
	n := 0
	for _, f := range r.Findings {
		if f.Severity == Error {
			n++
		}
	}
	return n
}

// Check checks the given files and folders (folders are walked for source files).
func Check(paths []string, opt Options) (*Result, error) {
	files, err := SourceFiles(paths)
	if err != nil {
		return nil, err
	}
	return CheckFiles(files, paths, opt)
}

// CheckFiles checks exactly these files. roots is where to look for project-wide settings
// when opt.Root is empty.
func CheckFiles(files, roots []string, opt Options) (*Result, error) {
	proj := &project{}
	searchRoots := roots
	if opt.Root != "" {
		searchRoots = []string{opt.Root}
	}
	proj.load(searchRoots, files)
	res := &Result{Findings: []Finding{}}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		chk := checkerFor(f, string(src))
		if chk == nil {
			continue
		}
		res.Files++
		fc := newFile(f, string(src))
		chk(fc, proj)
		lines, only := opt.Lines[f]
		for _, fd := range fc.findings {
			if fc.ignored(fd.Line, fd.Rule) || (only && !lines[fd.Line]) {
				continue
			}
			res.Findings = append(res.Findings, fd)
		}
	}
	sort.SliceStable(res.Findings, func(i, j int) bool {
		a, b := res.Findings[i], res.Findings[j]
		if a.File != b.File {
			return a.File < b.File
		}
		return a.Line < b.Line
	})
	return res, nil
}

type checker func(f *file, p *project)

// checkerFor picks the checks for a file by its extension (and content, for .js/.tsx).
func checkerFor(path, src string) checker {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".xml":
		if isLayoutXML(path, src) {
			return checkAndroidXML
		}
	case ".kt":
		if strings.Contains(src, "androidx.compose") {
			return checkCompose
		}
	case ".swift":
		return checkSwift
	case ".dart":
		return checkFlutter
	case ".js", ".jsx", ".ts", ".tsx", ".mjs":
		if isReactNative(src) {
			return checkReactNative
		}
		if ext != ".ts" && strings.Contains(src, "<") {
			return checkWeb
		}
	case ".html", ".htm", ".vue", ".svelte":
		return checkWeb
	}
	return nil
}

// skipDirs are never walked: build output, dependencies, tooling.
var skipDirs = map[string]bool{
	"node_modules": true, "build": true, "dist": true, "out": true, "Pods": true, "DerivedData": true,
	"vendor": true, "target": true, "__tests__": true, "androidTest": true, "test": true, "tests": true,
	"generated": true, "intermediates": true,
}

var testFile = regexp.MustCompile(`(\.test\.|\.spec\.|Tests?\.(swift|kt|dart)$|_test\.dart$)`)

// SourceFiles expands folders into the files the checks read. Files named directly are
// always kept (unless no check applies to them).
func SourceFiles(paths []string) ([]string, error) {
	return sourceFiles(paths, wantedFile)
}

// sourceFiles expands folders into the files in them that want keeps.
func sourceFiles(paths []string, want func(name string) bool) ([]string, error) {
	var out []string
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if !st.IsDir() {
			out = append(out, p)
			continue
		}
		err = filepath.WalkDir(p, func(q string, e fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if e.IsDir() {
				if q != p && skippedDir(e.Name()) {
					return filepath.SkipDir
				}
				return nil
			}
			if want(e.Name()) {
				out = append(out, q)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func skippedDir(name string) bool {
	return skipDirs[name] || (strings.HasPrefix(name, ".") && name != "." && name != "..")
}

func wantedFile(name string) bool {
	if testFile.MatchString(name) {
		return false
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".xml", ".kt", ".swift", ".dart", ".js", ".jsx", ".ts", ".tsx", ".mjs", ".html", ".htm", ".vue", ".svelte":
		return true
	}
	return false
}

// Wanted reports whether a folder walk would check this file: a source file, not a test,
// and not inside build output, dependencies or a hidden folder.
func Wanted(path string) bool {
	parts := strings.Split(filepath.ToSlash(filepath.Clean(path)), "/")
	for _, d := range parts[:len(parts)-1] {
		if skippedDir(d) {
			return false
		}
	}
	return wantedFile(parts[len(parts)-1])
}

// project holds project-wide facts some checks need.
type project struct {
	testTagsExposed bool // some Kotlin file sets testTagsAsResourceId = true
	// What loops and list callbacks repeat, by name, wherever they are (issue #147): the
	// components of JavaScript, JSX, Vue and Svelte; Compose's composables; Android's row
	// layouts, which adapters inflate.
	jsRows, ktRows, layoutRows rows
	pages                      map[string]jsPage // the checked JavaScript files load read, by path
}

var testTagsAsResourceID = regexp.MustCompile(`testTagsAsResourceId\s*=\s*true`)

// rowFileLimit: a JavaScript file larger than this (a bundle, minified or not) is not
// read for rows unless it is checked: it defines no row a person would fix.
const rowFileLimit = 1 << 20

// load reads the project under roots for what the checks of the files to check need: the
// rows of the kinds of files among them, and, for Compose, testTagsAsResourceId. Kinds none
// of them is are not read: a one-file check reads only what that file's check needs.
func (p *project) load(roots, check []string) {
	p.jsRows, p.ktRows, p.layoutRows = rows{}, rows{}, rows{}
	p.pages = map[string]jsPage{}
	checked := map[string]bool{}
	var needJS, needKt, needLayouts bool
	for _, f := range check {
		checked[absPath(f)] = true
		switch strings.ToLower(filepath.Ext(f)) {
		case ".js", ".jsx", ".tsx", ".mjs", ".vue", ".svelte":
			needJS = true
		case ".kt":
			needKt = true
		case ".xml":
			needLayouts = true
		}
	}
	if !needJS && !needKt && !needLayouts {
		return
	}
	// Java too: its adapters inflate row layouts.
	files, _ := sourceFiles(roots, func(name string) bool {
		return wantedFile(name) || (needLayouts && strings.HasSuffix(name, ".java") && !testFile.MatchString(name))
	})
	var js, kt []rowFile
	for _, f := range files {
		ext := strings.ToLower(filepath.Ext(f))
		switch ext {
		case ".kt":
			if !needKt && !needLayouts {
				continue
			}
		case ".java":
		case ".xml":
			if !needLayouts {
				continue
			}
		case ".js", ".jsx", ".tsx", ".mjs", ".vue", ".svelte":
			if !needJS {
				continue
			}
			if !checked[absPath(f)] {
				if strings.HasSuffix(strings.ToLower(f), ".min.js") {
					continue
				}
				if st, err := os.Stat(f); err != nil || st.Size() > rowFileLimit {
					continue
				}
			}
		default:
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		src := string(b)
		switch ext {
		case ".kt", ".java":
			if needKt && ext == ".kt" {
				if testTagsAsResourceID.MatchString(src) {
					p.testTagsExposed = true
				}
				if strings.Contains(src, "androidx.compose") {
					kt = append(kt, ktRowFile(f, src))
				}
			}
			if needLayouts {
				adapterLayouts(p.layoutRows, f, src)
			}
		case ".xml":
			listItems(p.layoutRows, f, src)
		default:
			if strings.Contains(src, "<") {
				pg := readJSPage(f, src)
				if checked[absPath(f)] {
					pg.src = src
					p.pages[absPath(f)] = pg
				}
				js = append(js, jsRowFile(f, src, pg))
			}
		}
	}
	p.jsRows.spread(js)
	p.ktRows.spread(kt)
}

// absPath is path made absolute, to know a file by whichever path names it.
func absPath(path string) string {
	if a, err := filepath.Abs(path); err == nil {
		return a
	}
	return path
}

// page is the source at path read as a page, as load read it when it was the same source.
func (p *project) page(path, src string) jsPage {
	if pg, ok := p.pages[absPath(path)]; ok && pg.src == src {
		return pg
	}
	return readJSPage(path, src)
}

// file is one source file being checked.
type file struct {
	path     string
	src      string
	starts   []int // offset of each line's start
	lines    []string
	findings []Finding
}

func newFile(path, src string) *file {
	f := &file{path: path, src: src, starts: []int{0}}
	for i := 0; i < len(src); i++ {
		if src[i] == '\n' {
			f.starts = append(f.starts, i+1)
		}
	}
	f.lines = strings.Split(src, "\n")
	return f
}

// line is the 1-based line of a byte offset.
func (f *file) line(off int) int {
	return sort.Search(len(f.starts), func(i int) bool { return f.starts[i] > off })
}

func (f *file) add(off int, rule string, el Element) {
	f.findings = append(f.findings, Finding{File: f.path, Line: f.line(off), Rule: rule,
		Severity: ruleSeverity(rule), Element: el})
}

// addFix adds a finding that fx fixes.
func (f *file) addFix(off int, rule string, el Element, fx *fix) {
	f.add(off, rule, el)
	fx.src = f.src
	f.findings[len(f.findings)-1].fix = fx
}

// addManual adds a finding left for a person, saying why.
func (f *file) addManual(off int, rule string, el Element, why string) {
	f.add(off, rule, el)
	f.findings[len(f.findings)-1].manual = why
}

var ignoreComment = regexp.MustCompile(`tb-a11y-ignore(?::?\s*([\w\-, ]+))?`)

// ignored reports whether a `tb-a11y-ignore` comment on the line or the line before
// covers the rule. Without rule names it covers every rule.
func (f *file) ignored(line int, rule string) bool {
	for _, l := range []int{line, line - 1} {
		if l < 1 || l > len(f.lines) {
			continue
		}
		m := ignoreComment.FindStringSubmatch(f.lines[l-1])
		if m == nil {
			continue
		}
		// The end of a comment (--> in HTML) is not a rule name.
		var names []string
		for _, n := range strings.FieldsFunc(m[1], func(r rune) bool { return r == ',' || r == ' ' }) {
			if strings.ContainsFunc(n, func(r rune) bool { return r >= 'a' && r <= 'z' }) {
				names = append(names, n)
			}
		}
		if len(names) == 0 {
			return true
		}
		for _, n := range names {
			if n == rule {
				return true
			}
		}
	}
	return false
}
