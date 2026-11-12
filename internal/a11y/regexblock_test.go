package a11y

import (
	"fmt"
	"strings"
	"testing"
)

// Issue #185: a '/' right after the '}' that ends a block starts a regular expression, not
// a division. It was read as a division, so a quote in the regular expression started a
// string, the brackets after it on its line were lost, and every finding after it in the
// file with them. Each file has a control on the line after, and a second component at its
// end: both must still be reported.
func TestRegexAfterBlock(t *testing.T) {
	for path, src := range map[string]string{
		// The issue's example: after if (…) {}, in a {…} child expression.
		"web/Issue.tsx": `export const A = ({ a, s }) => (
  <div>
    {(() => { if (a) {} /'/.test(s) })()}
    <button onClick={save}><img src="s.png" /></button>
  </div>
);
export const Z = () => <button onClick={issue}><img src="z.png" /></button>;
`,
		// After a function body.
		"web/Fn.tsx": `export const A = ({ s }) => (
  <div>
    {(() => { function f() {} /"/.test(s) })()}
    <button onClick={save}><img src="s.png" /></button>
  </div>
);
export const Z = () => <button onClick={fn}><img src="z.png" /></button>;
`,
		// After try {} catch {}, try {} finally {}, and else {}, in an attribute.
		"web/Try.tsx": `export const A = ({ s }) => (
  <div>
    <Card f={() => { try { go() } catch (e) {} /'/.test(s) }} />
    <Card f={() => { try { go() } finally {} /'/.test(s) }} />
    <Card f={() => { if (s) {} else {} /'/.test(s) }} />
    <button onClick={save}><img src="s.png" /></button>
  </div>
);
export const Z = () => <button onClick={tried}><img src="z.png" /></button>;
`,
		// A block in the file's own code, with a backtick in the regular expression: read as
		// a division, the backtick opened a template literal that ran to the end of the file.
		"web/Top.tsx": "const ok = (s) => { if (s) {} /`/.test(s) };\n" + `export const A = () => (
  <button onClick={save}><img src="s.png" /></button>
);
export const Z = () => <button onClick={top}><img src="z.png" /></button>;
`,
		// The same after the ')' of if (…), while (…) and for (…).
		"web/Cond.tsx": `export const A = ({ a, s }) => (
  <div>
    {(() => { if (a) /'/.test(s); while (a) /"/.test(s)
      for (;;) /` + "`" + `/.test(s) })()}
    <button onClick={save}><img src="s.png" /></button>
  </div>
);
export const Z = () => <button onClick={cond}><img src="z.png" /></button>;
`,
	} {
		t.Run(path, func(t *testing.T) {
			// The control on the line after, and the second component.
			want := map[string][]string{
				"web/Try.tsx":  {"web-no-selector@006", "web-no-selector@009"},
				"web/Top.tsx":  {"web-no-selector@003", "web-no-selector@005"},
				"web/Cond.tsx": {"web-no-selector@005", "web-no-selector@008"},
			}[path]
			if want == nil {
				want = []string{"web-no-selector@004", "web-no-selector@007"}
			}
			expect(t, rulesAt(t, write(t, map[string]string{path: src})), want...)
		})
	}
}

// Issue #185: a '/' after the '}' of an object literal is still a division: read as a
// regular expression, '/ 2 + f(' would hide a bracket and the "'" after it start a string.
// So after =, (, ',', :, ?, return, ${ and a {…} in JSX, an object is an expression.
func TestDivisionAfterObject(t *testing.T) {
	dir := write(t, map[string]string{
		"web/Obj.tsx": `export const A = ({ a, b, c }) => (
  <div>
    <Card w={({a: 1}) / 2 + f('/')} />
    <Card w={(() => { const x = {a} / 2 + f('/'); return x })()} />
    <Card w={({a: 1} / 2 + f('/'))} />
    <Card w={{ k: {a: 1} / 2 + f('/') }} />
    <Card w={c ? {a} / 2 : f('/')} />
    <Card w={(() => { return {a} / 2 + f('/') })()} />
    <Card w={[{a}, {b} / 2, f('/')]} />
    <Card w={` + "`${ {a: 1} / 2 + f('/') }`" + `} />
    <Card w={` + "`${a}` / 2 + f('/')" + `} />
    <Card w={{a} / 2 + f('/')} />
    <button onClick={save}><img src="s.png" /></button>
  </div>
);
export const Z = () => <button onClick={obj}><img src="z.png" /></button>;
`,
	})
	expect(t, rulesAt(t, dir), "web-no-selector@013", "web-no-selector@016")
}

