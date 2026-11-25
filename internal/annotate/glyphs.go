package annotate

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"unicode"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gomedium"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

func stripEscapes(s string) string {
	rs := []rune(s)
	var b strings.Builder
	stringEnd := func(i int) int {
		for j := i; j < len(rs); j++ {
			if rs[j] == 0x07 || rs[j] == 0x9C {
				return j + 1
			}
			if rs[j] == 0x1b && j+1 < len(rs) && rs[j+1] == '\\' {
				return j + 2
			}
		}
		return -1
	}
	csiEnd := func(i int) int {
		j := i
		for j < len(rs) && rs[j] >= 0x30 && rs[j] <= 0x3F {
			j++
		}
		for j < len(rs) && rs[j] >= 0x20 && rs[j] <= 0x2F {
			j++
		}
		if j < len(rs) && rs[j] >= 0x40 && rs[j] <= 0x7E {
			return j + 1
		}
		return j
	}
	for i := 0; i < len(rs); {
		r := rs[i]
		switch {
		case r == 0x9B:
			i = csiEnd(i + 1)
			continue
		case r == 0x9D || r == 0x90 || r == 0x98 || r == 0x9E || r == 0x9F:
			if e := stringEnd(i + 1); e > 0 {
				i = e
			} else {
				i++
			}
			continue
		case r != 0x1b:
			b.WriteRune(r)
			i++
			continue
		}
		if i+1 >= len(rs) {
			break
		}
		switch c := rs[i+1]; {
		case c == '[':
			i = csiEnd(i + 2)
		case c == ']' || c == 'P' || c == 'X' || c == '^' || c == '_':
			if e := stringEnd(i + 2); e > 0 {
				i = e
			} else {
				i += 2
			}
		case c >= 0x20 && c <= 0x2F:
			j := i + 1
			for j < len(rs) && rs[j] >= 0x20 && rs[j] <= 0x2F {
				j++
			}
			i = min(j+1, len(rs))
		case c >= 0x30 && c <= 0x7E:
			i += 2
		default:
			i++
		}
	}
	return b.String()
}

func cleanText(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\t' || r == '\n' || r == '\r' || r == '\v' || r == '\f':
			return ' '
		case r == zwnj:
			return r
		case unicode.Is(unicode.Cc, r), unicode.Is(unicode.Cf, r), unicode.Is(unicode.Variation_Selector, r):
			return -1
		}
		return r
	}, stripEscapes(s))
}

var (
	monoFont, _   = opentype.Parse(gomono.TTF)
	mediumFont, _ = opentype.Parse(gomedium.TTF)
)

var fontDirs = func() []string {
	under := func(root string, parts ...string) string {
		if root == "" {
			return ""
		}
		return filepath.Join(append([]string{root}, parts...)...)
	}
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		return []string{"/System/Library/Fonts", "/Library/Fonts", under(home, "Library", "Fonts")}
	case "windows":
		return []string{under(os.Getenv("WINDIR"), "Fonts"), under(os.Getenv("LOCALAPPDATA"), "Microsoft", "Windows", "Fonts")}
	}
	return []string{"/usr/share/fonts", "/usr/local/share/fonts", under(home, ".local", "share", "fonts"), under(home, ".fonts")}
}

const maxFontFile = 100 << 20

var fallback struct {
	sync.Mutex
	cover   map[rune]*sfnt.Font
	missing map[rune]bool
}

func outlined(f *sfnt.Font, buf *sfnt.Buffer, r rune) bool {
	x, err := f.GlyphIndex(buf, r)
	if err != nil || x == 0 {
		return false
	}
	if unicode.IsSpace(r) {
		return true
	}
	segs, err := f.LoadGlyph(buf, x, fixed.I(64), nil)
	return err == nil && len(segs) > 0
}

func fontFiles() []string {
	var files []string
	for _, dir := range fontDirs() {
		if dir == "" || !filepath.IsAbs(dir) {
			continue
		}
		filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			switch strings.ToLower(filepath.Ext(p)) {
			case ".ttf", ".otf", ".ttc", ".otc":
				if !strings.Contains(strings.ToLower(filepath.Base(p)), "lastresort") {
					files = append(files, p)
				}
			}
			return nil
		})
	}
	late := func(p string) bool {
		b := strings.ToLower(filepath.Base(p))
		return strings.Contains(b, "unifont") || strings.Contains(b, "emoji")
	}
	sort.SliceStable(files, func(i, j int) bool { return !late(files[i]) && late(files[j]) })
	return files
}

