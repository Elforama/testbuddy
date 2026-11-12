package a11y

import (
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
// expression after it may still be read as a division. That costs its own line at most:
// the scan picks up again at the next one.
func TestMisreadRegexCostsOneLine(t *testing.T) {
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

// Issue #185: a tag whose {…} attribute does not close ends at its line, its other
// attributes unknown: --fix gives it no id, since it may have one further on, and goes on
// to the control on the next line.
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
}
