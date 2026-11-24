package a11y

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestRegexAfterBlock(t *testing.T) {
	for path, src := range map[string]string{
		"web/Issue.tsx": `export const A = ({ a, s }) => (
  <div>
    {(() => { if (a) {} /'/.test(s) })()}
    <button onClick={save}><img src="s.png" /></button>
  </div>
);
export const Z = () => <button onClick={issue}><img src="z.png" /></button>;
`,
		"web/Fn.tsx": `export const A = ({ s }) => (
  <div>
    {(() => { function f() {} /"/.test(s) })()}
    <button onClick={save}><img src="s.png" /></button>
  </div>
);
export const Z = () => <button onClick={fn}><img src="z.png" /></button>;
`,
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
		"web/Top.tsx": "const ok = (s) => { if (s) {} /`/.test(s) };\n" + `export const A = () => (
  <button onClick={save}><img src="s.png" /></button>
);
export const Z = () => <button onClick={top}><img src="z.png" /></button>;
`,
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

func TestMisreadRegexInJSXKeepsWhatFollows(t *testing.T) {
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

func TestFixAfterUnclosedAttribute(t *testing.T) {
	src := `export const A = ({ k, s }) => (
  <div>
    <button onClick={() => { switch (k) { case 1: {} /'/.test(s) } }} data-testid="run"><img src="r.png" /></button>
    <button onClick={save}><img src="s.png" /></button>
  </div>
);
`
	dir := write(t, map[string]string{"web/A.tsx": src})
	rep := fixDir(t, dir, false)
	expectAdded(t, rep)
	if got := read(t, dir+"/web/A.tsx"); got != src {
		t.Errorf("changed the file:\n%s", got)
	}
	expectLeft(t, rep, "3 could not all be read", "4 comes after a {…} that does not close")
}

func fixUnchanged(t *testing.T, files map[string]string, want ...string) {
	t.Helper()
	dir := write(t, files)
	rep := fixDir(t, dir, false)
	if len(rep.Fixed) != 0 {
		t.Errorf("fixed %v, want nothing", added(rep))
	}
	for path, src := range files {
		if got := read(t, filepath.Join(dir, path)); got != src {
			t.Errorf("%s changed:\n%s", path, got)
		}
	}
	var left []string
	for _, l := range rep.Left {
		rel, _ := filepath.Rel(dir, l.File)
		left = append(left, fmt.Sprintf("%s:%d", filepath.ToSlash(rel), l.Line))
	}
	sort.Strings(left)
	sort.Strings(want)
	if strings.Join(left, " ") != strings.Join(want, " ") {
		t.Errorf("left %v, want %v", left, want)
	}
}

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

func TestFixLeavesCodeOfUnclosedChild(t *testing.T) {
	tick := "`"
	fixUnchanged(t, map[string]string{
		"web/Deeper.tsx": `export const A = ({ a, n, k, s }) => (
  <div>
    {(() => {
      switch (k) { case 1: {} /` + tick + `/.test(s) }
      if (i<a) return null
      const ok = n<button; return ok
    })()}
    <button onClick={mid}><img src="m.png" /></button>
  </div>
);
`,
		"web/Same.tsx": `export const A = ({k, s, n, a}) => (
  <div>
    {(() => { switch (k) { case 1: {} /` + tick + `/.test(s) }
    if (i<a) return null
    const ok = n<button; return ok
    })()}
    <button onClick={after}><img/></button>
  </div>
)
`,
		"web/Same2.tsx": `export const A = ({k, s, n, a}) => (
  <div>
    {(() => {
    switch (k) { case 1: {} /` + tick + `/.test(s) }
    if (i<a) return null
    const ok = n<button; return ok
    })()}
    <button onClick={after}><img/></button>
  </div>
)
`,
		"web/Mixed.tsx": "export const A = ({k,s}) => (\n        <div>\n        {(() => { switch (k) { case 1: {} /`/.test(s) }\n\t  if (i<a) return null\n\t  })()}\n        <button onClick={after}><img/></button>\n        </div>\n)\n",
		"web/Less.tsx": `export const A = ({k, s}) => (
      <div>
        {(() => { switch (k) { case 1: {} /` + tick + `/.test(s) }
  if (i<a) return null
  return null
})()}
        <button onClick={after}><img/></button>
      </div>
)
`,
		"web/Col0.tsx": `export const A = ({k, s}) => (
  <div>
    {(() => { switch (k) { case 1: {} /` + tick + `/.test(s) }
      const x = "a"
if (i<a) return null
      return null
    })()}
    <button onClick={after}><img/></button>
  </div>
)
`,
	}, "web/Col0.tsx:8", "web/Deeper.tsx:8", "web/Less.tsx:7", "web/Mixed.tsx:6", "web/Same.tsx:7", "web/Same2.tsx:8")
}

func TestTagAfterUnendedString(t *testing.T) {
	fixUnchanged(t, map[string]string{
		"web/A.tsx": `export const A = ({k, s}) => (
  <section>
  <div>{(() => { switch (k) { case 1: {} /'/.test(s) } })()}
    <button onClick={inner}><img src="i.png" /></button>
    <button onClick={inner2}><img src="i.png" /></button>
  </div>
  <button onClick={after}><img/></button>
  </section>
)
`,
	}, "web/A.tsx:4", "web/A.tsx:5", "web/A.tsx:7")
}

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
	expectLeft(t, rep, "5 comes after a {…} that does not close", "5 comes after a {…} that does not close",
		"5 .map() repeats it", "2 .map() repeats it")
}

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

func TestUnclosedMapOutsideJSX(t *testing.T) {
	dir := write(t, map[string]string{
		"web/a.html": `<html><body>
<p>Don't call items.map( here</p>
<button onclick="save()"><img src="s.png"></button>
</body></html>
`,
		"web/S.svelte": `<script>
  const hint = "use items.map(";
</script>
<div>
  <button on:click={save}><img src="s.png" /></button>
</div>
`,
		"web/C2.vue": `<template>
  <div>
    <!-- TODO: items.map( -->
    <button @click="save"><img src="s.png" /></button>
  </div>
</template>
`,
		"web/C3.vue": `<script setup>
// was: rows.map((r) => {
const a = 1
</script>
<template>
  <div>
    <button @click="save"><img src="s.png" /></button>
  </div>
</template>
`,
		"web/C4.vue": `<template>
  <div>
    <!-- was: items.map((it) => it.name) -->
    <button @click="save"><img src="s.png" /></button>
  </div>
</template>
`,
		"app/Screen.kt": `package x

import androidx.compose.material3.*
import androidx.compose.runtime.Composable

@Composable
fun Screen(names: List<String>, a: Boolean) {
    Box(Modifier.semantics { testTagsAsResourceId = true }) {
        val label = names.map("${if (a) "(" else ")"}x")
        IconButton(onClick = { save() }) {
            Icon(Icons.Default.Save, contentDescription = null)
        }
    }
}
`,
	})
	rep := fixDir(t, dir, true)
	if len(rep.Fixed) != 6 || len(rep.Left) != 0 {
		t.Errorf("fixed %v, left %+v; want the six controls fixed", added(rep), rep.Left)
	}
}
