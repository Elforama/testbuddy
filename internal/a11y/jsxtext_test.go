package a11y

import (
	"strings"
	"testing"
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

// FuzzParseJSX: the JSX reader never panics or hangs on any source, its tags lie in the
// source, and a bracket's pair comes after it (issue #180). Run it with
// go test ./internal/a11y -fuzz FuzzParseJSX -fuzztime 30s.
func FuzzParseJSX(f *testing.F) {
	for _, s := range []string{
		`<Card title={<Text>Don't</Text>} />`,
		`{items.map((it) => <li key={it.id}>it's {it.name}</li>)}`,
		`<a b={<span>http://x</span>}>{ok && <>Isn't // it</>}</a>`,
		"<d c={`a ${b ? '}' : `}`} c`} t={(s) => /['\"}]/.test(s) && a / b} />",
		`<C b={<p title="Don't">{'</p>'}{(<T,>(x: T) => x)(a)}{useState<string>('')}</p>} />`,
		`{a < b}{<T extends X>(x) => x}{x <y}`,
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		root := parseMarkup(src, false)
		root.walkAll(func(x *tag) bool {
			if x.off < 0 || x.off > len(src) {
				t.Fatalf("tag %q at %d, outside a source of %d", x.name, x.off, len(src))
			}
			return true
		})
		for i := 0; i < len(src); i++ {
			if src[i] == '{' || src[i] == '(' {
				if e := jsMatching(src, i); e != -1 && (e <= i || e > len(src)) {
					t.Fatalf("jsMatching(%d) = %d in a source of %d", i, e, len(src))
				}
			}
		}
		jsLoops(src)
	})
}
