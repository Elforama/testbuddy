package look

import (
	"fmt"
	"strings"
	"testing"
)

func rowTexts(rows []Row) string {
	var out []string
	for _, r := range rows {
		out = append(out, r.Text)
	}
	return strings.Join(out, "\n")
}

func whole(texts ...string) []Row {
	var out []Row
	for _, t := range texts {
		out = append(out, Row{Text: t})
	}
	return out
}

func cut(text string) Row { return Row{Text: text, Cut: true} }

func expectJoin(t *testing.T, before, after []Row, overlap bool, want ...string) {
	t.Helper()
	got, ok := JoinRows(before, after)
	if rowTexts(got) != strings.Join(want, "\n") || ok != overlap {
		t.Errorf("JoinRows = (overlap %v)\n%s\nwant (overlap %v)\n%s", ok, rowTexts(got), overlap, strings.Join(want, "\n"))
	}
}

func TestRowsOfTheMainScrollArea(t *testing.T) {
	s := android(
		nd(1, "android.widget.TextView", rect(0, 0, 1080, 100), text("Orders")),
		nd(1, "android.widget.HorizontalScrollView", rect(0, 100, 1080, 200), scrollable),
		nd(2, "android.widget.Button", rect(0, 100, 300, 200), text("All"), click),
		nd(2, "android.widget.Button", rect(300, 100, 600, 200), text("Open"), click),
		nd(1, "androidx.recyclerview.widget.RecyclerView", rect(0, 200, 1080, 2200), scrollable),
		nd(2, "android.view.ViewGroup", rect(0, 210, 1080, 400)),
		nd(3, "android.widget.TextView", rect(40, 220, 700, 280), text("Order #1")),
		nd(3, "android.widget.TextView", rect(800, 220, 1040, 280), text("$12")),
		nd(2, "android.view.ViewGroup", rect(0, 420, 1080, 620)),
		nd(3, "android.widget.TextView", rect(40, 440, 700, 500), text("Order #2")),
		nd(3, "android.widget.TextView", rect(800, 440, 1040, 500), text("$30")),
		nd(2, "android.view.ViewGroup", rect(0, 2100, 1080, 2300)),
		nd(3, "android.widget.TextView", rect(40, 2120, 700, 2180), text("Order #9")),
		nd(3, "android.widget.TextView", rect(40, 2190, 700, 2250), text("3 items")),
	)
	rows, view := Build(s, NewRefs()).Rows()
	if view != rect(0, 200, 1080, 2200) {
		t.Errorf("view = %v, want the list's bounds", view)
	}
	want := "Text \"Order #1\"  Text \"$12\"\nText \"Order #2\"  Text \"$30\"\nText \"Order #9\"\nText \"3 items\""
	if rowTexts(rows) != want {
		t.Fatalf("rows =\n%s\nwant\n%s", rowTexts(rows), want)
	}
	if rows[0].Cut || rows[1].Cut || rows[2].Cut || !rows[3].Cut {
		t.Errorf("cut = %v %v %v %v, want only the row that crosses the list's bottom edge", rows[0].Cut, rows[1].Cut, rows[2].Cut, rows[3].Cut)
	}
}

func TestRowsWithoutAScrollAreaAreTheScreens(t *testing.T) {
	s := android(
		nd(1, "android.widget.TextView", row(0), text("One")),
		nd(1, "android.widget.TextView", row(1), text("Two")),
	)
	rows, view := Build(s, NewRefs()).Rows()
	if rowTexts(rows) != "Text \"One\"\nText \"Two\"" || view != rect(0, 0, phoneW, phoneH) {
		t.Errorf("rows = %q in %v", rowTexts(rows), view)
	}
}

func TestRowsLeaveOutMarksThatChangeWithTheScroll(t *testing.T) {
	s := android(
		nd(1, "androidx.recyclerview.widget.RecyclerView", rect(0, 200, 1080, 2200), scrollable),
		nd(2, "android.widget.TextView", rect(0, 150, 1080, 350), text("Half above")),
		nd(2, "android.widget.TextView", rect(0, 350, 1080, 550), text("Whole")),
	)
	rows, _ := Build(s, NewRefs()).Rows()
	if rowTexts(rows) != "Text \"Half above\"\nText \"Whole\"" || !rows[0].Cut || rows[1].Cut {
		t.Errorf("rows = %+v", rows)
	}
}

func TestJoinRowsThreePagesWithOverlap(t *testing.T) {
	all := whole("1", "2", "3", "4")
	all, _ = JoinRows(all, whole("3", "4", "5", "6"))
	expectJoin(t, all, whole("5", "6", "7"), true, "1", "2", "3", "4", "5", "6", "7")
}

func TestJoinRowsTakesACutRowOnceAndWhole(t *testing.T) {
	before := append(whole("A", "B", "C"), cut("D title"))
	after := append([]Row{cut("B subtitle")}, whole("C", "D title  D subtitle", "E")...)
	expectJoin(t, before, after, true, "A", "B", "C", "D title  D subtitle", "E")
}

func TestJoinRowsWholeRowsFlushWithTheEdge(t *testing.T) {
	before := append(whole("A", "B"), cut("C"))
	after := append([]Row{cut("C")}, whole("D", "E")...)
	expectJoin(t, before, after, true, "A", "B", "C", "D", "E")
}

func TestJoinRowsKeepsGenuineDuplicates(t *testing.T) {
	expectJoin(t, whole("A", "B", "B"), whole("B", "B", "C"), true, "A", "B", "B", "C")
	expectJoin(t, whole("A", "B", "B", "B"), whole("B", "B", "C"), true, "A", "B", "B", "B", "C")
}

func TestJoinRowsWithoutOverlapKeepsEverything(t *testing.T) {
	before := append(whole("A", "B"), cut("C top"))
	after := append([]Row{cut("F bottom")}, whole("G", "H")...)
	expectJoin(t, before, after, false, "A", "B", "C top", "F bottom", "G", "H")
}

func TestJoinRowsKeepsTheLastPagesEdgeRow(t *testing.T) {
	all := whole("1", "2")
	all, _ = JoinRows(all, append(whole("2", "3"), cut("4")))
	if rowTexts(all) != "1\n2\n3\n4" {
		t.Errorf("rows = %q: the row at the bottom edge of the last page is part of the list", rowTexts(all))
	}
}

func TestJoinRowsLongList(t *testing.T) {
	var all []Row
	for page := 0; page < 20; page++ {
		var p []Row
		for i := page * 3; i < page*3+5; i++ {
			p = append(p, Row{Text: fmt.Sprint("row ", i), Cut: i == page*3 || i == page*3+4})
		}
		all, _ = JoinRows(all, p)
	}
	if len(all) != 62 || all[0].Text != "row 0" || all[61].Text != "row 61" {
		t.Fatalf("%d rows; want 62, row 0 to row 61", len(all))
	}
}
