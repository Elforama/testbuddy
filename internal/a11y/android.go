package a11y

import (
	"encoding/xml"
	"path/filepath"
	"strings"
)

// isLayoutXML: an Android layout (in a layout*/ folder, using the android namespace).
func isLayoutXML(path, src string) bool {
	dir := filepath.Base(filepath.Dir(path))
	return strings.HasPrefix(dir, "layout") && strings.Contains(src, "schemas.android.com/apk/res/android")
}

// interactiveViews are views people tap or type in. Image-like ones have no text of their own.
var interactiveViews = map[string]bool{
	"Button": true, "ImageButton": true, "EditText": true, "CheckBox": true, "RadioButton": true,
	"Switch": true, "SwitchCompat": true, "SwitchMaterial": true, "MaterialSwitch": true, "ToggleButton": true,
	"Spinner": true, "SeekBar": true, "FloatingActionButton": true, "ExtendedFloatingActionButton": true,
	"Chip": true, "MaterialButton": true, "TextInputEditText": true, "AutoCompleteTextView": true,
	"MaterialAutoCompleteTextView": true, "CheckedTextView": true, "AppCompatButton": true,
	"AppCompatImageButton": true, "AppCompatEditText": true, "AppCompatCheckBox": true,
	"MaterialCheckBox": true, "MaterialRadioButton": true, "Slider": true, "RatingBar": true,
}

var imageViews = map[string]bool{
	"ImageButton": true, "AppCompatImageButton": true, "ImageView": true, "AppCompatImageView": true,
	"ShapeableImageView": true, "FloatingActionButton": true, "SeekBar": true, "Slider": true, "RatingBar": true,
}

const androidNS = "http://schemas.android.com/apk/res/android"

func checkAndroidXML(f *file, p *project) {
	row, isRow := p.layoutRows[layoutName(f.path)]
	d := xml.NewDecoder(strings.NewReader(f.src))
	d.Strict = false
	for {
		off := int(d.InputOffset())
		tok, err := d.Token()
		if err != nil {
			return
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		kind := se.Name.Local
		if i := strings.LastIndexByte(kind, '.'); i >= 0 {
			kind = kind[i+1:]
		}
		attr := map[string]string{}
		srcCompat := ""
		for _, a := range se.Attr {
			if a.Name.Space == androidNS || a.Name.Space == "android" {
				attr[a.Name.Local] = a.Value
			}
			if a.Name.Local == "srcCompat" {
				srcCompat = a.Value
			}
		}
		clickable := attr["clickable"] == "true" || attr["onClick"] != ""
		if !interactiveViews[kind] && !clickable {
			continue
		}
		if attr["id"] != "" || attr["text"] != "" || attr["contentDescription"] != "" || attr["hint"] != "" {
			continue
		}
		el := Element{Kind: kind}
		// The offset is where the previous token ended; the tag starts at the next '<'.
		if i := strings.IndexByte(f.src[off:], '<'); i >= 0 {
			off += i
		}
		// The fix: an id named after the click handler, else the image, else the view.
		base := attr["onClick"]
		if base == "" {
			if img := attr["src"]; img != "" {
				base = resourceName(img)
			} else if srcCompat != "" {
				base = resourceName(srcCompat)
			}
		}
		if base == "" {
			base = kind
		}
		rule := "android-no-id"
		if imageViews[kind] {
			rule = "android-no-selector"
		}
		if isRow {
			f.addManual(off, rule, el, perRow(row))
			continue
		}
		fx := attrFix(f.src, off, "android:id", "@+id/%s", base, androidRole(kind), "_")
		if imageViews[kind] {
			f.addFix(off, "android-no-selector", el, fx)
		} else {
			f.addFix(off, "android-no-id", el, fx)
		}
	}
}

// androidRole is what a view is, to end its id with: close_button, email_field.
func androidRole(kind string) string {
	switch {
	case strings.Contains(kind, "Radio"):
		return "radio"
	case strings.Contains(kind, "CheckBox"), kind == "CheckedTextView":
		return "checkbox"
	case strings.Contains(kind, "Switch"):
		return "switch"
	case strings.Contains(kind, "EditText"), strings.Contains(kind, "AutoComplete"):
		return "field"
	case kind == "Spinner":
		return "spinner"
	case kind == "SeekBar", kind == "Slider", kind == "RatingBar":
		return "slider"
	}
	return "button"
}
