package look

import (
	"strings"
	"testing"
)

func TestSimilarSurvivesTypos(t *testing.T) {
	s := android(
		nd(1, "android.widget.Button", slot(0), text("Log in"), click),
		nd(1, "android.widget.ImageButton", slot(1), id("login"), click),
		nd(1, "android.widget.TextView", slot(2), text("Settings")),
		nd(1, "android.widget.TextView", slot(3), text("Checkout total")),
		nd(1, "android.widget.TextView", slot(4), text("Help")),
	)
	l := Build(s, NewRefs())
	cases := []struct {
		query string
		want  []string
	}{
		{"Logn in", []string{"Log in", "#login"}},
		{"logn", []string{"Log in", "#login"}},
		{"lgoin", []string{"#login"}},
		{"Setings", []string{"Settings"}},
		{"Chekcout totl", []string{"Checkout total"}},
		{"Settings", []string{"Settings"}},
		{"Hello", nil},
		{"Logistics", nil},
		{"abc", nil},
	}
	for _, c := range cases {
		t.Run(c.query, func(t *testing.T) {
			var got []string
			for _, e := range l.Similar(c.query) {
				if e.Label != "" {
					got = append(got, e.Label)
				} else {
					got = append(got, "#"+e.ID)
				}
			}
			if strings.Join(got, "|") != strings.Join(c.want, "|") {
				t.Errorf("Similar(%q) = %v, want %v", c.query, got, c.want)
			}
		})
	}
}

func TestSimilarListsExactMatchesBeforeTypos(t *testing.T) {
	l := Build(textScreen("Card", "Carts", "Cart 1", "Cart 2", "Cart 3", "Cart 4"), NewRefs())
	got := l.Similar("Cart")
	var labels []string
	for _, e := range got {
		labels = append(labels, e.Label)
	}
	if strings.Join(labels, "|") != "Carts|Cart 1|Cart 2|Cart 3|Cart 4" {
		t.Errorf("a typo match took the place of an exact one, or the cap of 5 is gone: %v", labels)
	}
	l = Build(textScreen("Card", "Carts"), NewRefs())
	got = l.Similar("Cart")
	if len(got) != 2 || got[0].Label != "Carts" || got[1].Label != "Card" {
		t.Errorf("exact matches should come first, typo matches after: %d", len(got))
	}
}
