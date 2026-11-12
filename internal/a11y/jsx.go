package a11y

import (
	"regexp"
	"strings"
)

var reactNativeImport = regexp.MustCompile(`(from\s+|require\(\s*)['"]react-native['"]`)

func isReactNative(src string) bool { return reactNativeImport.MatchString(src) }

// rnTouchables are React Native components that make one tappable element.
var rnTouchables = map[string]bool{
	"TouchableOpacity": true, "TouchableHighlight": true, "TouchableWithoutFeedback": true,
	"TouchableNativeFeedback": true, "Pressable": true, "RectButton": true, "BorderlessButton": true,
}

func tagString(t *tag, key string) string {
	v := strings.TrimSpace(t.attrs[key])
	if strings.HasPrefix(v, "{") && strings.HasSuffix(v, "}") {
		v = strings.TrimSpace(v[1 : len(v)-1])
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'' || v[0] == '`') && v[len(v)-1] == v[0] && !strings.Contains(v, "${") {
			return v[1 : len(v)-1]
		}
		return ""
	}
	return v
}

func checkReactNative(f *file, p *project) {
	root, inLit := parseSource(f.src, false)
	loops := jsLoops(f.src, inLit)
	defs := jsDefs(f.src, root, inLit)
	root.walkAll(func(t *tag) bool {
		if t == root {
			return true
		}
		if strings.Contains(t.attrs["importantForAccessibility"], "no-hide-descendants") ||
			t.attrTrue("accessibilityElementsHidden") || t.attrTrue("aria-hidden") {
			f.add(t.off, "rn-hidden", Element{Kind: t.name, ID: tagString(t, "testID")})
		}
		if !rnTouchables[t.name] {
			return true
		}
		label := tagString(t, "accessibilityLabel")
		if label == "" {
			label = tagString(t, "aria-label")
		}
		el := Element{Kind: t.name, Label: label, ID: tagString(t, "testID")}
		if !t.spread && !t.has("testID") && !t.has("accessibilityLabel") && !t.has("aria-label") && !subtreeHasText(t) {
			if why := t.unsure(); why != "" {
				f.addManual(t.off, "rn-no-selector", el, why)
			} else if by := loopAround(t, loops); by != "" {
				f.addManual(t.off, "rn-no-selector", el, perItem(by, "testID"))
			} else if cb := callbackAround(f.src, t); cb != "" {
				f.addManual(t.off, "rn-no-selector", el, perCall(cb, "testID"))
			} else if row, why := p.jsRows.in(defs, t.off); row != "" {
				f.addManual(t.off, "rn-no-selector", el, perItemIn(row, why, "testID"))
			} else {
				base := firstName(handlerName(t.attrs["onPress"]), handlerName(t.attrs["onLongPress"]), iconName(t), t.name)
				f.addFix(t.off, "rn-no-selector", el, attrFix(f.src, t.off, "testID", "%s", base, "button", "-"))
			}
		}
		if t.attrs["accessible"] == "{false}" {
			return true
		}
		for _, c := range t.children {
			c.walk(func(d *tag) bool {
				if d.has("testID") {
					f.add(d.off, "rn-nested-testid", Element{Kind: d.name, ID: tagString(d, "testID")})
				}
				return true
			})
		}
		return true
	})
}

// subtreeHasText: t or something inside it shows text, an {expression} (which may be
// text), or a label.
func subtreeHasText(t *tag) bool {
	found := false
	t.walk(func(d *tag) bool {
		if found {
			return false
		}
		if d.text || d.has("accessibilityLabel") || d.has("aria-label") || d.has("title") || d.spread {
			found = true
		}
		return true
	})
	return found
}

// webClickRoles are the roles tb's page script treats as clickable.
var webClickRoles = map[string]bool{"button": true, "link": true, "checkbox": true, "menuitem": true,
	"menuitemcheckbox": true, "menuitemradio": true, "tab": true, "switch": true, "option": true, "radio": true, "treeitem": true}

// webSelectorAttrs are what tb's page script takes an element's id from (see idOf in
// internal/driver/web/page.js), plus React's htmlFor.
var webSelectorAttrs = []string{"id", "aria-label", "name", "title", "for", "htmlFor", "data-testid", "flt-semantics-identifier"}

// webIconTag: components that render an icon or image, not text.
var webIconTag = regexp.MustCompile(`(?i)(icon|svg|image|img|logo|avatar)$|^(svg|img|i|picture|canvas)$`)

