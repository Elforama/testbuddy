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

type Severity string

const (
	Error   Severity = "error"
	Warning Severity = "warning"
)

type Element struct {
	Kind  string `json:"kind"`
	Label string `json:"label,omitempty"`
	ID    string `json:"id,omitempty"`
}

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

type Finding struct {
	File     string   `json:"file"`
	Line     int      `json:"line"`
	Rule     string   `json:"rule"`
	Severity Severity `json:"severity"`
	Element  Element  `json:"element"`

	fix    *fix
	manual string
}

type Rule struct {
	ID       string   `json:"-"`
	Severity Severity `json:"severity"`
	Platform string   `json:"platform"`
	Problem  string   `json:"problem"`
	Fix      string   `json:"fix"`
}

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

type Options struct {
	Root  string
	Lines map[string]map[int]bool
}

type Result struct {
	Files    int       `json:"files"`
	Findings []Finding `json:"findings"`
}

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

func (r *Result) Errors() int {
	n := 0
	for _, f := range r.Findings {
		if f.Severity == Error {
			n++
		}
	}
	return n
}

func Check(paths []string, opt Options) (*Result, error) {
	files, err := SourceFiles(paths)
	if err != nil {
		return nil, err
	}
	return CheckFiles(files, paths, opt)
}

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

var skipDirs = map[string]bool{
	"node_modules": true, "build": true, "dist": true, "out": true, "Pods": true, "DerivedData": true,
	"vendor": true, "target": true, "__tests__": true, "androidTest": true, "test": true, "tests": true,
	"generated": true, "intermediates": true,
}

var testFile = regexp.MustCompile(`(\.test\.|\.spec\.|Tests?\.(swift|kt|dart)$|_test\.dart$)`)

func SourceFiles(paths []string) ([]string, error) {
	return sourceFiles(paths, wantedFile)
}

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

func Wanted(path string) bool {
	parts := strings.Split(filepath.ToSlash(filepath.Clean(path)), "/")
	for _, d := range parts[:len(parts)-1] {
		if skippedDir(d) {
			return false
		}
	}
	return wantedFile(parts[len(parts)-1])
}

type project struct {
	testTagsExposed            bool
	jsRows, ktRows, layoutRows rows
	pages                      map[string]jsPage
}

var testTagsAsResourceID = regexp.MustCompile(`testTagsAsResourceId\s*=\s*true`)

const rowFileLimit = 1 << 20

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

func absPath(path string) string {
	if a, err := filepath.Abs(path); err == nil {
		return a
	}
	return path
}

func (p *project) page(path, src string) jsPage {
	if pg, ok := p.pages[absPath(path)]; ok && pg.src == src {
		return pg
	}
	return readJSPage(path, src)
}

type file struct {
	path     string
	src      string
	starts   []int
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

func (f *file) line(off int) int {
	return sort.Search(len(f.starts), func(i int) bool { return f.starts[i] > off })
}

func (f *file) add(off int, rule string, el Element) {
	f.findings = append(f.findings, Finding{File: f.path, Line: f.line(off), Rule: rule,
		Severity: ruleSeverity(rule), Element: el})
}

func (f *file) addFix(off int, rule string, el Element, fx *fix) {
	f.add(off, rule, el)
	fx.src = f.src
	f.findings[len(f.findings)-1].fix = fx
}

func (f *file) addManual(off int, rule string, el Element, why string) {
	f.add(off, rule, el)
	f.findings[len(f.findings)-1].manual = why
}

var ignoreComment = regexp.MustCompile(`tb-a11y-ignore(?::?\s*([\w\-, ]+))?`)

func (f *file) ignored(line int, rule string) bool {
	for _, l := range []int{line, line - 1} {
		if l < 1 || l > len(f.lines) {
			continue
		}
		m := ignoreComment.FindStringSubmatch(f.lines[l-1])
		if m == nil {
			continue
		}
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
