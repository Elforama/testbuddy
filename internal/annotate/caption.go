package annotate

// The caption band: the step that is running, as text in a band below the recording.

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

// bandColor is the band's background (ffmpeg's 0xRRGGBB); captionImage paints the same.
const bandColor = "1c1c1e"

var bandRGB = color.RGBA{0x1c, 0x1c, 0x1e, 0xff}

// captionLines is the most lines a caption takes; a longer one ends in "…".
const captionLines = 2

// captionSize is the text's size in pixels for a w×h video: readable on a phone held upright
// (1080 wide: 49 px) and in a landscape browser window (656 high: 29 px).
func captionSize(w, h int) int {
	return min(max(min(w, h)/22, 14), 64)
}

// bandHeight is the band's height for a w×h video: two lines of text and a margin, even (the
// encoder wants even sizes).
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

// captionImage is a w×band image of text, white on the band's color, in at most captionLines
// centered lines of px pixels.
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
	top := (band-len(lines)*lineH)/2 + px // the first baseline
	d := &font.Drawer{Dst: img, Src: image.White, Face: face}
	for i, l := range lines {
		width := d.MeasureString(l)
		d.Dot = fixed.Point26_6{X: (fixed.I(w) - width) / 2, Y: fixed.I(top + i*lineH)}
		d.DrawString(l)
	}
	return img
}

// wrap breaks text into lines no wider than max, at most captionLines; the last line of a
// text that does not fit ends in "…". A word wider than a line is cut.
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
		if cur == "" { // one word wider than the line
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

// fitText cuts s to the most that, with "…" after it, is no wider than max (s itself when it fits).
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