// Issue #185: a .map() after a regular expression that follows a block, on its line, is a
// loop: read as a division, the regular expression's quote put the .map( in a string, and
// --fix gave every item the same id. The button after it gets one id, once.
func TestFixRegexAfterBlockKeepsLoop(t *testing.T) {
	dir := write(t, map[string]string{
		"web/A.tsx": `export const A = ({ rows, s }) => (
  <div>
    {(() => { if (s) {} /'/.test(s) })() || rows.map((r) => <button key={r.id} onClick={pick}><img src="p.png" /></button>)}
    <button onClick={save}><img src="s.png" /></button>
  </div>
);
`,
	})
	rep := fixDir(t, dir, false)
	expectAdded(t, rep, `4 data-testid="save-button"`)
	if len(rep.Left) != 1 || !strings.Contains(rep.Left[0].Reason, ".map()") {
		t.Errorf("left %+v, want the button .map() repeats", rep.Left)
	}
	if got := read(t, dir+"/web/A.tsx"); strings.Count(got, "data-testid") != 1 {
		t.Errorf("want one data-testid, got:\n%s", got)
	}
}

// Issue #185: where a '{' cannot be told a block or an object (case 1: {}), a regular
// expression after it may still be read as a division. In JSX, that costs the lines of
// the {…} it is in: the scan picks up again after them. (In a file's own code, outside
// JSX, there is no such resynchronising: a backtick in it still runs to the next one.)
func TestMisreadRegexInJSXCostsItsLines(t *testing.T) {
	dir := write(t, map[string]string{
		"web/Case.tsx": `export const A = ({ k, s }) => (
  <div>
    {(() => { switch (k) { case 1: {} /'/.test(s) } })()}
    <button onClick={save}><img src="s.png" /></button>
  </div>
);
export const Z = () => <button onClick={kase}><img src="z.png" /></button>;
`,
	})
	expect(t, rulesAt(t, dir), "web-no-selector@004", "web-no-selector@007")
}

// Issue #185: a tag whose {…} attribute does not close ends with that {…}, its other
// attributes unknown: it is reported, but --fix leaves it for a person, since it may have
// an id further on, and goes on to the control on the next line.
func TestFixAfterUnclosedAttribute(t *testing.T) {
	dir := write(t, map[string]string{
		"web/A.tsx": `export const A = ({ k, s }) => (
  <div>
    <button onClick={() => { switch (k) { case 1: {} /'/.test(s) } }} data-testid="run"><img src="r.png" /></button>
    <button onClick={save}><img src="s.png" /></button>
  </div>
);
`,
	})
	rep := fixDir(t, dir, false)
	expectAdded(t, rep, `4 data-testid="save-button"`)
	if got := read(t, dir+"/web/A.tsx"); strings.Count(got, "data-testid") != 2 {
		t.Errorf("want the run and save ids, once each, got:\n%s", got)
	}
	expectLeft(t, rep, "3 could not all be read")
}

// expectLeft checks what --fix left for a person: "line part-of-reason", in report order.
func expectLeft(t *testing.T, rep *FixReport, want ...string) {
	t.Helper()
	if len(rep.Left) != len(want) {
		t.Fatalf("left %+v, want %v", rep.Left, want)
	}
	for i, l := range rep.Left {
		line, why, _ := strings.Cut(want[i], " ")
		if fmt.Sprint(l.Line) != line || !strings.Contains(l.Reason, why) {
			t.Errorf("left %d %q, want %s", l.Line, l.Reason, want[i])
		}
	}
}

