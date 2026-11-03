// Package report writes test results: JUnit XML and HTML.
package report

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Flow is one flow's result.
type Flow struct {
	Name       string            `json:"name"`
	File       string            `json:"file"`
	Device     string            `json:"device"`
	Status     string            `json:"status"` // SUCCESS, ERROR, WARNING, SKIPPED
	Error      string            `json:"error,omitempty"`
	Duration   time.Duration     `json:"durationMs"`
	Start      time.Time         `json:"start"`
	Tags       []string          `json:"tags,omitempty"`
	Properties map[string]string `json:"properties,omitempty"`
	Steps      []Step            `json:"steps,omitempty"`
	DebugDir   string            `json:"debugDir,omitempty"`
}

type Step struct {
	Desc     string        `json:"desc"`
	Status   string        `json:"status"`
	Error    string        `json:"error,omitempty"`
	Duration time.Duration `json:"durationMs"`
}

// MarshalJSON writes durationMs in milliseconds, not time.Duration nanoseconds.
func (f Flow) MarshalJSON() ([]byte, error) {
	type flow Flow
	return json.Marshal(struct {
		flow
		Duration int64 `json:"durationMs"`
	}{flow(f), f.Duration.Milliseconds()})
}

// MarshalJSON writes durationMs in milliseconds, not time.Duration nanoseconds.
func (s Step) MarshalJSON() ([]byte, error) {
	type step Step
	return json.Marshal(struct {
		step
		Duration int64 `json:"durationMs"`
	}{step(s), s.Duration.Milliseconds()})
}

// Suite is a whole run (one per device or shard in JUnit).
type Suite struct {
	Name   string
	Device string
	Flows  []Flow
	Start  time.Time
}

type xmlProperty struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

type xmlCase struct {
	XMLName    xml.Name       `xml:"testcase"`
	ID         string         `xml:"id,attr"`
	Name       string         `xml:"name,attr"`
	Classname  string         `xml:"classname,attr"`
	File       string         `xml:"file,attr,omitempty"`
	Time       string         `xml:"time,attr"`
	Timestamp  string         `xml:"timestamp,attr"`
	Status     string         `xml:"status,attr"`
	Properties *[]xmlProperty `xml:"properties>property,omitempty"`
	Failure    *string        `xml:"failure,omitempty"`
}

type xmlSuite struct {
	XMLName   xml.Name  `xml:"testsuite"`
	Name      string    `xml:"name,attr"`
	Device    string    `xml:"device,attr,omitempty"`
	Tests     int       `xml:"tests,attr"`
	Failures  int       `xml:"failures,attr"`
	Time      string    `xml:"time,attr"`
	Timestamp string    `xml:"timestamp,attr"`
	Cases     []xmlCase `xml:"testcase"`
}

type xmlSuites struct {
	XMLName xml.Name   `xml:"testsuites"`
	Suites  []xmlSuite `xml:"testsuite"`
}

func secs(d time.Duration) string { return fmt.Sprintf("%.3f", d.Seconds()) }

// JUnit renders suites in the usual flow-runner JUnit shape.
func JUnit(suites []Suite) ([]byte, error) {
	var doc xmlSuites
	for _, s := range suites {
		xs := xmlSuite{Name: s.Name, Device: s.Device, Tests: len(s.Flows), Timestamp: s.Start.Format("2006-01-02T15:04:05")}
		var total time.Duration
		for _, f := range s.Flows {
			total += f.Duration
			id, class := f.Name, f.Name
			if v := f.Properties["junitId"]; v != "" {
				id = v
			}
			if v := f.Properties["junitClassname"]; v != "" {
				class = v
			}
			c := xmlCase{ID: id, Name: f.Name, Classname: class, File: rel(f.File), Time: secs(f.Duration),
				Timestamp: f.Start.Format("2006-01-02T15:04:05"), Status: f.Status}
			var props []xmlProperty
			keys := make([]string, 0, len(f.Properties))
			for k := range f.Properties {
				if k != "junitId" && k != "junitClassname" {
					keys = append(keys, k)
				}
			}
			sort.Strings(keys)
			for _, k := range keys {
				props = append(props, xmlProperty{k, f.Properties[k]})
			}
			if len(f.Tags) > 0 {
				props = append(props, xmlProperty{"tags", strings.Join(f.Tags, ", ")})
			}
			if len(props) > 0 {
				c.Properties = &props
			}
			if f.Status == "ERROR" {
				msg := f.Error
				c.Failure = &msg
				xs.Failures++
			}
			xs.Cases = append(xs.Cases, c)
		}
		xs.Time = secs(total)
		doc.Suites = append(doc.Suites, xs)
	}
	out, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte("<?xml version='1.0' encoding='UTF-8'?>\n"), append(out, '\n')...), nil
}

