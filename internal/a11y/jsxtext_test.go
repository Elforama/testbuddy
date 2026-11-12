package a11y

import (
	"strings"
	"testing"
	"time"
)

// Issue #180: inside a {…} expression, JSX text is text, not JavaScript. An apostrophe
// (Don't) or a URL (http://) in it used to start a string or a line comment, the scan for
// the closing brace ran past it, and every finding after it in the file was lost. Each
// file ends with a second component, whose control must still be reported.
func TestJSXTextInExpression(t *testing.T) {
	dir := write(t, map[string]string{
		// The issue's example: an apostrophe in JSX passed as an attribute.
		"src/Card.tsx": `import { Pressable, Text } from 'react-native';
export function A() {
  return <View>
    <Card title={<Text>Don't</Text>} />
    <Pressable onPress={save}><Icon /></Pressable>
  </View>;
}
export function B() {
  return <Pressable onPress={back}><Icon /></Pressable>;
}
`,
		// A URL in JSX text in an attribute.
		"web/Tip.tsx": `export const T = () => (
  <div>
    <Tip text={<span>Docs at http://example.com</span>} />
    <button onClick={save}><img src="s.png" /></button>
  </div>
);
export const Z = () => <button onClick={tip}><img src="z.png" /></button>;
`,
		// An apostrophe in the JSX a .map() callback renders, in children.
		"web/List.tsx": `export const L = ({ items }) => (
  <ul>
    {items.map((it) => <li key={it.id}>it's {it.name}</li>)}
    <button onClick={save}><img src="s.png" /></button>
  </ul>
);
export const Z = () => <button onClick={list}><img src="z.png" /></button>;
`,
		// JSX text in a child expression: quotes, //, a fragment, an unclosed quote.
		"web/Note.tsx": `export const N = ({ ok }) => (
  <div>
    {ok && <p>Don't miss <a href="http://x.org/a">http://x.org</a>, it's "great</p>}
    {ok ? <>Isn't it? // no</> : <b>'</b>}
    <button onClick={open}><img src="o.png" /></button>
  </div>
);
export const Z = () => <button onClick={note}><img src="z.png" /></button>;
`,
	})
	expect(t, rulesAt(t, dir), "rn-no-selector@005", "rn-no-selector@009",
		"web-no-selector@004", "web-no-selector@004", "web-no-selector@005",
		"web-no-selector@007", "web-no-selector@007", "web-no-selector@008")
}

// Issue #180: JavaScript inside {…}, also inside JSX inside {…}, still skips its strings,
// comments, template literals and regular expressions: a brace or a tag in one of them is
// not code. A '<button>' string is not JSX, and TypeScript generics are not tags.
func TestJSInExpressionStillSkipsLiterals(t *testing.T) {
	dir := write(t, map[string]string{
		"web/Code.tsx": `export const C = ({ a, b }) => (
  <div>
    <div title={'<button>'} data-x={"<button onClick={x}>"} />
    <div title={/* <button> } */ a} data-y={// <button> }
      b} />
    <Card head={<span>{log('}')}{"{"}it's</span>} />
    <div className={` + "`a ${b ? '}' : `}${'`'}`} c`" + `} />
    <Tip test={(s) => /['"}]/.test(s) && a / b / 2} />
    <Card body={<p title="Don't">{'</p>'}{(<T,>(x: T) => x)(a)}{useState<string>('')}</p>} />
    <button onClick={save}><img src="s.png" /></button>
  </div>
);
export const Z = () => <button onClick={code}><img src="z.png" /></button>;
`,
	})
	expect(t, rulesAt(t, dir), "web-no-selector@010", "web-no-selector@013")
}

// Issue #180: a .map() whose JSX has an apostrophe ends at its own ')': the controls after
// it are not taken as the loop's items, so --fix gives them an id instead of leaving them.
func TestFixAfterJSXTextInMap(t *testing.T) {
	dir := write(t, map[string]string{
		"web/List.tsx": `export const L = ({ items }) => (
  <ul>
    {items.map((it) => <li key={it.id}>it's <button onClick={pick}><img src="p.png" /></button></li>)}
    <button onClick={save}><img src="s.png" /></button>
  </ul>
);
`,
	})
	rep := fixDir(t, dir, false)
	var fixed []string
	for _, f := range rep.Fixed {
		fixed = append(fixed, f.Added)
	}
	if strings.Join(fixed, " ") != `data-testid="save-button"` {
		t.Errorf("fixed %v, want only save-button", fixed)
	}
	if len(rep.Left) != 1 || !strings.Contains(rep.Left[0].Reason, ".map()") {
		t.Errorf("left %+v, want the button .map() repeats", rep.Left)
	}
}

// Issue #180 review: a comment in a tag's head, a generic component (<Select<Opt> …>), a
// type parameter's default (<T = X>) and a regular expression after a keyword (typeof, in)
// are read as what they are, and lose nothing after them.
func TestJSXHeadsGenericsAndRegexKeywords(t *testing.T) {
	dir := write(t, map[string]string{
		"web/More.tsx": `export const M = ({ a }) => (
  <div>
    <Card body={<p /* it's */ a="1">Don't</p>} />
    <Card body={<Select<Opt> value={a}>it's</Select>} />
    <Card f={<T = string>(x: T) => x} g={typeof /'/ === "x"} h={a in /'/} />
    <Select<Opt> onChange={pick}><button onClick={save}><img src="s.png" /></button></Select>
  </div>
);
export const Z = () => <button onClick={more}><img src="z.png" /></button>;
`,
		"src/Pick.tsx": `import { Pressable } from 'react-native';
export const P = () => <Pressable<Opt> onPress={go} testID="pick"><Icon /></Pressable>;
`,
	})
	expect(t, rulesAt(t, dir), "web-no-selector@006", "web-no-selector@009")
}

