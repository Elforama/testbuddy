package annotate

import (
	"sort"
	"strings"
	"unicode"
)

var arabicForms = map[rune][4]rune{
	0x0621: {0xFE80, 0x0000, 0x0000, 0x0000},
	0x0622: {0xFE81, 0xFE82, 0x0000, 0x0000},
	0x0623: {0xFE83, 0xFE84, 0x0000, 0x0000},
	0x0624: {0xFE85, 0xFE86, 0x0000, 0x0000},
	0x0625: {0xFE87, 0xFE88, 0x0000, 0x0000},
	0x0626: {0xFE89, 0xFE8A, 0xFE8B, 0xFE8C},
	0x0627: {0xFE8D, 0xFE8E, 0x0000, 0x0000},
	0x0628: {0xFE8F, 0xFE90, 0xFE91, 0xFE92},
	0x0629: {0xFE93, 0xFE94, 0x0000, 0x0000},
	0x062A: {0xFE95, 0xFE96, 0xFE97, 0xFE98},
	0x062B: {0xFE99, 0xFE9A, 0xFE9B, 0xFE9C},
	0x062C: {0xFE9D, 0xFE9E, 0xFE9F, 0xFEA0},
	0x062D: {0xFEA1, 0xFEA2, 0xFEA3, 0xFEA4},
	0x062E: {0xFEA5, 0xFEA6, 0xFEA7, 0xFEA8},
	0x062F: {0xFEA9, 0xFEAA, 0x0000, 0x0000},
	0x0630: {0xFEAB, 0xFEAC, 0x0000, 0x0000},
	0x0631: {0xFEAD, 0xFEAE, 0x0000, 0x0000},
	0x0632: {0xFEAF, 0xFEB0, 0x0000, 0x0000},
	0x0633: {0xFEB1, 0xFEB2, 0xFEB3, 0xFEB4},
	0x0634: {0xFEB5, 0xFEB6, 0xFEB7, 0xFEB8},
	0x0635: {0xFEB9, 0xFEBA, 0xFEBB, 0xFEBC},
	0x0636: {0xFEBD, 0xFEBE, 0xFEBF, 0xFEC0},
	0x0637: {0xFEC1, 0xFEC2, 0xFEC3, 0xFEC4},
	0x0638: {0xFEC5, 0xFEC6, 0xFEC7, 0xFEC8},
	0x0639: {0xFEC9, 0xFECA, 0xFECB, 0xFECC},
	0x063A: {0xFECD, 0xFECE, 0xFECF, 0xFED0},
	0x0641: {0xFED1, 0xFED2, 0xFED3, 0xFED4},
	0x0642: {0xFED5, 0xFED6, 0xFED7, 0xFED8},
	0x0643: {0xFED9, 0xFEDA, 0xFEDB, 0xFEDC},
	0x0644: {0xFEDD, 0xFEDE, 0xFEDF, 0xFEE0},
	0x0645: {0xFEE1, 0xFEE2, 0xFEE3, 0xFEE4},
	0x0646: {0xFEE5, 0xFEE6, 0xFEE7, 0xFEE8},
	0x0647: {0xFEE9, 0xFEEA, 0xFEEB, 0xFEEC},
	0x0648: {0xFEED, 0xFEEE, 0x0000, 0x0000},
	0x0649: {0xFEEF, 0xFEF0, 0xFBE8, 0xFBE9},
	0x064A: {0xFEF1, 0xFEF2, 0xFEF3, 0xFEF4},
	0x0671: {0xFB50, 0xFB51, 0x0000, 0x0000},
	0x0677: {0xFBDD, 0x0000, 0x0000, 0x0000},
	0x0679: {0xFB66, 0xFB67, 0xFB68, 0xFB69},
	0x067A: {0xFB5E, 0xFB5F, 0xFB60, 0xFB61},
	0x067B: {0xFB52, 0xFB53, 0xFB54, 0xFB55},
	0x067E: {0xFB56, 0xFB57, 0xFB58, 0xFB59},
	0x067F: {0xFB62, 0xFB63, 0xFB64, 0xFB65},
	0x0680: {0xFB5A, 0xFB5B, 0xFB5C, 0xFB5D},
	0x0683: {0xFB76, 0xFB77, 0xFB78, 0xFB79},
	0x0684: {0xFB72, 0xFB73, 0xFB74, 0xFB75},
	0x0686: {0xFB7A, 0xFB7B, 0xFB7C, 0xFB7D},
	0x0687: {0xFB7E, 0xFB7F, 0xFB80, 0xFB81},
	0x0688: {0xFB88, 0xFB89, 0x0000, 0x0000},
	0x068C: {0xFB84, 0xFB85, 0x0000, 0x0000},
	0x068D: {0xFB82, 0xFB83, 0x0000, 0x0000},
	0x068E: {0xFB86, 0xFB87, 0x0000, 0x0000},
	0x0691: {0xFB8C, 0xFB8D, 0x0000, 0x0000},
	0x0698: {0xFB8A, 0xFB8B, 0x0000, 0x0000},
	0x06A4: {0xFB6A, 0xFB6B, 0xFB6C, 0xFB6D},
	0x06A6: {0xFB6E, 0xFB6F, 0xFB70, 0xFB71},
	0x06A9: {0xFB8E, 0xFB8F, 0xFB90, 0xFB91},
	0x06AD: {0xFBD3, 0xFBD4, 0xFBD5, 0xFBD6},
	0x06AF: {0xFB92, 0xFB93, 0xFB94, 0xFB95},
	0x06B1: {0xFB9A, 0xFB9B, 0xFB9C, 0xFB9D},
	0x06B3: {0xFB96, 0xFB97, 0xFB98, 0xFB99},
	0x06BA: {0xFB9E, 0xFB9F, 0x0000, 0x0000},
	0x06BB: {0xFBA0, 0xFBA1, 0xFBA2, 0xFBA3},
	0x06BE: {0xFBAA, 0xFBAB, 0xFBAC, 0xFBAD},
	0x06C0: {0xFBA4, 0xFBA5, 0x0000, 0x0000},
	0x06C1: {0xFBA6, 0xFBA7, 0xFBA8, 0xFBA9},
	0x06C5: {0xFBE0, 0xFBE1, 0x0000, 0x0000},
	0x06C6: {0xFBD9, 0xFBDA, 0x0000, 0x0000},
	0x06C7: {0xFBD7, 0xFBD8, 0x0000, 0x0000},
	0x06C8: {0xFBDB, 0xFBDC, 0x0000, 0x0000},
	0x06C9: {0xFBE2, 0xFBE3, 0x0000, 0x0000},
	0x06CB: {0xFBDE, 0xFBDF, 0x0000, 0x0000},
	0x06CC: {0xFBFC, 0xFBFD, 0xFBFE, 0xFBFF},
	0x06D0: {0xFBE4, 0xFBE5, 0xFBE6, 0xFBE7},
	0x06D2: {0xFBAE, 0xFBAF, 0x0000, 0x0000},
	0x06D3: {0xFBB0, 0xFBB1, 0x0000, 0x0000},
}

