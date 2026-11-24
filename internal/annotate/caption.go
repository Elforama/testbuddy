package annotate

import (
	"image"
	"image/color"
	"image/draw"
	"strings"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gomedium"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

const bandColor = "1c1c1e"

var bandRGB = color.RGBA{0x1c, 0x1c, 0x1e, 0xff}

const captionLines = 2

func captionSize(w, h int) int {
	return min(max(min(w, h)/22, 14), 64)
}

func bandHeight(w, h int) int {
	px := captionSize(w, h)
	b := px*captionLines*13/10 + px
	return b + b%2
}

var fontFace = func() func(px int) font.Face {
	f, err := opentype.Parse(gomedium.TTF)
	return func(px int) font.Face {
		if err != nil {
			return nil
		}
		face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: float64(px), DPI: 72, Hinting: font.HintingFull})
		if err != nil {
			return nil
		}
		return face
	}
}()

func captionImage(text string, w, band, px int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, band))
	draw.Draw(img, img.Bounds(), &image.Uniform{bandRGB}, image.Point{}, draw.Src)
	face := fontFace(px)
	if face == nil {
		return img
	}
	defer face.Close()
	lines := wrap(face, text, fixed.I(w-2*px))
	lineH := px * 13 / 10
	top := (band-len(lines)*lineH)/2 + px
	d := &font.Drawer{Dst: img, Src: image.White, Face: face}
	for i, l := range lines {
		width := d.MeasureString(l)
		d.Dot = fixed.Point26_6{X: (fixed.I(w) - width) / 2, Y: fixed.I(top + i*lineH)}
		d.DrawString(l)
	}
	return img
}

func wrap(face font.Face, text string, max fixed.Int26_6) []string {
	words := strings.Fields(text)
	var lines []string
	cur := ""
	for len(words) > 0 {
		try := words[0]
		if cur != "" {
			try = cur + " " + words[0]
		}
		if font.MeasureString(face, try) <= max {
			cur, words = try, words[1:]
			continue
		}
		if cur == "" {
			cur, words = fitText(face, words[0], max), words[1:]
		}
		lines = append(lines, cur)
		cur = ""
		if len(lines) == captionLines {
			break
		}
	}
	if cur != "" && len(lines) < captionLines {
		lines = append(lines, cur)
	}
	if len(words) > 0 && len(lines) > 0 {
		last := lines[len(lines)-1]
		lines[len(lines)-1] = fitText(face, last+" "+strings.Join(words, " "), max)
	}
	return lines
}

func fitText(face font.Face, s string, max fixed.Int26_6) string {
	if font.MeasureString(face, s) <= max {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && font.MeasureString(face, string(r)+"…") > max {
		r = r[:len(r)-1]
	}
	return strings.TrimRight(string(r), " ") + "…"
}
