package annotate

import (
	"bytes"
	"fmt"
	"image"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
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

var ansiRE = regexp.MustCompile("\x1b(\\[[0-?]*[ -/]*[@-~]|\\][^\x07\x1b]*(\x07|\x1b\\\\)?|[@-Z\\\\-_])")

func cleanText(s string) string {
	s = ansiRE.ReplaceAllString(s, "")
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\t' || r == '\n' || r == '\r' || r == '\v' || r == '\f':
			return ' '
		case unicode.Is(unicode.Cc, r), unicode.Is(unicode.Cf, r), unicode.Is(unicode.Variation_Selector, r):
			return -1
		}
		return r
	}, s)
}

var (
	monoFont, _   = opentype.Parse(gomono.TTF)
	mediumFont, _ = opentype.Parse(gomedium.TTF)
)

var fontDirs = func() []string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		return []string{"/System/Library/Fonts", "/Library/Fonts", filepath.Join(home, "Library", "Fonts")}
	case "windows":
		return []string{filepath.Join(os.Getenv("WINDIR"), "Fonts"), filepath.Join(os.Getenv("LOCALAPPDATA"), "Microsoft", "Windows", "Fonts")}
	}
	return []string{"/usr/share/fonts", "/usr/local/share/fonts", filepath.Join(home, ".local", "share", "fonts"), filepath.Join(home, ".fonts")}
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
		if dir == "" {
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

func findFallbacks(base *sfnt.Font, texts ...string) {
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
		for _, r := range t {
			if fallback.cover[r] == nil && !fallback.missing[r] && !outlined(base, &buf, r) {
				want[r] = true
			}
		}
	}
	if len(want) == 0 {
		return
	}
	for _, p := range fontFiles() {
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
	faces []font.Face
	pick  map[rune]int
}

func newTextFace(base *sfnt.Font, px int, text string) (font.Face, string) {
	findFallbacks(base, text)
	opts := &opentype.FaceOptions{Size: float64(px), DPI: 72, Hinting: font.HintingFull}
	bf, err := opentype.NewFace(base, opts)
	if err != nil {
		return nil, text
	}
	tf := &textFace{faces: []font.Face{bf}, pick: map[rune]int{}}
	index := map[*sfnt.Font]int{}
	var buf sfnt.Buffer
	var b strings.Builder
	fallback.Lock()
	defer fallback.Unlock()
	for _, r := range text {
		if _, ok := tf.pick[r]; ok || outlined(base, &buf, r) {
			b.WriteRune(r)
			continue
		}
		f := fallback.cover[r]
		if f == nil {
			fmt.Fprintf(&b, "[U+%04X]", r)
			continue
		}
		i, ok := index[f]
		if !ok {
			face, err := opentype.NewFace(f, opts)
			if err != nil {
				fmt.Fprintf(&b, "[U+%04X]", r)
				continue
			}
			i = len(tf.faces)
			tf.faces = append(tf.faces, face)
			index[f] = i
		}
		tf.pick[r] = i
		b.WriteRune(r)
	}
	return tf, b.String()
}

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
