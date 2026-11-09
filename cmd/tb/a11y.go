package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/elforama/testbuddy/internal/a11y"
)

const a11yUsage = `tb a11y: check app source for UI tb cannot detect or tap (no device needed)

  tb a11y [paths...] [options]     paths default to the current folder

Reads Android layouts, Jetpack Compose, SwiftUI/UIKit, React Native, Flutter and web
(HTML, JSX/TSX, Vue, Svelte) code. Prints "ok", or each element tb cannot detect or tap,
in tb look's notation (Kind "label" #id) with the check it fails; each check
that failed is explained once at the end, with its fix:
  ✗  tb cannot detect it, or can tap it only by position (fails the check)
  !  likely a problem (fails only with --fail-on warning)

Options:
  --changed            only lines added since --base (for CI: checks new code only)
  --base REF           what --changed compares with (default: the merge base with
                       origin/main, origin/master, main or master)
  --format F           text (default), json, or github (GitHub Actions annotations)
  --json               same as --format json
  --fail-on LEVEL      error (default), warning, or none
  --fix                add the missing selector to each element that lacks one, in
                       place: android:id, testID (React Native), data-testid (web) or a
                       Compose testTag, named after what the element does (its click
                       handler, icon, link or image: close-button), unique in its file.
                       Then reports what is left: labels people see (Flutter tooltips)
                       and warnings stay for a person. Works with --changed.
  --dry-run            with --fix: say what it would add, change nothing
  --rules              list the checks

Silence one finding with a comment on its line or the line before:
  tb-a11y-ignore                   every check
  tb-a11y-ignore: compose-hidden   just this check (comma-separate several)

Exit status: 0 ok, 1 found elements tb cannot use, 2 bad input.
`

func cmdA11y(argv []string) int {
	a, err := parseArgs(argv,
		flagSpec{[]string{"changed"}, false}, flagSpec{[]string{"base"}, true},
		flagSpec{[]string{"format", "f"}, true}, flagSpec{[]string{"json"}, false},
		flagSpec{[]string{"fail-on"}, true}, flagSpec{[]string{"rules"}, false},
		flagSpec{[]string{"fix"}, false}, flagSpec{[]string{"dry-run"}, false},
		flagSpec{[]string{"help", "h"}, false})
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n\n%s", err, a11yUsage)
		return 2
	}
	if a.flag("help") {
		fmt.Print(a11yUsage)
		return 0
	}
	if a.flag("rules") {
		for _, r := range a11y.Rules {
			fmt.Printf("%s %s (%s): %s. Fix: %s\n", mark(r.Severity), r.ID, r.Platform, r.Problem, r.Fix)
		}
		return 0
	}
	format := a.str("format")
	if a.flag("json") {
		format = "json"
	}
	if format == "" {
		format = "text"
	}
	if format != "text" && format != "json" && format != "github" {
		fmt.Fprintf(os.Stderr, "--format: want text, json or github, not %q\n", format)
		return 2
	}
	fix, dryRun := a.flag("fix"), a.flag("dry-run")
	if dryRun && !fix {
		fmt.Fprintln(os.Stderr, "--dry-run goes with --fix")
		return 2
	}
	if fix && format == "github" {
		fmt.Fprintln(os.Stderr, "--fix reports as text or json, not github annotations")
		return 2
	}
	failOn := a.str("fail-on")
	if failOn == "" {
		failOn = "error"
	}
	if failOn != "error" && failOn != "warning" && failOn != "none" {
		fmt.Fprintf(os.Stderr, "--fail-on: want error, warning or none, not %q\n", failOn)
		return 2
	}
	paths := a.pos
	if len(paths) == 0 {
		paths = []string{"."}
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
	}

	opt := a11y.Options{Root: gitTop()}
	base := ""
	if a.flag("changed") {
		if opt.Root == "" {
			fmt.Fprintln(os.Stderr, "--changed needs a git repository")
			return 2
		}
		if base = a.str("base"); base == "" {
			if base = defaultBase(); base == "" {
				fmt.Fprintln(os.Stderr, "--changed: no origin/main, origin/master, main or master to compare with; pass --base REF")
				return 2
			}
		}
	}
	check := func() (*a11y.Result, error) {
		if base == "" {
			return a11y.Check(paths, opt)
		}
		files, lines, err := changedLines(opt.Root, base, paths)
		if err != nil {
			return nil, err
		}
		o := opt
		o.Lines = lines
		return a11y.CheckFiles(files, paths, o)
	}
	res, err := check()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	var fixed *a11y.FixReport
	if fix {
		if fixed, err = a11y.Fix(res, dryRun); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		// Report what is left now.
		if !dryRun && len(fixed.Fixed) > 0 {
			if res, err = check(); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 2
			}
		}
	}

	switch format {
	case "json":
		out := a11yJSON(res)
		if fixed != nil {
			out.DryRun = fixed.DryRun
			fixedList, left := []a11yFixJSON{}, []a11yFixJSON{}
			for _, f := range fixed.Fixed {
				fixedList = append(fixedList, a11yFixJSON{File: f.File, Line: f.Line, Element: f.Element.Line(), Rule: f.Rule, Added: f.Added})
			}
			for _, l := range fixed.Left {
				left = append(left, a11yFixJSON{File: l.File, Line: l.Line, Element: l.Element.Line(), Rule: l.Rule, Reason: l.Reason})
			}
			out.Fixed, out.Left = &fixedList, &left
		}
		b, _ := json.Marshal(out)
		fmt.Println(string(b))
	case "github":
		for _, f := range res.Findings {
			level := "warning"
			if f.Severity == a11y.Error {
				level = "error"
			}
			r, _ := a11y.RuleByID(f.Rule)
			msg := fmt.Sprintf("%s: %s. Fix: %s", f.Element.Line(), r.Problem, r.Fix)
			fmt.Printf("::%s file=%s,line=%d,title=tb a11y (%s)::%s\n", level, ghEscape(f.File, true), f.Line, f.Rule, ghEscape(msg, false))
		}
		fmt.Println(a11ySummary(res))
	default:
		if fixed != nil {
			fmt.Print(a11yFixText(fixed))
		}
		fmt.Print(a11yText(res))
	}

	errs, warns := res.Errors(), len(res.Findings)-res.Errors()
	if (failOn == "error" && errs > 0) || (failOn == "warning" && errs+warns > 0) {
		return 1
	}
	return 0
}

