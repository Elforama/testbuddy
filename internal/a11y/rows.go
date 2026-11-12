package a11y

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Rows a loop elsewhere repeats (issue #147). loops.go leaves the elements a loop repeats
// in its own source; but a loop often repeats a component defined apart: a row component in
// another file (items.map(i => <Row/>), items(posts) { PostRow(it) }), a function passed as
// renderItem={renderRow}, a RecyclerView row layout an adapter inflates. One fixed id in
// such a row would be the same on every item too. So the project is read first for what
// its loops and list callbacks render, by name, and an element inside a definition of one
// of those names (or of what they render in turn) is left for a person.
//
// Names are matched without following imports: a name repeated in one place leaves every
// definition of it, which can only leave an element for a person, never give a repeated one
// a fixed id.

// rowWhy says what repeats a component or a row layout.
type rowWhy struct {
	via   string // the component the loop repeats, which renders this one ("": this one)
	by    string // what repeats it: ".map()", "v-for", "renderItem={…}", "items { }", "ContactAdapter", "tools:listitem"
	at    string // where that is: "List.tsx:9"
	maybe bool   // a callback that can render it once per item, not a loop that surely does
}

// def is a named definition (a component, a function, a composable) and where it is.
type def struct {
	name     string
	from, to int
}

// use is a name a definition renders or a loop repeats: <Row/>, PostRow(it), renderRow.
type use struct {
	name string
	off  int
	why  *rowWhy // what repeats it right here; nil when nothing does
}

// rows are the names of what a loop or list callback repeats, and why.
type rows map[string]rowWhy

// in is the innermost of defs around off whose name is repeated, and why; "" if none.
func (r rows) in(defs []def, off int) (string, rowWhy) {
	name, size := "", -1
	var why rowWhy
	for _, d := range defs {
		if off <= d.from || off >= d.to {
			continue
		}
		if w, ok := r[d.name]; ok && (size < 0 || d.to-d.from < size) {
			name, size, why = d.name, d.to-d.from, w
		}
	}
	return name, why
}

// spread adds to r what the definitions of names in r render, until nothing changes: a
// row's own components are repeated with it. files: each file's definitions and uses. Each
// name is added once, and each definition's uses are read once, when its name is added: a
// worklist, so a long chain of components (or a cycle) costs no more than the uses.
func (r rows) spread(files []rowFile) {
	type at struct{ file, def int }
	defsOf := map[string][]at{}
	for fi, f := range files {
		for di, d := range f.defs {
			defsOf[d.name] = append(defsOf[d.name], at{fi, di})
		}
	}
	var work []string
	add := func(name string, w rowWhy) {
		if _, ok := r[name]; !ok {
			r[name] = w
			work = append(work, name)
		}
	}
	for name := range r {
		work = append(work, name)
	}
	sort.Strings(work)
	for _, f := range files {
		for _, u := range f.uses {
			if u.why != nil {
				add(u.name, *u.why)
			}
		}
	}
	for len(work) > 0 {
		name := work[0]
		work = work[1:]
		w := r[name]
		if w.via == "" {
			w.via = name
		}
		for _, a := range defsOf[name] {
			f := files[a.file]
			d := f.defs[a.def]
			// The uses inside d: f.uses is in offset order.
			i := sort.Search(len(f.uses), func(i int) bool { return f.uses[i].off > d.from })
			for ; i < len(f.uses) && f.uses[i].off < d.to; i++ {
				add(f.uses[i].name, w)
			}
		}
	}
}

// rowFile is one file's definitions and uses (in offset order).
type rowFile struct {
	defs []def
	uses []use
}

// sorted puts f's uses in offset order.
func (f rowFile) sorted() rowFile {
	sort.SliceStable(f.uses, func(i, j int) bool { return f.uses[i].off < f.uses[j].off })
	return f
}

// perItemIn is why an element inside def, which a loop elsewhere repeats, is left.
func perItemIn(def string, w rowWhy, selector string) string {
	what, in := def, "it is in "+def
	if w.via != "" {
		what = w.via
		in += ", which " + w.via + " renders,"
	}
	if w.maybe {
		return "may need a per-item id: " + in + " and the " + w.by + " callback in " + w.at + " can render " + what +
			" once per item (as FlatList's renderItem does), so one fixed " + selector +
			" could be the same on every item; build it from the item (its id or key), or add one by hand if it renders once"
	}
	return "needs a per-item id: " + in + " and the " + w.by + " in " + w.at + " repeats " + what + ", so one fixed " +
		selector + " would be the same on every item and match whichever tb meets first; build it from the item (its id or key)"
}

// perRow is why a view in a row layout is left.
func perRow(w rowWhy) string {
	who := w.by + " (" + w.at + ") inflates it once per item"
	if w.by == "tools:listitem" {
		who = "a list previews it with tools:listitem (" + w.at + "), a sign it is inflated once per item"
	}
	return "needs a per-item id: it is a row layout: " + who + ", so one fixed android:id would be the same on every row " +
		"and match whichever tb meets first; set a contentDescription from the item where the row is bound, or add an id by hand if flows find the row first"
}

// lines tells the line of an offset in one source, as "List.tsx:9". Where each line
// starts is found once, when first asked, not by counting from the start for each offset:
// a minified file is one long line read for thousands of uses.
type lines struct {
	path, src string
	starts    []int
}

func (l *lines) at(off int) string {
	if l.starts == nil {
		l.starts = []int{0}
		for i := 0; i < len(l.src); i++ {
			if l.src[i] == '\n' {
				l.starts = append(l.starts, i+1)
			}
		}
	}
	n := sort.Search(len(l.starts), func(i int) bool { return l.starts[i] > off })
	return fmt.Sprintf("%s:%d", filepath.Base(l.path), n)
}

// JavaScript and JSX.

var (
	jsFuncDef  = regexp.MustCompile(`\bfunction\b\s*\*?\s*([A-Za-z_$][\w$]*)\s*(?:<[^<>()]*>\s*)?\(`)
	jsVarDef   = regexp.MustCompile(`\b(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*(?::[^=;]*)?=`)
	jsClassDef = regexp.MustCompile(`\bclass\s+([A-Za-z_$][\w$]*)[^{;]*\{`)
	// jsFieldDef: a class field, renderRow = …, with spaces around its "=": a JSX attribute
	// on its own line (title="x", data={items}) has none.
	jsFieldDef  = regexp.MustCompile(`(?m)^[ \t]*(?:(?:public|private|protected|static|readonly)\s+)*([A-Za-z_$][\w$]*)(?:\s*:[^=;\n]*)?[ \t]+=[ \t]`)
	jsMethodDef = regexp.MustCompile(`(?m)^[ \t]*(?:(?:public|private|protected|static|async|override)\s+)*([A-Za-z_$][\w$]*)\s*\([^()]*\)\s*(?::[^{;\n]*)?\{`)
	// jsMapRef: a loop given a function by name, items.map(renderRow).
	jsMapRef = regexp.MustCompile(`\.\s*(map|flatMap)\s*\(\s*([A-Za-z_$][\w$]*(?:\.[A-Za-z_$][\w$]*)*)\s*\)`)
)

// jsDefs are the definitions of a JavaScript or JSX source: functions, classes and their
// methods, and the values of const, let, var and class fields that are functions or calls
// (memo(…), useCallback(…)). root is the source's parse; inLit tells its strings and comments.
func jsDefs(src string, root *tag, inLit func(int) bool) []def {
	r := &jsDefReader{src: src, match: jsMatcher(src), ends: map[int]int{}}
	root.walkAll(func(t *tag) bool {
		if t != root && t.attr == "" {
			r.ends[t.off] = t.end
		}
		return true
	})
	var out []def
	add := func(m []int, end int) {
		if end > m[1] {
			out = append(out, def{src[m[2]:m[3]], m[0], end})
		}
	}
	for _, m := range jsFuncDef.FindAllStringSubmatchIndex(src, -1) {
		if !inLit(m[0]) {
			add(m, r.body(r.match(src, m[1]-1)))
		}
	}
	for _, re := range []*regexp.Regexp{jsVarDef, jsFieldDef} {
		for _, m := range re.FindAllStringSubmatchIndex(src, -1) {
			if inLit(m[0]) || jsKeywords[src[m[2]:m[3]]] || (m[1] < len(src) && (src[m[1]] == '=' || src[m[1]] == '>')) {
				continue
			}
			add(m, r.value(m[1]))
		}
	}
	for _, re := range []*regexp.Regexp{jsClassDef, jsMethodDef} {
		for _, m := range re.FindAllStringSubmatchIndex(src, -1) {
			if inLit(m[0]) || jsKeywords[src[m[2]:m[3]]] || jsStatements[src[m[2]:m[3]]] {
				continue
			}
			add(m, r.match(src, m[1]-1))
		}
	}
	return out
}

// jsStatements start a block, not a method.
var jsStatements = map[string]bool{"else": true, "do": true, "try": true, "with": true, "class": true}

type jsDefReader struct {
	src   string
	match func(string, int) int
	ends  map[int]int // a tag's '<' -> its end
}

func (r *jsDefReader) space(i int) int {
	for i < len(r.src) && isSpace(r.src[i]) {
		i++
	}
	return i
}

// word: the word w starts at i.
func (r *jsDefReader) word(i int, w string) bool {
	s := r.src
	return strings.HasPrefix(s[i:], w) && (i+len(w) == len(s) || !isIdentChar(s[i+len(w)]))
}

func isIdentChar(c byte) bool { return isLetter(c) || c >= '0' && c <= '9' || c == '$' }

// value is the end of the value at i, when it is a function or a call or JSX, else -1.
func (r *jsDefReader) value(i int) int {
	s := r.src
	if i = r.space(i); i >= len(s) {
		return -1
	}
	if r.word(i, "async") {
		i = r.space(i + 5)
	}
	if r.word(i, "function") {
		k := strings.IndexByte(s[i:], '(')
		if k < 0 {
			return -1
		}
		return r.body(r.match(s, i+k))
	}
	switch s[i] {
	case '{', '[':
		return r.match(s, i)
	case '<':
		if e, ok := r.ends[i]; ok {
			return e
		}
		return -1
	case '(':
		e := r.match(s, i)
		if a := r.arrow(e); a >= 0 {
			return r.value(a)
		}
		return e
	}
	j := i
	for j < len(s) && (isIdentChar(s[j]) || s[j] == '.') {
		j++
	}
	if j == i {
		return -1
	}
	k := r.space(j)
	if strings.HasPrefix(s[k:], "=>") { // item => …
		return r.value(k + 2)
	}
	if k < len(s) && s[k] == '(' { // memo(…), useCallback(…), forwardRef(…)
		return r.match(s, k)
	}
	return -1
}

// arrow is the offset past the "=>" after parameters that end at e (a return type between
// them allowed), or -1 when they are no arrow function's.
func (r *jsDefReader) arrow(e int) int {
	if e < 0 {
		return -1
	}
	s := r.src
	k := r.space(e)
	if k < len(s) && s[k] == ':' {
		end := lineEnd(s, k, len(s))
		a := strings.Index(s[k:end], "=>")
		if a < 0 {
			return -1
		}
		k += a
	}
	if strings.HasPrefix(s[k:], "=>") {
		return k + 2
	}
	return -1
}

// body is the end of a function's body, whose parameters end at e.
func (r *jsDefReader) body(e int) int {
	if e < 0 {
		return -1
	}
	k := strings.IndexByte(r.src[e:], '{')
	if k < 0 {
		return -1
	}
	return r.match(r.src, e+k)
}

// componentName is the component a tag renders: <Row/> gives Row, and a Vue template's
// <todo-item> TodoItem. A namespaced one keeps its namespace, <Menu.Item/> Menu.Item, which
// no definition of a bare Item matches. "" for an HTML element (and <motion.div>).
func componentName(n string) string {
	if i := strings.LastIndexByte(n, '.'); i >= 0 {
		if componentName(n[i+1:]) == "" {
			return ""
		}
		return n
	}
	if n == "" {
		return ""
	}
	if strings.Contains(n, "-") {
		var b strings.Builder
		for _, w := range strings.Split(n, "-") {
			if w != "" {
				b.WriteString(strings.ToUpper(w[:1]) + w[1:])
			}
		}
		return b.String()
	}
	if n[0] >= 'A' && n[0] <= 'Z' {
		return n
	}
	return ""
}

// identValue is the function an attribute names, {renderRow} or {this.renderRow}: renderRow.
func identValue(v string) string {
	v = strings.TrimSpace(v)
	if !strings.HasPrefix(v, "{") || !strings.HasSuffix(v, "}") {
		return ""
	}
	v = strings.TrimSpace(v[1 : len(v)-1])
	if !identPath.MatchString(v) {
		return ""
	}
	return v[strings.LastIndexByte(v, '.')+1:]
}

// jsPage is a JavaScript, JSX, Vue or Svelte source read for its tags, loops and
// definitions: what checkWeb and checkReactNative read too.
type jsPage struct {
	root  *tag
	loops []loop // its .map() calls (and flatMap, forEach)
	each  []loop // its {#each} blocks
	defs  []def
	inLit func(int) bool // the strings and comments of its code, as far as they were read
	src   string         // the source read, when project.load keeps the page
}

func readJSPage(path, src string) jsPage {
	var pg jsPage
	switch strings.ToLower(filepath.Ext(path)) {
	case ".html", ".htm":
		pg.root = parseHTMLPage(src)
		pg.loops = pageLoops(src)
		pg.inLit = inHTMLComment(src)
	case ".vue", ".svelte":
		// A single-file component is one definition, named after its file.
		pg.root = parseMarkup(src, true)
		pg.loops = pageLoops(src)
		pg.inLit = inHTMLComment(src)
		pg.defs = []def{{componentName(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))), 0, len(src) + 1}}
	default:
		pg.root, pg.inLit = parseSource(src, false)
		pg.loops = jsLoops(src, pg.inLit)
		pg.defs = jsDefs(src, pg.root, pg.inLit)
	}
	pg.each = eachBlocks(src)
	return pg
}