var lamAlef = map[rune][2]rune{0x0622: {0xFEF5, 0xFEF6}, 0x0623: {0xFEF7, 0xFEF8}, 0x0625: {0xFEF9, 0xFEFA}, 0x0627: {0xFEFB, 0xFEFC}}

const (
	tatweel = 0x0640
	zwnj    = 0x200C
)

func transparent(r rune) bool { return unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) }

func joinsNext(r rune) bool { return r == tatweel || arabicForms[r][2] != 0 }

func joinsPrev(r rune) bool { return r == tatweel || arabicForms[r][1] != 0 }

func shapeArabic(s string) string {
	rs := []rune(s)
	near := func(i, step int) (rune, int) {
		for j := i + step; j >= 0 && j < len(rs); j += step {
			if !transparent(rs[j]) {
				return rs[j], j
			}
		}
		return 0, -1
	}
	var b strings.Builder
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		if r == zwnj {
			continue
		}
		f, ok := arabicForms[r]
		if !ok {
			b.WriteRune(r)
			continue
		}
		p, _ := near(i, -1)
		prev := joinsNext(p)
		n, j := near(i, 1)
		if r == 0x0644 {
			if lig, ok := lamAlef[n]; ok {
				if prev {
					b.WriteRune(lig[1])
				} else {
					b.WriteRune(lig[0])
				}
				b.WriteString(string(rs[i+1 : j]))
				i = j
				continue
			}
		}
		next := f[2] != 0 && joinsPrev(n)
		form := f[0]
		switch {
		case prev && next && f[3] != 0:
			form = f[3]
		case prev && f[1] != 0:
			form = f[1]
		case next && f[2] != 0:
			form = f[2]
		}
		b.WriteRune(form)
	}
	return b.String()
}

