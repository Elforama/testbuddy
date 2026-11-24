package a11y

import (
	"regexp"
	"sort"
	"strings"
)

type loop struct {
	from, to int
	by       string
}

var (
	jsLoopCall = regexp.MustCompile(`\.\s*(map|flatMap|forEach)\s*\(`)
	ktLoopCall = regexp.MustCompile(`\b(items|itemsIndexed|forEach|forEachIndexed|map|mapIndexed|repeat|for)\s*[({]`)
	svelteEach = regexp.MustCompile(`\{\s*([#/])each\b`)
)

func callLoops(src string, re *regexp.Regexp, by func(name string) string, match func(string, int) int, inLit func(int) bool, unclosedRuns bool) []loop {
	var out []loop
	for _, m := range re.FindAllStringSubmatchIndex(src, -1) {
		if inLit != nil && inLit(m[0]) {
			continue
		}
		name := src[m[2]:m[3]]
		i := m[1] - 1
		end := -1
		if src[i] == '(' {
			if end = match(src, i); end < 0 {
				if unclosedRuns {
					out = append(out, loop{m[0], len(src), by(name)})
				}
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

func jsLoops(src string, inLit func(int) bool) []loop {
	return callLoops(src, jsLoopCall, jsLoopName, jsMatcher(src), inLit, inLit != nil)
}

func jsLoopName(n string) string { return "." + n + "()" }

func pageLoops(src string) []loop {
	return callLoops(src, jsLoopCall, jsLoopName, jsMatcher(src), inHTMLComment(src), false)
}

func inHTMLComment(src string) func(int) bool {
	var spans []span
	for k := 0; ; {
		e := strings.Index(src[k:], "<!--")
		if e < 0 {
			break
		}
		from := k + e
		end := len(src)
		if c := strings.Index(src[from+4:], "-->"); c >= 0 {
			end = from + 4 + c + 3
		}
		spans = append(spans, span{from, end})
		k = end
	}
	return func(off int) bool {
		i := sort.Search(len(spans), func(i int) bool { return spans[i].to > off })
		return i < len(spans) && spans[i].from <= off
	}
}

func ktLoops(src string) []loop {
	return callLoops(src, ktLoopCall, func(n string) string {
		if n == "for" {
			return "a for loop"
		}
		return n + " { }"
	}, matching, func(off int) bool { return inLiteral(src, off) }, false)
}

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

func loopAt(loops []loop, off int) string {
	for _, l := range loops {
		if off > l.from && off < l.to {
			return l.by
		}
	}
	return ""
}

var templateLoops = []string{"v-for", "*ngFor", "x-for"}

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

func perItem(by, selector string) string {
	return "needs a per-item id: " + by + " repeats it, so one fixed " + selector +
		" would be the same on every item and match whichever tb meets first; build it from the item (its id or key)"
}

func perCall(attr, selector string) string {
	return "may need a per-item id: the " + attr + "={…} callback can render it once per item (as FlatList's renderItem does), so one fixed " +
		selector + " could be the same on every item; build it from the item (its id or key), or add one by hand if it renders once"
}
