package a11y

import (
	"regexp"
	"strings"
)

// Elements a loop repeats: --fix cannot give them a fixed id, since every item would get
// the same one and an id: selector would match whichever item tb meets first (issue #60).

// loop is a stretch of source that a loop repeats, and what repeats it.
type loop struct {
	from, to int
	by       string // ".map()", "items { }", "a for loop", "{#each}", ...
}

var (
	// jsLoopCall: an array method whose callback makes one element per item.
	jsLoopCall = regexp.MustCompile(`\.\s*(map|flatMap|forEach)\s*\(`)
	// ktLoopCall: Compose's lazy list builders, Kotlin's collection loops, repeat and for.
	ktLoopCall = regexp.MustCompile(`\b(items|itemsIndexed|forEach|forEachIndexed|map|mapIndexed|repeat|for)\s*[({]`)
	svelteEach = regexp.MustCompile(`\{\s*([#/])each\b`)
)

// callLoops are the loops re finds: from the loop's name through its parentheses and the
// lambda or block after them (for a for loop without braces, to the end of its line).
// match finds the bracket that closes one: jsMatching for JavaScript, whose JSX text is
// not code (issue #180).
func callLoops(src string, re *regexp.Regexp, by func(name string) string, match func(string, int) int) []loop {
	var out []loop
	for _, m := range re.FindAllStringSubmatchIndex(src, -1) {
		if inLiteral(src, m[0]) {
			continue
		}
		name := src[m[2]:m[3]]
		i := m[1] - 1
		end := -1
		if src[i] == '(' {
			if end = match(src, i); end < 0 {
				continue
			}
			i = end
			for i < len(src) && isSpace(src[i]) {
				i++
			}
		}
		if i < len(src) && src[i] == '{' {
			if e := match(src, i); e > 0 {
				end = e
			}
		} else if name == "for" {
			if e := strings.IndexByte(src[i:], '\n'); e >= 0 {
				end = i + e
			} else {
				end = len(src)
			}
		}
		if end > 0 {
			out = append(out, loop{m[0], end, by(name)})
		}
	}
	return out
}

// jsLoops are the .map() (and flatMap, forEach) calls of JavaScript or JSX.
func jsLoops(src string) []loop {
	return callLoops(src, jsLoopCall, func(n string) string { return "." + n + "()" }, jsMatching)
}

// ktLoops are the loops of Kotlin and Compose: items { }, forEach { }, repeat, for, ...
func ktLoops(src string) []loop {
	return callLoops(src, ktLoopCall, func(n string) string {
		if n == "for" {
			return "a for loop"
		}
		return n + " { }"
	}, matching)
}

// eachBlocks are Svelte's {#each} … {/each} blocks.
func eachBlocks(src string) []loop {
	var out []loop
	var open []int
	for _, m := range svelteEach.FindAllStringSubmatchIndex(src, -1) {
		if src[m[2]] == '#' {
			open = append(open, m[0])
		} else if len(open) > 0 {
			out = append(out, loop{open[len(open)-1], m[1], "{#each}"})
			open = open[:len(open)-1]
		}
	}
	return out
}

// loopAt says what repeats the source at off, or "".
func loopAt(loops []loop, off int) string {
	for _, l := range loops {
		if off > l.from && off < l.to {
			return l.by
		}
	}
	return ""
}

// templateLoops are the attributes that repeat a tag in a template: Vue, Angular, Alpine.
var templateLoops = []string{"v-for", "*ngFor", "x-for"}

// loopAround says what repeats a tag: a loop attribute on it or on a tag around it, or a
// loop in the source around it. "" when nothing does.
func loopAround(t *tag, loops []loop) string {
	for d := t; d != nil; d = d.parent {
		for _, a := range templateLoops {
			if d.has(a) {
				return a
			}
		}
	}
	return loopAt(loops, t.off)
}

// perItem is why an element a loop repeats is left: it needs an id made from each item.
func perItem(by, selector string) string {
	return "needs a per-item id: " + by + " repeats it, so one fixed " + selector +
		" would be the same on every item and match whichever tb meets first; build it from the item (its id or key)"
}

// perCall is why an element a callback attribute renders is left: renderItem={…} runs once
// per item, and a render prop may too, so it may need an id made from each item (issue #147).
func perCall(attr, selector string) string {
	return "may need a per-item id: the " + attr + "={…} callback can render it once per item (as FlatList's renderItem does), so one fixed " +
		selector + " could be the same on every item; build it from the item (its id or key), or add one by hand if it renders once"
}
