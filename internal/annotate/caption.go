package annotate

import (
	"context"
	"image"
	"image/color"
	"image/draw"
	"strings"

	"golang.org/x/image/font"
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

func captionImage(text string, w, band, px int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, band))
	draw.Draw(img, img.Bounds(), &image.Uniform{bandRGB}, image.Point{}, draw.Src)
	if mediumFont == nil {
		return img
	}
	text = strings.Join(strings.Fields(cleanText(text)), " ")
	findFallbacks(context.Background(), mediumFont, text)
	face := newTextFace(mediumFont, px)
	if face == nil {
		return img
	}
	defer face.Close()
	lines := wrap(face.measure, text, fixed.I(w-2*px))
	lineH := px * 13 / 10
	top := (band-len(lines)*lineH)/2 + px
	d := &font.Drawer{Dst: img, Src: image.White, Face: face}
	for i, l := range lines {
		width := face.measure(l)
		d.Dot = fixed.Point26_6{X: (fixed.I(w) - width) / 2, Y: fixed.I(top + i*lineH)}
		d.DrawString(face.show(l))
	}
	return img
}

func wrap(measure func(string) fixed.Int26_6, text string, max fixed.Int26_6) []string {
	words := strings.Fields(text)
	var lines []string
	cur := ""
	for len(words) > 0 {
		try := words[0]
		if cur != "" {
			try = cur + " " + words[0]
		}
		if measure(try) <= max {
			cur, words = try, words[1:]
			continue
		}
		if cur == "" {
			cur, words = fitText(measure, words[0], max), words[1:]
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
		lines[len(lines)-1] = fitText(measure, last+" "+strings.Join(words, " "), max)
	}
	return lines
}

func fitText(measure func(string) fixed.Int26_6, s string, max fixed.Int26_6) string {
	if measure(s) <= max {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && measure(string(r)+"…") > max {
		r = r[:len(r)-1]
	}
	return strings.TrimRight(string(r), " ") + "…"
}