func rel(p string) string {
	if p == "" {
		return ""
	}
	wd, _ := os.Getwd()
	if r, err := filepath.Rel(wd, p); err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return r
	}
	return p
}

// HTML renders a self-contained report. detailed adds every step.
func HTML(suites []Suite, detailed bool) []byte {
	var b strings.Builder
	b.WriteString(`<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Test Buddy report</title>
<style>
:root{--bg:#fff;--fg:#1d1d1f;--muted:#6e6e73;--ok:#1a7f37;--bad:#cf222e;--warn:#9a6700;--card:#f6f8fa;--line:#d0d7de}
@media (prefers-color-scheme:dark){:root{--bg:#0d1117;--fg:#e6edf3;--muted:#8d96a0;--ok:#3fb950;--bad:#f85149;--warn:#d29922;--card:#161b22;--line:#30363d}}
body{font:14px/1.5 -apple-system,system-ui,sans-serif;background:var(--bg);color:var(--fg);margin:0;padding:24px 16px;max-width:960px;margin:auto}
h1{font-size:22px;margin:0 0 4px}.muted{color:var(--muted)}
.flow{background:var(--card);border:1px solid var(--line);border-radius:8px;margin:10px 0;padding:10px 14px}
.flow summary{cursor:pointer;display:flex;gap:10px;align-items:baseline;flex-wrap:wrap}
.SUCCESS .st{color:var(--ok)}.ERROR .st{color:var(--bad)}.WARNING .st{color:var(--warn)}
pre{white-space:pre-wrap;word-break:break-word;background:var(--bg);padding:8px;border-radius:6px;border:1px solid var(--line)}
table{border-collapse:collapse;width:100%;margin-top:6px}td{padding:2px 6px;border-top:1px solid var(--line);vertical-align:top}
</style></head><body>`)
	for _, s := range suites {
		pass, fail := 0, 0
		var total time.Duration
		for _, f := range s.Flows {
			total += f.Duration
			if f.Status == "ERROR" {
				fail++
			} else {
				pass++
			}
		}
		fmt.Fprintf(&b, "<h1>%s</h1><p class=muted>%s · %d passed, %d failed · %s · %s</p>",
			html.EscapeString(s.Name), html.EscapeString(s.Device), pass, fail, total.Round(time.Millisecond), s.Start.Format(time.RFC1123))
		for _, f := range s.Flows {
			fmt.Fprintf(&b, `<details class="flow %s"><summary><b class=st>%s</b><b>%s</b><span class=muted>%s · %s</span></summary>`,
				f.Status, f.Status, html.EscapeString(f.Name), html.EscapeString(rel(f.File)), f.Duration.Round(time.Millisecond))
			if len(f.Tags) > 0 {
				fmt.Fprintf(&b, "<p class=muted>tags: %s</p>", html.EscapeString(strings.Join(f.Tags, ", ")))
			}
			if f.Error != "" {
				fmt.Fprintf(&b, "<pre>%s</pre>", html.EscapeString(f.Error))
			}
			if detailed && len(f.Steps) > 0 {
				b.WriteString("<table>")
				for i, st := range f.Steps {
					fmt.Fprintf(&b, "<tr><td>%d</td><td>%s</td><td>%s</td><td class=muted>%s</td></tr>", i+1, st.Status,
						html.EscapeString(st.Desc), st.Duration.Round(time.Millisecond))
				}
				b.WriteString("</table>")
			}
			b.WriteString("</details>")
		}
	}
	b.WriteString("</body></html>\n")
	return []byte(b.String())
}