func checkWeb(f *file, p *project) {
	// An HTML page and a Vue or Svelte template have their .map( calls found by pageLoops,
	// which does not read their scripts' strings and comments: every .map( in them counts as
	// a loop but one in an HTML comment. That is the safe direction: it can only leave an
	// element for a person, never give the items of a loop one fixed id.
	pg := readJSPage(f.path, f.src)
	root, loops := pg.root, pg.loops
	root.walkAll(func(t *tag) bool {
		if t == root || t.name == "" || !isLower(t.name[0]) {
			return true
		}
		name := strings.ToLower(t.name)
		if name == "svg" {
			return false
		}
		role := strings.ToLower(tagString(t, "role"))
		typ := strings.ToLower(tagString(t, "type"))
		kind := name
		switch {
		case name == "input" && typ == "hidden":
			return true
		case name == "input":
			if typ == "" {
				typ = "text"
			}
			kind = "input:" + typ
		case name == "button", name == "a", name == "select", name == "textarea", name == "summary":
		case webClickRoles[role]:
		default:
			return true
		}
		if t.spread {
			return true
		}
		for _, a := range webSelectorAttrs {
			if t.has(a) {
				return true
			}
		}
		if t.has("placeholder") || (name == "input" && t.has("value")) || webHasText(t) {
			return true
		}
		if why := t.unsure(); why != "" {
			f.addManual(t.off, "web-no-selector", Element{Kind: kind}, why)
			return true
		}
		if by := loopAround(t, loops); by != "" {
			f.addManual(t.off, "web-no-selector", Element{Kind: kind}, perItem(by, "data-testid"))
			return true
		}
		if cb := callbackAround(f.src, t); cb != "" {
			f.addManual(t.off, "web-no-selector", Element{Kind: kind}, perCall(cb, "data-testid"))
			return true
		}
		if row, why := p.jsRows.in(pg.defs, t.off); row != "" {
			f.addManual(t.off, "web-no-selector", Element{Kind: kind}, perItemIn(row, why, "data-testid"))
			return true
		}
		base, role := webName(t, name, role, typ)
		f.addFix(t.off, "web-no-selector", Element{Kind: kind}, attrFix(f.src, t.off, "data-testid", "%s", base, role, "-"))
		return true
	})
}

// webHandlers are the click handler attributes of JSX, HTML, Vue and Svelte.
var webHandlers = []string{"onClick", "onclick", "@click", "v-on:click", "on:click"}

// webName is what a data-testid is made of: what the element does (its handler, its icon,
// its link), and what it is (button, link, field, ...).
func webName(t *tag, name, role, typ string) (base, what string) {
	switch {
	case name == "a":
		what = "link"
	case name == "select":
		what = "select"
	case name == "textarea":
		what, base = "field", "text"
	case name == "input":
		switch typ {
		case "checkbox", "radio":
			what = typ
		case "submit", "button", "reset", "image":
			what, base = "button", typ
		case "range":
			what = "slider"
		default:
			what, base = "field", typ
		}
	case role != "" && webClickRoles[role]:
		what = role
	default:
		what = "button"
	}
	for _, h := range webHandlers {
		if n := handlerName(t.attrs[h]); n != "" {
			return n, what
		}
	}
	if n := iconName(t); n != "" {
		return n, what
	}
	if name == "a" {
		if n := hrefName(tagString(t, "href")); n != "" {
			return n, what
		}
	}
	return base, what
}

// firstName is the first name that is not empty.
func firstName(names ...string) string {
	for _, n := range names {
		if n != "" {
			return n
		}
	}
	return ""
}

func isLower(c byte) bool { return c >= 'a' && c <= 'z' }

// webHasText: text tb reads inside t. Images and SVGs are skipped by tb's page script;
// components (capitalised) count as text unless their name says icon or image.
func webHasText(t *tag) bool {
	found := false
	t.walk(func(d *tag) bool {
		if found {
			return false
		}
		if d != t && webIconTag.MatchString(d.name) {
			return false
		}
		if d.text || d.spread || (d != t && d.name != "" && !isLower(d.name[0])) {
			found = true
			return false
		}
		if d != t {
			for _, a := range []string{"aria-label", "title"} {
				if d.has(a) {
					found = true
				}
			}
		}
		return true
	})
	return found
}