// Issue #180 review: a .map() on the line of JSX text with an apostrophe or a URL is still
// a loop: that text is not a string or a comment it would be in. --fix leaves the button it
// repeats for a person, and never gives every item the same id. A .map( in a real string or
// comment is not a loop.
func TestMapAfterJSXTextOnItsLine(t *testing.T) {
	dir := write(t, map[string]string{
		"web/A.tsx": `export const A = ({ items }) => (
  <div><p>Don't</p>{items.map((it) => <button key={it} onClick={pick}><img src="p.png" /></button>)}</div>
);
`,
		"web/B.tsx": `export const B = ({ items }) => (
  <div><p>See http://x.org</p>{items.map((it) => <button key={it} onClick={pick}><img src="p.png" /></button>)}</div>
);
`,
		"web/C.tsx": `export const C = () => (
  <div title={"items.map((it) =>"}>{/* items.map( */}<button onClick={go}><img src="g.png" /></button></div>
);
`,
	})
	rep := fixDir(t, dir, false)
	var fixed []string
	for _, f := range rep.Fixed {
		fixed = append(fixed, f.Added)
	}
	if strings.Join(fixed, " ") != `data-testid="go-button"` {
		t.Errorf("fixed %v, want only go-button", fixed)
	}
	if len(rep.Left) != 2 {
		t.Errorf("left %+v, want the two buttons .map() repeats", rep.Left)
	}
	for _, l := range rep.Left {
		if !strings.Contains(l.Reason, ".map()") {
			t.Errorf("left %+v, not for its .map()", l)
		}
	}
}

// Issue #180 review: unclosed { and JSX, as in a file someone is half-way through
// editing, are read in time close to linear: a failed scan is not redone at every level
// around it. Each takes well under 100 ms; the bound is generous so a slow machine passes.
func TestUnclosedJSXIsFast(t *testing.T) {
	var half strings.Builder
	half.WriteString("export const A = ({ ok }) => (\n  <View>\n")
	for i := 0; i < 40; i++ {
		half.WriteString("    {ok && (<View style={styles.row}>\n      <Text>Row</Text>\n      <Pressable onPress={() => { save(\n")
	}
	half.WriteString("  </View>\n);\n")
	for name, src := range map[string]string{
		"<a b={ x40":          strings.Repeat("<a b={", 40),
		"<a>{ x40":            strings.Repeat("<a>{", 40),
		"half-edited x40":     half.String(),
		"2000 unclosed tags":  "<x a={" + strings.Repeat("<a b={1}>", 2000),
		"<a b={ x2000":        strings.Repeat("<a b={", 2000),
		"{ok && (<View x2000": strings.Repeat("{ok && (<View style={s}><Pressable onPress={() => { save(\n", 2000),
	} {
		start := time.Now()
		p := &markupParser{src: src}
		root := parse(p)
		jsLoops(src, p.inLiteral)
		took := time.Since(start)
		t.Logf("%s: %d bytes in %v, %d steps, %d tags", name, len(src), took, p.steps, len(root.children))
		if took > time.Second {
			t.Errorf("%s: %d bytes took %v", name, len(src), took)
		}
	}
}

// FuzzParseJSX: the JSX reader never panics or hangs on any source, its tags lie in the
// source, and a bracket's pair comes after it (issue #180). Run it with
// go test ./internal/a11y -fuzz FuzzParseJSX -fuzztime 30s. The JSX-aware scans have a
// budget of steps, quadratic in the source, which an exponential scan soon runs past.
func FuzzParseJSX(f *testing.F) {
	for _, s := range []string{
		`<Card title={<Text>Don't</Text>} />`,
		`{items.map((it) => <li key={it.id}>it's {it.name}</li>)}`,
		`<a b={<span>http://x</span>}>{ok && <>Isn't // it</>}</a>`,
		"<d c={`a ${b ? '}' : `}`} c`} t={(s) => /['\"}]/.test(s) && a / b} />",
		`<C b={<p title="Don't">{'</p>'}{(<T,>(x: T) => x)(a)}{useState<string>('')}</p>} />`,
		`{a < b}{<T extends X>(x) => x}{x <y}`,
		strings.Repeat("<a b={", 25),
		strings.Repeat("<a>{", 25),
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		p := &markupParser{src: src}
		root := parse(p)
		if n := len(src) + 1; p.steps > 4*n*n+64 {
			t.Fatalf("%d steps for a source of %d", p.steps, len(src))
		}
		root.walkAll(func(x *tag) bool {
			if x.off < 0 || x.off > len(src) {
				t.Fatalf("tag %q at %d, outside a source of %d", x.name, x.off, len(src))
			}
			return true
		})
		match := jsMatcher(src)
		for i := 0; i < len(src); i++ {
			if src[i] == '{' || src[i] == '(' {
				if e := match(src, i); e != -1 && (e <= i || e > len(src)) {
					t.Fatalf("match(%d) = %d in a source of %d", i, e, len(src))
				}
			}
		}
		jsLoops(src, p.inLiteral)
	})
}
