package report

import (
	"encoding/json"
	"encoding/xml"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 2, 9, 30, 15, 0, time.UTC)

// parsed mirrors the JUnit shape for decoding the output back.
type pProp struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}
type pProps struct {
	List []pProp `xml:"property"`
}
type pCase struct {
	ID        string  `xml:"id,attr"`
	Name      string  `xml:"name,attr"`
	Classname string  `xml:"classname,attr"`
	File      *string `xml:"file,attr"`
	Time      string  `xml:"time,attr"`
	Timestamp string  `xml:"timestamp,attr"`
	Status    string  `xml:"status,attr"`
	Props     *pProps `xml:"properties"`
	Failure   *string `xml:"failure"`
}

func (c pCase) properties() []pProp {
	if c.Props == nil {
		return nil
	}
	return c.Props.List
}

type pSuite struct {
	Name      string  `xml:"name,attr"`
	Device    *string `xml:"device,attr"`
	Tests     int     `xml:"tests,attr"`
	Failures  int     `xml:"failures,attr"`
	Time      string  `xml:"time,attr"`
	Timestamp string  `xml:"timestamp,attr"`
	Cases     []pCase `xml:"testcase"`
}
type pSuites struct {
	XMLName xml.Name `xml:"testsuites"`
	Suites  []pSuite `xml:"testsuite"`
}

func junit(t *testing.T, suites []Suite) (string, pSuites) {
	t.Helper()
	b, err := JUnit(suites)
	if err != nil {
		t.Fatalf("JUnit: %v", err)
	}
	var doc pSuites
	if err := xml.Unmarshal(b, &doc); err != nil {
		t.Fatalf("output is not valid XML: %v\n%s", err, b)
	}
	return string(b), doc
}

// The document starts with the XML declaration and ends with a newline.
func TestJUnitEnvelope(t *testing.T) {
	out, doc := junit(t, nil)
	if !strings.HasPrefix(out, "<?xml version='1.0' encoding='UTF-8'?>\n<testsuites>") {
		t.Errorf("prefix: %q", out)
	}
	if !strings.HasSuffix(out, "</testsuites>\n") {
		t.Errorf("suffix: %q", out)
	}
	if len(doc.Suites) != 0 {
		t.Errorf("suites = %d", len(doc.Suites))
	}
}

// A suite carries its name, device, test and failure counts, total time and start timestamp.
func TestJUnitSuiteAttributes(t *testing.T) {
	s := Suite{Name: "Test Suite", Device: "Pixel 9, iPhone 17", Start: t0, Flows: []Flow{
		{Name: "a", Status: "SUCCESS", Duration: 1500 * time.Millisecond, Start: t0},
		{Name: "b", Status: "ERROR", Error: "boom", Duration: 250 * time.Millisecond, Start: t0},
		{Name: "c", Status: "WARNING", Duration: 1234567 * time.Microsecond, Start: t0},
		{Name: "d", Status: "SKIPPED", Start: t0},
		{Name: "e", Status: "ERROR", Duration: time.Millisecond, Start: t0},
	}}
	_, doc := junit(t, []Suite{s})
	if len(doc.Suites) != 1 {
		t.Fatalf("suites = %d", len(doc.Suites))
	}
	got := doc.Suites[0]
	if got.Name != "Test Suite" || got.Device == nil || *got.Device != "Pixel 9, iPhone 17" {
		t.Errorf("name/device = %q/%v", got.Name, got.Device)
	}
	if got.Tests != 5 {
		t.Errorf("tests = %d", got.Tests)
	}
	if got.Failures != 2 {
		t.Errorf("failures = %d, want 2 (only ERROR counts; WARNING and SKIPPED do not)", got.Failures)
	}
	// 1.5 + 0.25 + 1.234567 + 0 + 0.001
	if got.Time != "2.986" {
		t.Errorf("time = %q", got.Time)
	}
	if got.Timestamp != "2026-11-02T09:30:15" {
		t.Errorf("timestamp = %q", got.Timestamp)
	}
	if len(got.Cases) != 5 {
		t.Fatalf("cases = %d", len(got.Cases))
	}
	for i, name := range []string{"a", "b", "c", "d", "e"} {
		if got.Cases[i].Name != name {
			t.Errorf("case %d = %q, want flows kept in order", i, got.Cases[i].Name)
		}
	}
}