// listRenderProps are the props a list calls once per item (or per section): FlatList,
// SectionList, VirtualizedList, ListView and swipe lists. Another render prop, such as a form
// field's render={…}, renders once: what it renders is not repeated (the element right
// inside such a callback is still left for a person, by perCall).
var listRenderProps = map[string]bool{"renderItem": true, "renderSectionHeader": true, "renderSectionFooter": true,
	"renderHiddenItem": true, "renderRow": true, "renderSeparator": true, "renderCell": true}

// jsRowFile reads a JavaScript, JSX, Vue or Svelte source (read as pg) for its definitions
// and the components and functions they render, with what repeats them right there.
func jsRowFile(path, src string, pg jsPage) rowFile {
	pos := &lines{path: path, src: src}
	f := rowFile{defs: pg.defs}
	loops := append(append([]loop(nil), pg.loops...), pg.each...)
	pg.root.walkAll(func(t *tag) bool {
		if t == pg.root || t.attr != "" {
			return true
		}
		if name := componentName(t.name); name != "" {
			u := use{name: name, off: t.off}
			if by := loopAround(t, loops); by != "" {
				u.why = &rowWhy{by: by, at: pos.at(t.off)}
			} else if cb := callbackAround(src, t); listRenderProps[cb] {
				u.why = &rowWhy{by: cb + "={…}", at: pos.at(t.off), maybe: true}
			}
			f.uses = append(f.uses, u)
		}
		// renderItem={renderRow}: a function a list calls, maybe once per item.
		var attrs []string
		for a := range t.attrs {
			if listRenderProps[a] {
				attrs = append(attrs, a)
			}
		}
		sort.Strings(attrs)
		for _, a := range attrs {
			if n := identValue(t.attrs[a]); n != "" {
				f.uses = append(f.uses, use{n, t.off, &rowWhy{by: a + "={…}", at: pos.at(t.off), maybe: true}})
			}
		}
		return true
	})
	for _, m := range jsMapRef.FindAllStringSubmatchIndex(src, -1) {
		if pg.inLit(m[0]) {
			continue
		}
		n := src[m[4]:m[5]]
		n = n[strings.LastIndexByte(n, '.')+1:]
		f.uses = append(f.uses, use{n, m[0], &rowWhy{by: "." + src[m[2]:m[3]] + "()", at: pos.at(m[0])}})
	}
	return f.sorted()
}