// a11yText lists findings per file, one element per line in tb look's notation with the
// rule it breaks, then a legend explaining each rule used once, then a summary; or just "ok".
//
//	src/Login.tsx
//	  :6 Pressable ✗ rn-no-selector
//
//	✗ rn-no-selector: touchable with no testID, …. Fix: add testID="…" …
func a11yText(res *a11y.Result) string {
	var b strings.Builder
	last := ""
	for _, f := range res.Findings {
		if f.File != last {
			b.WriteString(f.File + "\n")
			last = f.File
		}
		fmt.Fprintf(&b, "  :%d %s %s %s\n", f.Line, f.Element.Line(), mark(f.Severity), f.Rule)
	}
	if len(res.Findings) > 0 {
		b.WriteByte('\n')
		for _, r := range res.Used() {
			fmt.Fprintf(&b, "%s %s: %s. Fix: %s\n", mark(r.Severity), r.ID, r.Problem, r.Fix)
		}
	}
	b.WriteString(a11ySummary(res) + "\n")
	return b.String()
}

// a11yJSON is the text output's shape as JSON: findings grouped by file, each element in
// tb look's notation, and each rule used explained once.
//
//	{"files":2,"errors":1,"warnings":0,
//	 "findings":{"src/Login.tsx":[{"line":6,"element":"Pressable","rule":"rn-no-selector"}]},
//	 "rules":{"rn-no-selector":{"severity":"error","platform":"React Native","problem":"…","fix":"…"}}}
func a11yJSON(res *a11y.Result) *a11yReport {
	out := &a11yReport{Files: res.Files, Errors: res.Errors(), Warnings: len(res.Findings) - res.Errors(),
		Findings: map[string][]a11yItem{}, Rules: map[string]a11y.Rule{}}
	for _, f := range res.Findings {
		out.Findings[f.File] = append(out.Findings[f.File], a11yItem{f.Line, f.Element.Line(), f.Rule})
	}
	for _, r := range res.Used() {
		out.Rules[r.ID] = r
	}
	return out
}

type a11yItem struct {
	Line    int    `json:"line"`
	Element string `json:"element"`
	Rule    string `json:"rule"`
}

// a11yReport is tb a11y's JSON. With --fix it also lists what was fixed (as the elements are
// after the fix) and what was left for a person, with why.
type a11yReport struct {
	Files    int                   `json:"files"`
	Errors   int                   `json:"errors"`
	Warnings int                   `json:"warnings"`
	Findings map[string][]a11yItem `json:"findings"`
	Rules    map[string]a11y.Rule  `json:"rules"`
	DryRun   bool                  `json:"dry_run,omitempty"`
	Fixed    *[]a11yFixJSON        `json:"fixed,omitempty"` // with --fix, even when empty
	Left     *[]a11yFixJSON        `json:"left,omitempty"`
}

