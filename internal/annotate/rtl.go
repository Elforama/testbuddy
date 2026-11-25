package annotate

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/bidi"
)

type arabicForms struct {
	base rune
	n    int
}

var arabic = func() map[rune]arabicForms {
	m := map[rune]arabicForms{0x0621: {0xFE80, 1}}
	next := rune(0xFE81)
	for r := rune(0x0622); r <= 0x064A; r++ {
		if r >= 0x063B && r <= 0x0640 {
			continue
		}
		n := 4
		switch r {
		case 0x0622, 0x0623, 0x0624, 0x0625, 0x0627, 0x0629, 0x062F, 0x0630, 0x0631, 0x0632, 0x0648, 0x0649:
			n = 2
		}
		m[r] = arabicForms{next, n}
		next += rune(n)
	}
	return m
}()

var lamAlef = map[rune]rune{0x0622: 0xFEF5, 0x0623: 0xFEF7, 0x0625: 0xFEF9, 0x0627: 0xFEFB}

const tatweel = 0x0640

func transparent(r rune) bool { return unicode.Is(unicode.Mn, r) }

func joinsNext(r rune) bool { return r == tatweel || arabic[r].n == 4 }

func joinsPrev(r rune) bool { return r == tatweel || arabic[r].n >= 2 }

func shapeArabic(s string) string {
	rs := []rune(s)
	near := func(i, step int) rune {
		for j := i + step; j >= 0 && j < len(rs); j += step {
			if !transparent(rs[j]) {
				return rs[j]
			}
		}
		return 0
	}
	var b strings.Builder
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		f, ok := arabic[r]
		if !ok {
			b.WriteRune(r)
			continue
		}
		prev := joinsNext(near(i, -1))
		if r == 0x0644 && i+1 < len(rs) {
			if lig, ok := lamAlef[rs[i+1]]; ok {
				if prev {
					lig++
				}
				b.WriteRune(lig)
				i++
				continue
			}
		}
		next := f.n == 4 && joinsPrev(near(i, 1))
		form := 0
		switch {
		case f.n == 1:
		case prev && next:
			form = 3
		case prev:
			form = 1
		case next:
			form = 2
		}
		b.WriteRune(f.base + rune(form))
	}
	return b.String()
}

var mirror = map[rune]rune{'(': ')', ')': '(', '[': ']', ']': '[', '{': '}', '}': '{', '<': '>', '>': '<', '«': '»', '»': '«'}

func reverseRun(s string) string {
	var clusters [][]rune
	for _, r := range s {
		if k := len(clusters); k > 0 && transparent(r) {
			clusters[k-1] = append(clusters[k-1], r)
			continue
		}
		if m, ok := mirror[r]; ok {
			r = m
		}
		clusters = append(clusters, []rune{r})
	}
	var b strings.Builder
	for i := len(clusters) - 1; i >= 0; i-- {
		b.WriteString(string(clusters[i]))
	}
	return b.String()
}

func visual(s string) string {
	s = shapeArabic(s)
	rtl := false
	for _, r := range s {
		if unicode.In(r, unicode.Hebrew, unicode.Arabic, unicode.Syriac, unicode.Thaana, unicode.Nko) {
			rtl = true
			break
		}
	}
	if !rtl {
		return s
	}
	var p bidi.Paragraph
	if _, err := p.SetString(s, bidi.DefaultDirection(bidi.LeftToRight)); err != nil {
		return s
	}
	o, err := p.Order()
	if err != nil {
		return s
	}
	var b strings.Builder
	for i := 0; i < o.NumRuns(); i++ {
		run := o.Run(i)
		t := run.String()
		if run.Direction() != bidi.RightToLeft {
			b.WriteString(t)
			continue
		}
		core := strings.TrimRight(t, " ")
		lead := len(core) - len(strings.TrimLeft(core, " "))
		b.WriteString(core[:lead] + reverseRun(core[lead:]) + t[len(core):])
	}
	return b.String()
}