// Kotlin.

var (
	ktFunDef  = regexp.MustCompile(`\bfun\s+(?:<[^>]*>\s*)?(?:[\w.]+\.)?([A-Za-z_]\w*)\s*\(`)
	ktCallUse = regexp.MustCompile(`\b([A-Z]\w*)\s*[({]`)
)

// ktDefs are a Kotlin source's functions, with a block body or an expression one.
func ktDefs(src string) []def {
	var out []def
	for _, m := range ktFunDef.FindAllStringSubmatchIndex(src, -1) {
		if inLiteral(src, m[0]) {
			continue
		}
		e := matching(src, m[1]-1)
		if e < 0 {
			continue
		}
		// After the parameters and the return type: a block, or "=" and an expression.
		i := e
		for i < len(src) && src[i] != '{' && src[i] != '=' && src[i] != '\n' {
			i++
		}
		if i < len(src) && src[i] == '\n' { // the return type, or the body, on the next line
			j := i
			for j < len(src) && isSpace(src[j]) {
				j++
			}
			if j < len(src) && (src[j] == '{' || src[j] == '=') {
				i = j
			}
		}
		end := -1
		switch {
		case i >= len(src):
		case src[i] == '{':
			end = matching(src, i)
		case src[i] == '=':
			end = ktExprEnd(src, i+1)
		}
		if end > m[1] {
			out = append(out, def{src[m[2]:m[3]], m[0], end})
		}
	}
	return out
}