func parseFonts(p string) []*sfnt.Font {
	if st, err := os.Stat(p); err != nil || st.Size() > maxFontFile {
		return nil
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	if bytes.HasPrefix(data, []byte("ttcf")) {
		c, err := sfnt.ParseCollection(data)
		if err != nil {
			return nil
		}
		var out []*sfnt.Font
		for i := 0; i < c.NumFonts(); i++ {
			if f, err := c.Font(i); err == nil {
				out = append(out, f)
			}
		}
		return out
	}
	f, err := sfnt.Parse(data)
	if err != nil {
		return nil
	}
	return []*sfnt.Font{f}
}

func drawnRunes(text string) []rune {
	var out []rune
	for _, r := range text {
		out = append(out, r)
		if f, ok := arabicForms[r]; ok {
			for _, x := range f {
				if x != 0 {
					out = append(out, x)
				}
			}
		}
		if l, ok := lamAlef[r]; ok {
			out = append(out, l[0], l[1])
		}
	}
	return out
}

func findFallbacks(ctx context.Context, base *sfnt.Font, texts ...string) {
	if base == nil {
		return
	}
	var buf sfnt.Buffer
	fallback.Lock()
	defer fallback.Unlock()
	if fallback.cover == nil {
		fallback.cover, fallback.missing = map[rune]*sfnt.Font{}, map[rune]bool{}
	}
	want := map[rune]bool{}
	for _, t := range texts {
		for _, r := range drawnRunes(t) {
			if r != zwnj && fallback.cover[r] == nil && !fallback.missing[r] && !outlined(base, &buf, r) {
				want[r] = true
			}
		}
	}
	if len(want) == 0 {
		return
	}
	for _, p := range fontFiles() {
		if ctx.Err() != nil {
			return
		}
		for _, f := range parseFonts(p) {
			for r := range want {
				if outlined(f, &buf, r) {
					fallback.cover[r] = f
					delete(want, r)
				}
			}
		}
		if len(want) == 0 {
			return
		}
	}
	for r := range want {
		fallback.missing[r] = true
	}
}

type textFace struct {
	base  *sfnt.Font
	opts  *opentype.FaceOptions
	faces []font.Face
	index map[*sfnt.Font]int
	pick  map[rune]int
	buf   sfnt.Buffer
}

func newTextFace(base *sfnt.Font, px int) *textFace {
	if base == nil {
		return nil
	}
	opts := &opentype.FaceOptions{Size: float64(px), DPI: 72, Hinting: font.HintingFull}
	bf, err := opentype.NewFace(base, opts)
	if err != nil {
		return nil
	}
	return &textFace{base: base, opts: opts, faces: []font.Face{bf}, index: map[*sfnt.Font]int{}, pick: map[rune]int{}}
}

func (f *textFace) has(r rune) bool {
	if _, ok := f.pick[r]; ok {
		return true
	}
	if outlined(f.base, &f.buf, r) {
		f.pick[r] = 0
		return true
	}
	fallback.Lock()
	fb := fallback.cover[r]
	fallback.Unlock()
	if fb == nil {
		return false
	}
	i, ok := f.index[fb]
	if !ok {
		face, err := opentype.NewFace(fb, f.opts)
		if err != nil {
			return false
		}
		i = len(f.faces)
		f.faces = append(f.faces, face)
		f.index[fb] = i
	}
	f.pick[r] = i
	return true
}

func (f *textFace) show(s string) string {
	var b strings.Builder
	for _, r := range visual(s) {
		if f.has(r) {
			b.WriteRune(r)
		} else {
			fmt.Fprintf(&b, "[U+%04X]", r)
		}
	}
	return b.String()
}

func (f *textFace) measure(s string) fixed.Int26_6 { return font.MeasureString(f, f.show(s)) }

func (f *textFace) face(r rune) font.Face { return f.faces[f.pick[r]] }

func (f *textFace) Close() error {
	for _, x := range f.faces {
		x.Close()
	}
	return nil
}

func (f *textFace) Glyph(dot fixed.Point26_6, r rune) (dr image.Rectangle, mask image.Image, maskp image.Point, advance fixed.Int26_6, ok bool) {
	return f.face(r).Glyph(dot, r)
}

func (f *textFace) GlyphBounds(r rune) (fixed.Rectangle26_6, fixed.Int26_6, bool) {
	return f.face(r).GlyphBounds(r)
}

func (f *textFace) GlyphAdvance(r rune) (fixed.Int26_6, bool) { return f.face(r).GlyphAdvance(r) }

func (f *textFace) Kern(r0, r1 rune) fixed.Int26_6 {
	if f.pick[r0] != f.pick[r1] {
		return 0
	}
	return f.face(r0).Kern(r0, r1)
}

func (f *textFace) Metrics() font.Metrics { return f.faces[0].Metrics() }