type a11yFixJSON struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Element string `json:"element"`
	Rule    string `json:"rule"`
	Added   string `json:"added,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// a11yFixText is a line per element --fix gave a selector (or would, in a dry run), and a
// line per element it left for a person, with why; then a blank line.
//
//	fixed app/res/layout/home.xml:3 ImageButton: added android:id="@+id/close_button"
//	left lib/home.dart:3 IconButton (flutter-no-label): its label is a tooltip people see, …
func a11yFixText(rep *a11y.FixReport) string {
	var b strings.Builder
	for _, f := range rep.Fixed {
		if rep.DryRun {
			fmt.Fprintf(&b, "would fix %s:%d %s: add %s\n", f.File, f.Line, f.Element.Line(), f.Added)
		} else {
			fmt.Fprintf(&b, "fixed %s:%d %s: added %s\n", f.File, f.Line, f.Element.Line(), f.Added)
		}
	}
	for _, l := range rep.Left {
		fmt.Fprintf(&b, "left %s:%d %s (%s): %s\n", l.File, l.Line, l.Element.Line(), l.Rule, l.Reason)
	}
	if len(rep.Fixed) == 0 && len(rep.Left) == 0 {
		b.WriteString("nothing to fix\n")
	}
	b.WriteByte('\n')
	return b.String()
}

func mark(s a11y.Severity) string {
	if s == a11y.Error {
		return "✗"
	}
	return "!"
}

func a11ySummary(res *a11y.Result) string {
	errs, warns := res.Errors(), len(res.Findings)-res.Errors()
	files := plural(res.Files, "file")
	if errs+warns == 0 {
		return fmt.Sprintf("ok: %s checked, every element tb can detect and tap", files)
	}
	var parts []string
	if errs > 0 {
		parts = append(parts, fmt.Sprintf("✗ %s tb cannot detect or tap", plural(errs, "element")))
	}
	if warns > 0 {
		parts = append(parts, fmt.Sprintf("! %d to check", warns))
	}
	return fmt.Sprintf("%s (%s checked)", strings.Join(parts, ", "), files)
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return strconv.Itoa(n) + " " + word + "s"
}

// ghEscape escapes a GitHub Actions workflow command value (properties also escape , and :).
func ghEscape(s string, property bool) string {
	s = strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A").Replace(s)
	if property {
		s = strings.NewReplacer(":", "%3A", ",", "%2C").Replace(s)
	}
	return s
}

func git(args ...string) (string, error) {
	var out, errb bytes.Buffer
	c := exec.Command("git", args...)
	c.Stdout, c.Stderr = &out, &errb
	if err := c.Run(); err != nil {
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

// gitTop is the repository's top folder, or "" outside a repository.
func gitTop() string {
	out, err := git("rev-parse", "--show-toplevel")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// defaultBase is where the current branch left the main branch.
func defaultBase() string {
	for _, ref := range []string{"origin/HEAD", "origin/main", "origin/master", "main", "master"} {
		if out, err := git("merge-base", "HEAD", ref); err == nil {
			return strings.TrimSpace(out)
		}
	}
	return ""
}

var hunkHeader = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@`)

// changedLines lists the files under paths that differ from base (committed, staged,
// unstaged or untracked), with the lines each one added. Untracked files are new
// throughout, so they have no line filter. Paths are relative to the current folder.
func changedLines(top, base string, paths []string) ([]string, map[string]map[int]bool, error) {
	cwd, _ := os.Getwd()
	rel := func(p string) string {
		abs := filepath.Join(top, p)
		if r, err := filepath.Rel(cwd, abs); err == nil {
			return r
		}
		return abs
	}
	under := func(p string) bool {
		for _, root := range paths {
			r, err := filepath.Rel(root, p)
			if err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator)) {
				return true
			}
		}
		return false
	}
	diff, err := git("-C", top, "diff", "-U0", "--no-color", "--no-ext-diff", "--diff-filter=ACMR", base, "--")
	if err != nil {
		return nil, nil, err
	}
	lines := map[string]map[int]bool{}
	var files []string
	cur := ""
	sc := bufio.NewScanner(strings.NewReader(diff))
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		l := sc.Text()
		if strings.HasPrefix(l, "+++ ") {
			cur = ""
			if p, ok := strings.CutPrefix(l, "+++ b/"); ok {
				if r := rel(p); under(r) {
					cur = r
					files = append(files, r)
					lines[r] = map[int]bool{}
				}
			}
			continue
		}
		if m := hunkHeader.FindStringSubmatch(l); m != nil && cur != "" {
			start, _ := strconv.Atoi(m[1])
			n := 1
			if m[2] != "" {
				n, _ = strconv.Atoi(m[2])
			}
			for i := start; i < start+n; i++ {
				lines[cur][i] = true
			}
		}
	}
	untracked, err := git("-C", top, "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return nil, nil, err
	}
	for _, p := range strings.Split(strings.TrimSpace(untracked), "\n") {
		if p == "" {
			continue
		}
		if r := rel(p); under(r) {
			files = append(files, r)
		}
	}
	// Keep only files a folder walk would check (no build output, tests or dependencies).
	var kept []string
	for _, f := range files {
		if a11y.Wanted(f) {
			kept = append(kept, f)
		}
	}
	return kept, lines, nil
}