// ktExprEnd is the end of the expression at i: its line, with the brackets opened on it.
func ktExprEnd(src string, i int) int {
	for i < len(src) && isSpace(src[i]) {
		i++
	}
	for i < len(src) && src[i] != '\n' {
		if j := skipLiteral(src, i); j != i {
			i = j
			continue
		}
		if c := src[i]; c == '(' || c == '{' || c == '[' {
			e := matching(src, i)
			if e < 0 {
				return len(src)
			}
			i = e
			continue
		}
		i++
	}
	return i
}

// ktRowFile reads a Compose source for its functions and the composables they call, with
// the loop that repeats each call right there.
func ktRowFile(path, src string) rowFile {
	pos := &lines{path: path, src: src}
	f := rowFile{defs: ktDefs(src)}
	loops := ktLoops(src)
	for _, m := range ktCallUse.FindAllStringSubmatchIndex(src, -1) {
		if inLiteral(src, m[0]) || (m[0] > 0 && src[m[0]-1] == '.') {
			continue
		}
		u := use{name: src[m[2]:m[3]], off: m[0]}
		if by := loopAt(loops, m[0]); by != "" {
			u.why = &rowWhy{by: by, at: pos.at(m[0])}
		}
		f.uses = append(f.uses, u)
	}
	return f.sorted()
}

