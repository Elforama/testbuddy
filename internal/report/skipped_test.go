package report

import (
	"encoding/xml"
	"strings"
	"testing"
	"time"
)

// A run whose ordered flow failed: the flows after it are skipped, with the reason.
func skippedRun() Suite {
	return Suite{Name: "Nightly", Device: "Pixel 9", Start: t0, Flows: []Flow{
		{Name: "login", Status: "SUCCESS", Duration: time.Second, Start: t0},
		{Name: "checkout", Status: "ERROR", Error: "Element not found", Duration: time.Second, Start: t0},
		{Name: "profile", Status: "SKIPPED", Error: "skipped: an ordered flow failed", Start: t0},
		{Name: "logout", Status: "SKIPPED", Error: "skipped: an ordered flow failed", Start: t0},
	}}
}

// issue #59: JUnit marks a skipped flow with <skipped message="..."> and counts it in the
// suite's skipped attribute (tests still counts it), so CI shows it skipped, not passed.
func TestJUnitSkippedFlows(t *testing.T) {
	b, err := JUnit([]Suite{skippedRun()})
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Suites []struct {
			Tests    int `xml:"tests,attr"`
			Failures int `xml:"failures,attr"`
			Skipped  int `xml:"skipped,attr"`
			Cases    []struct {
				Name    string `xml:"name,attr"`
				Failure *string
				Skipped *struct {
					Message string `xml:"message,attr"`
				} `xml:"skipped"`
			} `xml:"testcase"`
		} `xml:"testsuite"`
	}
	if err := xml.Unmarshal(b, &doc); err != nil {
		t.Fatalf("not XML: %v\n%s", err, b)
	}
	s := doc.Suites[0]
	if s.Tests != 4 || s.Failures != 1 || s.Skipped != 2 {
		t.Errorf("suite tests=%d failures=%d skipped=%d, want 4, 1, 2\n%s", s.Tests, s.Failures, s.Skipped, b)
	}
	for _, c := range s.Cases {
		skipped := c.Name == "profile" || c.Name == "logout"
		if skipped && (c.Skipped == nil || c.Skipped.Message != "skipped: an ordered flow failed") {
			t.Errorf("%s: no <skipped message=...> element: %+v\n%s", c.Name, c.Skipped, b)
		}
		if !skipped && c.Skipped != nil {
			t.Errorf("%s has a <skipped> element", c.Name)
		}
	}
}

// issue #59: the HTML report counts skipped flows on their own, not as passed, and shows
// each one as skipped with its reason.
func TestHTMLSkippedFlows(t *testing.T) {
	out := string(HTML([]Suite{skippedRun()}, false))
	if want := "Pixel 9 · 1 passed, 1 failed, 2 skipped · "; !strings.Contains(out, want) {
		t.Errorf("header lacks %q in\n%s", want, out)
	}
	want := `<details class="flow SKIPPED"><summary><b class=st>SKIPPED</b><b>profile</b>`
	if !strings.Contains(out, want) {
		t.Errorf("skipped flow block missing %q in\n%s", want, out)
	}
	if !strings.Contains(out, "<pre>skipped: an ordered flow failed</pre>") {
		t.Errorf("skipped flow's reason missing in\n%s", out)
	}
	if !strings.Contains(out, ".SKIPPED .st{") {
		t.Error("no style for skipped flows")
	}
}
