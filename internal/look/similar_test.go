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
		nd(1, "android.widget.Button", slot(5), text("Exit"), click),
		nd(1, "android.widget.Button", slot(6), text("Text"), click),
		nd(1, "android.widget.Button", slot(7), text("None"), click),
		nd(1, "android.widget.TextView", slot(8), text("Orders 2024")),
		nd(1, "android.widget.TextView", slot(9), text("Build 20245")),
	)
	l := Build(s, NewRefs())
	cases := []struct {
		query string
		want  []string
	}{
		{"Loginn", []string{"#login"}},
		{"lgoin", []string{"#login"}},
		{"Logim now", []string{"#login"}},
		{"Setings", []string{"Settings"}},
		{"Chekcout totl", []string{"Checkout total"}},
		{"Settings", []string{"Settings"}},
		{"Order", []string{"Orders 2024"}},
		{"Logn in", nil},
		{"Edit", nil},
		{"Next", nil},
		{"Done", nil},
		{"2025", nil},
		{"20246", nil},
		{"Settimgz", nil},
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
	l := Build(textScreen("Older", "Orders", "Order 1", "Order 2", "Order 3", "Order 4"), NewRefs())
	got := l.Similar("Order")
	var labels []string
	for _, e := range got {
		labels = append(labels, e.Label)
	}
	if strings.Join(labels, "|") != "Orders|Order 1|Order 2|Order 3|Order 4" {
		t.Errorf("a typo match took the place of an exact one, or the cap of 5 is gone: %v", labels)
	}
	l = Build(textScreen("Older", "Orders"), NewRefs())
	got = l.Similar("Order")
	if len(got) != 2 || got[0].Label != "Orders" || got[1].Label != "Older" {
		t.Errorf("exact matches should come first, typo matches after: %d", len(got))
	}
}