// Android row layouts.

var (
	classDecl   = regexp.MustCompile(`\bclass\s+(\w+)`)
	rowBase     = regexp.MustCompile(`(?:Adapter|ViewHolder|BindableItem)$`)
	layoutRef   = regexp.MustCompile(`\bR\.layout\.(\w+)`)
	bindingRef  = regexp.MustCompile(`\b([A-Z]\w*)Binding\b`)
	listItemRef = regexp.MustCompile(`\btools:listitem\s*=\s*"@layout/(\w+)"`)
)

// adapterLayouts adds to r the layouts the adapters and view holders of a Kotlin or Java
// source inflate, by R.layout or by their view binding (ItemContactBinding: item_contact).
func adapterLayouts(r rows, path, src string) {
	pos := &lines{path: path, src: src}
	for _, m := range classDecl.FindAllStringSubmatchIndex(src, -1) {
		if inLiteral(src, m[0]) {
			continue
		}
		// The header: up to the body's '{', or the next class when it has none.
		end := len(src)
		if n := classDecl.FindStringIndex(src[m[1]:]); n != nil {
			end = m[1] + n[0]
		}
		body := strings.IndexByte(src[m[1]:end], '{')
		if body >= 0 {
			end = m[1] + body
		}
		if !rowClass(src[m[1]:end]) {
			continue
		}
		if body >= 0 {
			if e := matching(src, end); e > 0 {
				end = e
			}
		}
		region := src[m[0]:end]
		why := rowWhy{by: src[m[2]:m[3]], at: pos.at(m[0])}
		for _, l := range layoutRef.FindAllStringSubmatch(region, -1) {
			if _, ok := r[l[1]]; !ok {
				r[l[1]] = why
			}
		}
		for _, b := range bindingRef.FindAllStringSubmatch(region, -1) {
			if b[1] == "View" || b[1] == "ViewData" {
				continue
			}
			if _, ok := r[snake(b[1])]; !ok {
				r[snake(b[1])] = why
			}
		}
	}
}