// A suite without a device omits the device attribute; an empty suite has zero counts.
func TestJUnitEmptySuite(t *testing.T) {
	out, doc := junit(t, []Suite{{Name: "empty", Start: t0}})
	s := doc.Suites[0]
	if s.Device != nil {
		t.Errorf("device attribute present: %q", *s.Device)
	}
	if s.Tests != 0 || s.Failures != 0 || s.Time != "0.000" || len(s.Cases) != 0 {
		t.Errorf("empty suite = %+v", s)
	}
	if strings.Contains(out, "<testcase") {
		t.Errorf("empty suite has a testcase: %s", out)
	}
}

// Several suites (one per device or shard) are kept in order, each with its own counts.
func TestJUnitMultipleSuites(t *testing.T) {
	_, doc := junit(t, []Suite{
		{Name: "shard 1", Device: "a", Start: t0, Flows: []Flow{{Name: "x", Status: "ERROR", Duration: time.Second}}},
		{Name: "shard 2", Device: "b", Start: t0.Add(time.Hour), Flows: []Flow{{Name: "y", Status: "SUCCESS", Duration: 2 * time.Second}, {Name: "z", Status: "SUCCESS"}}},
	})
	if len(doc.Suites) != 2 {
		t.Fatalf("suites = %d", len(doc.Suites))
	}
	a, b := doc.Suites[0], doc.Suites[1]
	if a.Name != "shard 1" || a.Tests != 1 || a.Failures != 1 || a.Time != "1.000" {
		t.Errorf("suite 1 = %+v", a)
	}
	if b.Name != "shard 2" || b.Tests != 2 || b.Failures != 0 || b.Time != "2.000" || b.Timestamp != "2026-11-02T10:30:15" {
		t.Errorf("suite 2 = %+v", b)
	}
}

// Test case id and classname default to the flow name, overridden by the junitId and
// junitClassname properties, which are not repeated as properties.
func TestJUnitCaseIdentity(t *testing.T) {
	cases := []struct {
		name            string
		props           map[string]string
		wantID, wantCls string
	}{
		{"defaults", nil, "Login", "Login"},
		{"id only", map[string]string{"junitId": "TC-1"}, "TC-1", "Login"},
		{"classname only", map[string]string{"junitClassname": "auth.Login"}, "Login", "auth.Login"},
		{"both", map[string]string{"junitId": "TC-2", "junitClassname": "auth"}, "TC-2", "auth"},
		{"empty values fall back", map[string]string{"junitId": "", "junitClassname": ""}, "Login", "Login"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, doc := junit(t, []Suite{{Name: "s", Flows: []Flow{{Name: "Login", Status: "SUCCESS", Properties: c.props}}}})
			tc := doc.Suites[0].Cases[0]
			if tc.ID != c.wantID || tc.Classname != c.wantCls || tc.Name != "Login" {
				t.Errorf("id/classname/name = %q/%q/%q", tc.ID, tc.Classname, tc.Name)
			}
			for _, p := range tc.properties() {
				if p.Name == "junitId" || p.Name == "junitClassname" {
					t.Errorf("%s repeated as a property", p.Name)
				}
			}
			if tc.Props != nil {
				t.Errorf("properties element present with no other properties")
			}
		})
	}
}