type bidiClass int

const (
	classON bidiClass = iota
	classL
	classR
	classAL
	classEN
	classAN
	classNSM
	classES
	classCS
	classET
)

func classOf(r rune) bidiClass {
	switch {
	case transparent(r):
		return classNSM
	case r == tatweel:
		return classAL
	case r == '+' || r == '-' || r == 0x2212:
		return classES
	case r == ',' || r == '.' || r == ':' || r == '/' || r == 0x00A0 || r == 0x060C:
		return classCS
	case strings.ContainsRune("$%#°¢£¤¥€‰‱", r) || r == 0x066A || unicode.Is(unicode.Sc, r):
		return classET
	case r >= '0' && r <= '9', r >= 0x06F0 && r <= 0x06F9, r >= 0xFF10 && r <= 0xFF19:
		return classEN
	case r >= 0x0660 && r <= 0x0669, r == 0x066B, r == 0x066C:
		return classAN
	case unicode.In(r, unicode.Hebrew), r >= 0x07C0 && r <= 0x07FF, r >= 0xFB1D && r <= 0xFB4F:
		return classR
	case unicode.In(r, unicode.Arabic, unicode.Syriac, unicode.Thaana):
		return classAL
	case unicode.IsLetter(r), unicode.Is(unicode.Mc, r):
		return classL
	}
	return classON
}

func brackets(rs []rune, cls []bidiClass) {
	open := map[rune]rune{'(': ')', '[': ']', '{': '}', '«': '»', '‹': '›'}
	type pending struct {
		at    int
		close rune
	}
	var stack []pending
	var pairs [][2]int
	for i, r := range rs {
		if cls[i] != classON {
			continue
		}
		if c, ok := open[r]; ok {
			stack = append(stack, pending{i, c})
			continue
		}
		for k := len(stack) - 1; k >= 0; k-- {
			if stack[k].close == r {
				pairs = append(pairs, [2]int{stack[k].at, i})
				stack = stack[:k]
				break
			}
		}
	}
	sort.Slice(pairs, func(a, b int) bool { return pairs[a][0] < pairs[b][0] })
	dir := func(c bidiClass) bidiClass {
		switch c {
		case classL:
			return classL
		case classR, classEN, classAN:
			return classR
		}
		return classON
	}
	for _, p := range pairs {
		inL, inR := false, false
		for k := p[0] + 1; k < p[1]; k++ {
			switch dir(cls[k]) {
			case classL:
				inL = true
			case classR:
				inR = true
			}
		}
		set := classON
		switch {
		case inL:
			set = classL
		case inR:
			before := classL
			for k := p[0] - 1; k >= 0; k-- {
				if d := dir(cls[k]); d != classON {
					before = d
					break
				}
			}
			set = classL
			if before == classR {
				set = classR
			}
		}
		if set != classON {
			cls[p[0]], cls[p[1]] = set, set
		}
	}
}