// rowClass: a class header (what follows its name) names a supertype whose own name ends
// in Adapter, ViewHolder or BindableItem: RecyclerView.Adapter<VH>(), ListAdapter<…>,
// RecyclerView.ViewHolder(v). Not what its primary constructor takes (adapter: PostAdapter)
// or its type parameters hold, nor a type nested in an adapter (NoteAdapter.OnNoteListener).
func rowClass(header string) bool {
	// What is at depth 0: the brackets' contents go; a ':' or ',' splits.
	var b strings.Builder
	depth := 0
	for i := 0; i < len(header); i++ {
		switch c := header[i]; c {
		case '(', '<', '[':
			depth++
		case ')', '>', ']':
			if depth > 0 {
				depth--
			}
		case ':', ',':
			if depth == 0 {
				b.WriteByte(',')
			}
		default:
			if depth == 0 {
				b.WriteByte(c)
			}
		}
	}
	// A type starts each part and follows each keyword; the words after it are not types.
	for _, part := range strings.Split(b.String(), ",") {
		typ := true
		for _, w := range strings.Fields(part) {
			switch {
			case w == "extends" || w == "implements":
				typ = true
			case typ && rowBase.MatchString(w[strings.LastIndexByte(w, '.')+1:]):
				return true
			default:
				typ = false
			}
		}
	}
	return false
}

// listItems adds to r the layouts a layout's lists preview with tools:listitem.
func listItems(r rows, path, src string) {
	pos := &lines{path: path, src: src}
	for _, m := range listItemRef.FindAllStringSubmatchIndex(src, -1) {
		name := src[m[2]:m[3]]
		if _, ok := r[name]; !ok {
			r[name] = rowWhy{by: "tools:listitem", at: pos.at(m[0])}
		}
	}
}

// layoutName is an Android layout's resource name: res/layout/item_contact.xml gives item_contact.
func layoutName(path string) string {
	return strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
}