// Other properties are listed sorted by name, then tags as one "tags" property.
func TestJUnitProperties(t *testing.T) {
	f := Flow{Name: "f", Status: "SUCCESS", Tags: []string{"smoke", "auth"},
		Properties: map[string]string{"zeta": "last", "alpha": "first", "junitId": "X", "mid": "a&b<c>"}}
	out, doc := junit(t, []Suite{{Name: "s", Flows: []Flow{f}}})
	props := doc.Suites[0].Cases[0].properties()
	want := []pProp{{"alpha", "first"}, {"mid", "a&b<c>"}, {"zeta", "last"}, {"tags", "smoke, auth"}}
	if len(props) != len(want) {
		t.Fatalf("properties = %+v", props)
	}
	for i := range want {
		if props[i] != want[i] {
			t.Errorf("property %d = %+v, want %+v", i, props[i], want[i])
		}
	}
	if !strings.Contains(out, `value="a&amp;b&lt;c&gt;"`) {
		t.Errorf("property value not escaped: %s", out)
	}
}

// Tags alone produce a properties element with just the tags property.
func TestJUnitTagsOnly(t *testing.T) {
	_, doc := junit(t, []Suite{{Name: "s", Flows: []Flow{{Name: "f", Status: "SUCCESS", Tags: []string{"one"}}}}})
	props := doc.Suites[0].Cases[0].properties()
	if len(props) != 1 || props[0] != (pProp{"tags", "one"}) {
		t.Errorf("properties = %+v", props)
	}
}

// Only ERROR flows get a <failure> with the (escaped) error text; others have none.
func TestJUnitFailure(t *testing.T) {
	flows := []Flow{
		{Name: "err", Status: "ERROR", Error: `Element not found: "Log <in>" & more`},
		{Name: "err-empty", Status: "ERROR"},
		{Name: "warn", Status: "WARNING", Error: "warned about something"},
		{Name: "ok", Status: "SUCCESS"},
		{Name: "skip", Status: "SKIPPED"},
	}
	out, doc := junit(t, []Suite{{Name: "s", Flows: flows}})
	cs := doc.Suites[0].Cases
	if cs[0].Failure == nil || *cs[0].Failure != `Element not found: "Log <in>" & more` {
		t.Errorf("failure = %v", cs[0].Failure)
	}
	if !strings.Contains(out, "&lt;in&gt;") || !strings.Contains(out, "&amp; more") {
		t.Errorf("failure text not escaped: %s", out)
	}
	if cs[1].Failure == nil || *cs[1].Failure != "" {
		t.Errorf("empty error ERROR flow should still have a failure element: %v", cs[1].Failure)
	}
	for _, c := range cs[2:] {
		if c.Failure != nil {
			t.Errorf("%s has a failure element", c.Name)
		}
	}
	for i, st := range []string{"ERROR", "ERROR", "WARNING", "SUCCESS", "SKIPPED"} {
		if cs[i].Status != st {
			t.Errorf("case %d status = %q", i, cs[i].Status)
		}
	}
}

// Case time is seconds with three decimals; timestamp is the flow's start without a zone.
func TestJUnitCaseTime(t *testing.T) {
	cases := map[time.Duration]string{
		0:                          "0.000",
		time.Millisecond:           "0.001",
		1500 * time.Millisecond:    "1.500",
		61*time.Second + 5e8:       "61.500",
		1234567 * time.Microsecond: "1.235",
		499 * time.Microsecond:     "0.000",
	}
	for d, want := range cases {
		_, doc := junit(t, []Suite{{Name: "s", Flows: []Flow{{Name: "f", Status: "SUCCESS", Duration: d, Start: t0}}}})
		c := doc.Suites[0].Cases[0]
		if c.Time != want {
			t.Errorf("time(%v) = %q, want %q", d, c.Time, want)
		}
		if c.Timestamp != "2026-11-02T09:30:15" {
			t.Errorf("timestamp = %q", c.Timestamp)
		}
	}
	if got := secs(2 * time.Minute); got != "120.000" {
		t.Errorf("secs(2m) = %q", got)
	}
}