// Issue #185 review: the lines of a {…} child that does not close are code, not markup:
// i<a and n<button in them are comparisons, which --fix must not write an id into. The
// control after them gets its id; the file's code is untouched.
func TestFixLeavesCodeOfUnclosedChild(t *testing.T) {
	src := `export const A = ({ a, n, k, s }) => (
  <div>
    {(() => {
      switch (k) { case 1: {} /` + "`" + `/.test(s) }
      if (i<a) return null
      const ok = n<button; return ok
    })()}
    <button onClick={mid}><img src="m.png" /></button>
  </div>
);
`
	dir := write(t, map[string]string{"web/A.tsx": src})
	rep := fixDir(t, dir, false)
	expectAdded(t, rep, `8 data-testid="mid-button"`)
	got := read(t, dir+"/web/A.tsx")
	if want := strings.Replace(src, "<button onClick={mid}>", `<button data-testid="mid-button" onClick={mid}>`, 1); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// Issue #185 review: a .map() whose ')' cannot be found repeats what follows it all the
// same: --fix leaves its button for a person, never one fixed id for every item. In a {…}
// child, in an attribute, and in a file's own code (where main dropped such a loop too).
func TestFixLeavesUnclosedMap(t *testing.T) {
	dir := write(t, map[string]string{
		"web/Child.tsx": `export const A = ({ rows, s }) => (
  <div>
    {rows.map((r) => {
      switch (r.k) { case 1: {} /` + "`" + `/.test(s) }
      return <button onClick={pick}><img src="p.png" /></button>
    })}
  </div>
);
`,
		"web/Attr.tsx": `export const A = ({ rows, s }) => (
  <div>
    <List items={rows.map((r) => {
      switch (r.k) { case 1: {} /` + "`" + `/.test(s) }
      return <button onClick={pick}><img src="p.png" /></button>
    })} />
  </div>
);
`,
		"web/Quote.tsx": `export const A = ({ rows, s }) => (
  <div>
    {rows.map((r) => {
      switch (r.k) { case 1: {} /'/.test(s) }
      return <button onClick={pick}><img src="p.png" /></button>
    })}
  </div>
);
`,
		"web/Top.tsx": `const items = rows.map((r) => { switch (r.k) { case 1: {} /'}/.test(s) }
  return <button onClick={pick}><img src="p.png" /></button> })
`,
	})
	rep := fixDir(t, dir, false)
	if len(rep.Fixed) != 0 {
		t.Errorf("fixed %v, want nothing", added(rep))
	}
	// In Child and Attr, the misread backtick hides the button in what it takes for a
	// template literal, up to the end of the {…}: it is lost, but given no id.
	expectLeft(t, rep, "5 .map() repeats it", "2 .map() repeats it")
}

// Issue #185 review: a tag whose attributes could not all be read is still reported (left
// for a person), and does not count as text for the tags around it.
func TestUnreadTagIsReported(t *testing.T) {
	dir := write(t, map[string]string{
		"web/A.tsx": `export const A = ({ k, s }) => (
  <div role="button" onClick={go}>
    <button onClick={() => { switch (k) { case 1: {} /'/.test(s) } }}><img src="r.png" /></button>
  </div>
);
`,
	})
	expect(t, rulesAt(t, dir), "web-no-selector@002", "web-no-selector@003")
}

// Issue #185 review: right inside a block, a '{' opens a block too ({} in () => { {} }),
// and a regular expression can follow it. Read as a division, the backtick in it would hide
// the button the callback returns; read right, --fix names it.
func TestRegexAfterBlockInBlock(t *testing.T) {
	dir := write(t, map[string]string{
		"web/A.tsx": `export const A = ({ s }) => (
  <div>
    {(() => { {} /` + "`" + `/.test(s)
      return <button onClick={save}><img src="s.png" /></button>
    })()}
  </div>
);
`,
	})
	expectAdded(t, fixDir(t, dir, false), `4 data-testid="save-button"`)
}

// Issue #185 review: a {…} child that does not close may render text: the tag around it is
// not reported as having none.
func TestUnclosedChildMayBeText(t *testing.T) {
	dir := write(t, map[string]string{
		"web/A.tsx": `export const A = ({ k, s }) => (
  <div>
    <button onClick={go}>{(() => { switch (k) { case 1: {} /'/.test(s) } })()}
    </button>
    <button onClick={save}><img src="s.png" /></button>
  </div>
);
`,
	})
	expect(t, rulesAt(t, dir), "web-no-selector@005")
}
