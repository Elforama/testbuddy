package a11y

import (
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/ci"
)

const stepsPerByte = 8

func wallLimit() time.Duration {
	if ci.On() {
		return 10 * time.Second
	}
	return time.Second
}

func TestJSXTextInExpression(t *testing.T) {
	dir := write(t, map[string]string{
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
		"web/Tip.tsx": `export const T = () => (
  <div>
    <Tip text={<span>Docs at http://example.com</span>} />
    <button onClick={save}><img src="s.png" /></button>
  </div>
);
export const Z = () => <button onClick={tip}><img src="z.png" /></button>;
`,
		"web/List.tsx": `export const L = ({ items }) => (
  <ul>
    {items.map((it) => <li key={it.id}>it's {it.name}</li>)}
    <button onClick={save}><img src="s.png" /></button>
  </ul>
);
export const Z = () => <button onClick={list}><img src="z.png" /></button>;
`,
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

func TestMapAfterDivisionIsALoop(t *testing.T) {
	dir := write(t, map[string]string{
		"web/A.tsx": `export const A = ({ rows, n }) => (
  <div>{n-- / 2 > 0 && rows.map((r) => r.w / 2 > 1 &&
    <button key={r.id} onClick={pick}><img src="p.png" /></button>)}</div>
);
`,
		"web/B.tsx": `export const B = ({ rows, of }) => (
  <div>{of / 2 > 0 && rows.map((r) => r.w / 2 > 1 &&
    <button key={r.id} onClick={pick}><img src="p.png" /></button>)}</div>
);
`,
	})
	rep := fixDir(t, dir, false)
	if len(rep.Fixed) != 0 {
		t.Errorf("fixed %+v, want nothing: .map() repeats both buttons", rep.Fixed)
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

func TestTypeParams(t *testing.T) {
	for src, want := range map[string]bool{
		"<T,>(x: T) => x": true, "<T extends X>(x: T) => x": true, "<T = string>(x: T) => x": true,
		"<pkey={x}>": false, "<a b={x}>": false, "<T>(x)": false, "<a == b": false,
	} {
		if got := typeParams(src, 0); got != want {
			t.Errorf("typeParams(%q) = %v, want %v", src, got, want)
		}
	}
}

func TestDeepNestingIsLean(t *testing.T) {
	const depth = 4000
	src := strings.Repeat("{ok && <a b={x}>", depth) + strings.Repeat("</a>}", depth)
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	start := time.Now()
	p := &markupParser{src: src}
	parse(p)
	took := time.Since(start)
	runtime.ReadMemStats(&after)
	alloc := after.TotalAlloc - before.TotalAlloc
	t.Logf("depth %d, %d bytes: %v, %d MB allocated, %d steps", depth, len(src), took, alloc>>20, p.steps)
	if p.steps > stepsPerByte*len(src) {
		t.Errorf("depth %d: %d steps for %d bytes, over %d per byte", depth, p.steps, len(src), stepsPerByte)
	}
	if took > wallLimit() || alloc > 200<<20 {
		t.Errorf("depth %d took %v and %d MB", depth, took, alloc>>20)
	}
}

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
		"<a b={/* x50000":     "<div>\n" + strings.Repeat("<a b={/* x\n", 50000),
		"{/* x50000":          "<div>\n" + strings.Repeat("{/* x\n", 50000),
		`{""" x50000`:         "<div>\n" + strings.Repeat("{\"\"\" x\n", 50000),
		"{`a x50000":          "<div>\n" + strings.Repeat("{`a\n", 50000),
		"<a b={( x20000":      "<div>\n" + strings.Repeat("<a b={(\n", 20000),
		"misread x20000":      "<div>\n" + strings.Repeat("{(() => { switch (k) { case 1: {} /'/.test(s) } })()}\n", 20000),
		"misread {x20000":     "<div>\n" + strings.Repeat("  {(() => { switch (k) { case 1: {} /'/.test(s) }\n", 20000),
	} {
		start := time.Now()
		p := &markupParser{src: src}
		root := parse(p)
		jsLoops(src, p.inLiteral)
		took := time.Since(start)
		t.Logf("%s: %d bytes in %v, %d steps, %d tags", name, len(src), took, p.steps, len(root.children))
		if p.steps > stepsPerByte*len(src) {
			t.Errorf("%s: %d steps for %d bytes, over %d per byte", name, p.steps, len(src), stepsPerByte)
		}
		if took > wallLimit() {
			t.Errorf("%s: %d bytes took %v", name, len(src), took)
		}
	}
}

func FuzzParseJSX(f *testing.F) {
	for _, s := range []string{
		`<Card title={<Text>Don't</Text>} />`,
		`{items.map((it) => <li key={it.id}>it's {it.name}</li>)}`,
		`<a b={<span>http://x</span>}>{ok && <>Isn't // it</>}</a>`,
		"<d c={`a ${b ? '}' : `}`} c`} t={(s) => /['\"}]/.test(s) && a / b} />",
		`<C b={<p title="Don't">{'</p>'}{(<T,>(x: T) => x)(a)}{useState<string>('')}</p>} />`,
		`{a < b}{<T extends X>(x) => x}{x <y}`,
		"{(() => { if (a) {} /'/.test(s) })()}\n<a b={() => { switch (k) { case 1: {} /`/ } }} />",
		"x = {a} / 2 + f('/'); try {} finally {} /\"/.test(s); while (a) /'/",
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