// The file attribute is relative to the working directory when the flow is under it,
// absolute otherwise, and omitted when empty.
func TestJUnitFileAttribute(t *testing.T) {
	wd := t.TempDir()
	t.Chdir(wd)
	wd, _ = os.Getwd() // resolve symlinks the same way rel() sees them
	outside := filepath.Join(filepath.Dir(wd), "elsewhere", "x.yaml")
	flows := []Flow{
		{Name: "under", Status: "SUCCESS", File: filepath.Join(wd, "flows", "login.yaml")},
		{Name: "outside", Status: "SUCCESS", File: outside},
		{Name: "none", Status: "SUCCESS"},
	}
	_, doc := junit(t, []Suite{{Name: "s", Flows: flows}})
	cs := doc.Suites[0].Cases
	if cs[0].File == nil || *cs[0].File != filepath.Join("flows", "login.yaml") {
		t.Errorf("under wd: %v", cs[0].File)
	}
	if cs[1].File == nil || *cs[1].File != outside {
		t.Errorf("outside wd: %v", cs[1].File)
	}
	if cs[2].File != nil {
		t.Errorf("empty file attribute present: %q", *cs[2].File)
	}
}

// rel keeps paths it cannot relativize: empty, already relative, or outside the cwd.
func TestRel(t *testing.T) {
	wd := t.TempDir()
	t.Chdir(wd)
	wd, _ = os.Getwd()
	cases := map[string]string{
		"":                                   "",
		"relative/flow.yaml":                 "relative/flow.yaml",
		filepath.Join(wd, "a.yaml"):          "a.yaml",
		filepath.Join(wd, "d", "e", "f.yml"): filepath.Join("d", "e", "f.yml"),
		wd:                                   ".",
		"/definitely/elsewhere.yaml":         "/definitely/elsewhere.yaml",
	}
	for in, want := range cases {
		if got := rel(in); got != want {
			t.Errorf("rel(%q) = %q, want %q", in, got, want)
		}
	}
}

// A path under the cwd whose first element merely starts with ".." is still under the cwd.
func TestRelDotDotPrefixedName(t *testing.T) {
	wd := t.TempDir()
	t.Chdir(wd)
	wd, _ = os.Getwd()
	p := filepath.Join(wd, "..flows", "x.yaml")
	if got := rel(p); got != filepath.Join("..flows", "x.yaml") {
		t.Errorf("rel(%q) = %q, want ..flows/x.yaml", p, got)
	}
}

// The XML is indented two spaces per level and has the documented element names.
func TestJUnitExactShape(t *testing.T) {
	f := Flow{Name: "Login", File: "flows/login.yaml", Status: "ERROR", Error: "nope", Duration: 2 * time.Second, Start: t0,
		Tags: []string{"smoke"}, Properties: map[string]string{"owner": "qa"}}
	b, err := JUnit([]Suite{{Name: "Suite", Device: "Pixel", Start: t0, Flows: []Flow{f}}})
	if err != nil {
		t.Fatal(err)
	}
	want := `<?xml version='1.0' encoding='UTF-8'?>
<testsuites>
  <testsuite name="Suite" device="Pixel" tests="1" failures="1" time="2.000" timestamp="2026-11-02T09:30:15">
    <testcase id="Login" name="Login" classname="Login" file="flows/login.yaml" time="2.000" timestamp="2026-11-02T09:30:15" status="ERROR">
      <properties>
        <property name="owner" value="qa"></property>
        <property name="tags" value="smoke"></property>
      </properties>
      <failure>nope</failure>
    </testcase>
  </testsuite>
</testsuites>
`
	if string(b) != want {
		t.Errorf("JUnit =\n%s\nwant\n%s", b, want)
	}
}

// ---- HTML ----

// The HTML report is a full self-contained page with light and dark styles.
func TestHTMLPage(t *testing.T) {
	out := string(HTML(nil, false))
	for _, want := range []string{
		"<!doctype html>", `<meta charset="utf-8">`, `name="viewport"`, "<title>Test Buddy report</title>",
		"prefers-color-scheme:dark", "<style>", "</style></head><body>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if !strings.HasSuffix(out, "</body></html>\n") {
		t.Errorf("page ending: %q", out[len(out)-30:])
	}
	if strings.Contains(out, "<script") || strings.Contains(out, "http://") || strings.Contains(out, "https://") {
		t.Error("report should not load or run anything external")
	}
}