var mirror = map[rune]rune{'(': ')', ')': '(', '[': ']', ']': '[', '{': '}', '}': '{', '<': '>', '>': '<', '«': '»', '»': '«', '‹': '›', '›': '‹'}

func visual(s string) string {
	s = shapeArabic(s)
	rs := []rune(s)
	cls := make([]bidiClass, len(rs))
	any := false
	for i, r := range rs {
		cls[i] = classOf(r)
		if cls[i] == classR || cls[i] == classAL {
			any = true
		}
	}
	if !any {
		return s
	}
	for i, c := range cls {
		if c == classNSM {
			if i > 0 {
				cls[i] = cls[i-1]
			} else {
				cls[i] = classON
			}
		}
	}
	last := classL
	for i, c := range cls {
		switch c {
		case classL, classR, classAL:
			last = c
		case classEN:
			if last == classAL {
				cls[i] = classAN
			}
		}
	}
	for i, c := range cls {
		if c == classAL {
			cls[i] = classR
		}
	}
	for i := 1; i+1 < len(cls); i++ {
		a, c, b := cls[i-1], cls[i], cls[i+1]
		if c == classES && a == classEN && b == classEN {
			cls[i] = classEN
		} else if c == classCS && a == b && (a == classEN || a == classAN) {
			cls[i] = a
		}
	}
	for i := 0; i < len(cls); {
		if cls[i] != classET {
			i++
			continue
		}
		j := i
		for j < len(cls) && cls[j] == classET {
			j++
		}
		if (i > 0 && cls[i-1] == classEN) || (j < len(cls) && cls[j] == classEN) {
			for k := i; k < j; k++ {
				cls[k] = classEN
			}
		}
		i = j
	}
	for i, c := range cls {
		if c == classES || c == classCS || c == classET {
			cls[i] = classON
		}
	}
	last = classL
	for i, c := range cls {
		switch c {
		case classL, classR:
			last = c
		case classEN:
			if last == classL {
				cls[i] = classL
			}
		}
	}
	brackets(rs, cls)
	strong := func(c bidiClass) (bidiClass, bool) {
		switch c {
		case classL:
			return classL, true
		case classR, classEN, classAN:
			return classR, true
		}
		return 0, false
	}
	for i := 0; i < len(cls); {
		if _, ok := strong(cls[i]); ok {
			i++
			continue
		}
		j := i
		for j < len(cls) {
			if _, ok := strong(cls[j]); ok {
				break
			}
			j++
		}
		before, after := classL, classL
		if i > 0 {
			before, _ = strong(cls[i-1])
		}
		if j < len(cls) {
			after, _ = strong(cls[j])
		}
		fill := classL
		if before == classR && after == classR {
			fill = classR
		}
		for k := i; k < j; k++ {
			cls[k] = fill
		}
		i = j
	}
	level := make([]int, len(rs))
	top := 0
	for i, c := range cls {
		switch c {
		case classR:
			level[i] = 1
		case classEN, classAN:
			level[i] = 2
		}
		top = max(top, level[i])
	}
	type cluster struct {
		rs    []rune
		level int
	}
	var cl []cluster
	for i, r := range rs {
		if k := len(cl); k > 0 && transparent(r) {
			cl[k-1].rs = append(cl[k-1].rs, r)
			continue
		}
		if level[i]%2 == 1 {
			if m, ok := mirror[r]; ok {
				r = m
			}
		}
		cl = append(cl, cluster{[]rune{r}, level[i]})
	}
	for lv := top; lv >= 1; lv-- {
		for i := 0; i < len(cl); {
			if cl[i].level < lv {
				i++
				continue
			}
			j := i
			for j < len(cl) && cl[j].level >= lv {
				j++
			}
			for a, b := i, j-1; a < b; a, b = a+1, b-1 {
				cl[a], cl[b] = cl[b], cl[a]
			}
			i = j
		}
	}
	var b strings.Builder
	for _, c := range cl {
		b.WriteString(string(c.rs))
	}
	return b.String()
}