// Each suite gets a heading with device, pass/fail counts, total time and start time.
func TestHTMLSuiteHeader(t *testing.T) {
	s := Suite{Name: "Nightly", Device: "Pixel 9", Start: t0, Flows: []Flow{
		{Name: "a", Status: "SUCCESS", Duration: 1200 * time.Millisecond},
		{Name: "b", Status: "ERROR", Duration: 300 * time.Millisecond},
		{Name: "c", Status: "WARNING", Duration: 40 * time.Millisecond},
		{Name: "d", Status: "SKIPPED"},
	}}
	out := string(HTML([]Suite{s}, false))
	want := "<h1>Nightly</h1><p class=muted>Pixel 9 · 3 passed, 1 failed · 1.54s · Mon, 02 Nov 2026 09:30:15 UTC</p>"
	if !strings.Contains(out, want) {
		t.Errorf("header missing %q in\n%s", want, out)
	}
}

// Each flow is a <details> block classed by status with name, file and duration.
func TestHTMLFlowBlock(t *testing.T) {
	wd := t.TempDir()
	t.Chdir(wd)
	wd, _ = os.Getwd()
	f := Flow{Name: "Login", File: filepath.Join(wd, "flows", "login.yaml"), Status: "SUCCESS", Duration: 1234567 * time.Microsecond}
	out := string(HTML([]Suite{{Name: "s", Flows: []Flow{f}}}, false))
	want := `<details class="flow SUCCESS"><summary><b class=st>SUCCESS</b><b>Login</b><span class=muted>` +
		filepath.Join("flows", "login.yaml") + ` · 1.235s</span></summary></details>`
	if !strings.Contains(out, want) {
		t.Errorf("flow block missing %q in\n%s", want, out)
	}
	if strings.Contains(out, "<pre>") || strings.Contains(out, "tags:") || strings.Contains(out, "<table>") {
		t.Error("success flow without tags/error/steps rendered extras")
	}
}

// Tags and errors are shown (escaped) only when present.
func TestHTMLTagsAndError(t *testing.T) {
	f := Flow{Name: "f", Status: "ERROR", Tags: []string{"smoke", "<b>"}, Error: "line 1\nline <2> & \"3\""}
	out := string(HTML([]Suite{{Name: "s", Flows: []Flow{f}}}, false))
	if !strings.Contains(out, "<p class=muted>tags: smoke, &lt;b&gt;</p>") {
		t.Errorf("tags line missing in\n%s", out)
	}
	if !strings.Contains(out, "<pre>line 1\nline &lt;2&gt; &amp; &#34;3&#34;</pre>") {
		t.Errorf("error block missing in\n%s", out)
	}
	if !strings.Contains(out, `<details class="flow ERROR">`) {
		t.Error("error flow not classed ERROR")
	}
}

// Every user-supplied string is HTML-escaped: no markup from names, devices, files or steps.
func TestHTMLEscaping(t *testing.T) {
	evil := `<script>alert("x")</script>`
	s := Suite{Name: evil, Device: evil, Start: t0, Flows: []Flow{{
		Name: evil, File: "/tmp/" + evil, Status: "ERROR", Error: evil, Tags: []string{evil},
		Steps: []Step{{Desc: evil, Status: "failed"}},
	}}}
	for _, detailed := range []bool{false, true} {
		out := string(HTML([]Suite{s}, detailed))
		if strings.Contains(out, "<script>") {
			t.Errorf("detailed=%v: unescaped <script> in output", detailed)
		}
		if n := strings.Count(out, "&lt;script&gt;alert(&#34;x&#34;)&lt;/script&gt;"); n < 5 {
			t.Errorf("detailed=%v: escaped payload appears %d times, want >= 5", detailed, n)
		}
	}
}

// Detailed reports add a numbered step table; plain reports and step-less flows do not.
func TestHTMLDetailedSteps(t *testing.T) {
	f := Flow{Name: "f", Status: "SUCCESS", Steps: []Step{
		{Desc: "launchApp", Status: "passed", Duration: 1500 * time.Millisecond},
		{Desc: `tapOn: "A & B"`, Status: "failed", Error: "not found", Duration: 20 * time.Millisecond},
		{Desc: "back", Status: "skipped"},
	}}
	plain := string(HTML([]Suite{{Name: "s", Flows: []Flow{f}}}, false))
	if strings.Contains(plain, "<table>") {
		t.Error("plain report has a step table")
	}
	detailed := string(HTML([]Suite{{Name: "s", Flows: []Flow{f}}}, true))
	want := "<table>" +
		"<tr><td>1</td><td>passed</td><td>launchApp</td><td class=muted>1.5s</td></tr>" +
		"<tr><td>2</td><td>failed</td><td>tapOn: &#34;A &amp; B&#34;</td><td class=muted>20ms</td></tr>" +
		"<tr><td>3</td><td>skipped</td><td>back</td><td class=muted>0s</td></tr>" +
		"</table></details>"
	if !strings.Contains(detailed, want) {
		t.Errorf("step table missing %q in\n%s", want, detailed)
	}
	noSteps := string(HTML([]Suite{{Name: "s", Flows: []Flow{{Name: "g", Status: "SUCCESS"}}}}, true))
	if strings.Contains(noSteps, "<table>") {
		t.Error("detailed report has a table for a flow with no steps")
	}
}

// Several suites each get their own heading, in order, with their own flows.
func TestHTMLMultipleSuites(t *testing.T) {
	out := string(HTML([]Suite{
		{Name: "first", Flows: []Flow{{Name: "f1", Status: "SUCCESS"}}},
		{Name: "second", Flows: []Flow{{Name: "f2", Status: "ERROR"}, {Name: "f3", Status: "ERROR"}}},
	}, false))
	i1, i2 := strings.Index(out, "<h1>first</h1>"), strings.Index(out, "<h1>second</h1>")
	if i1 < 0 || i2 < 0 || i1 > i2 {
		t.Fatalf("headings out of order: %d %d", i1, i2)
	}
	if !strings.Contains(out[i1:i2], "1 passed, 0 failed") || !strings.Contains(out[i2:], "0 passed, 2 failed") {
		t.Errorf("per-suite counts wrong:\n%s", out)
	}
	if strings.Index(out, "<b>f1</b>") > i2 || strings.Index(out, "<b>f2</b>") < i2 {
		t.Error("flows rendered under the wrong suite")
	}
	if n := len(regexp.MustCompile(`<details class="flow `).FindAllString(out, -1)); n != 3 {
		t.Errorf("flow blocks = %d", n)
	}
}

// Flow and Step marshal with their documented JSON names; durations are in milliseconds as
// the "durationMs" name says.
func TestFlowJSON(t *testing.T) {
	f := Flow{Name: "n", File: "f.yaml", Device: "d", Status: "ERROR", Error: "e", Duration: 1500 * time.Millisecond, Start: t0,
		Tags: []string{"t"}, Properties: map[string]string{"k": "v"}, DebugDir: "dbg",
		Steps: []Step{{Desc: "tapOn: x", Status: "failed", Error: "x", Duration: 20 * time.Millisecond}}}
	b, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"name", "file", "device", "status", "error", "durationMs", "start", "tags", "properties", "steps", "debugDir"} {
		if _, ok := m[k]; !ok {
			t.Errorf("JSON lacks %q: %s", k, b)
		}
	}
	empty, _ := json.Marshal(Flow{Name: "n"})
	for _, k := range []string{"error", "tags", "properties", "steps", "debugDir"} {
		if strings.Contains(string(empty), `"`+k+`"`) {
			t.Errorf("empty %s not omitted: %s", k, empty)
		}
	}
	if ms := m["durationMs"].(float64); ms != 1500 {
		t.Errorf("durationMs = %v, want 1500", ms)
	}
	if ms := m["steps"].([]any)[0].(map[string]any)["durationMs"].(float64); ms != 20 {
		t.Errorf("step durationMs = %v, want 20", ms)
	}
}
