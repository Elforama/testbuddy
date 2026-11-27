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
	rows, view, area := Build(s, NewRefs()).Rows()
	if view != rect(0, 200, 1080, 2200) || area != "the scrollable column" {
		t.Errorf("view = %v, area %q; want the list's bounds, and the horizontal strip not counted", view, area)
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
	rows, view, area := Build(s, NewRefs()).Rows()
	if rowTexts(rows) != "Text \"One\"\nText \"Two\"" || view != rect(0, 0, phoneW, phoneH) || area != "" {
		t.Errorf("rows = %q in %v, area %q; want the screen's rows and no scroll area named", rowTexts(rows), view, area)
	}
}

func TestRowsLeaveOutMarksThatChangeWithTheScroll(t *testing.T) {
	s := android(
		nd(1, "androidx.recyclerview.widget.RecyclerView", rect(0, 200, 1080, 2200), scrollable),
		nd(2, "android.widget.TextView", rect(0, 150, 1080, 350), text("Half above")),
		nd(2, "android.widget.TextView", rect(0, 350, 1080, 550), text("Whole")),
	)
	rows, _, _ := Build(s, NewRefs()).Rows()
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

func TestRowsNameTheScrollArea(t *testing.T) {
	byID := android(
		nd(1, "androidx.recyclerview.widget.RecyclerView", rect(0, 200, 1080, 2200), scrollable, id("orders")),
		nd(2, "android.widget.TextView", rect(0, 300, 1080, 400), text("One")),
		nd(2, "android.widget.TextView", rect(0, 400, 1080, 500), text("Two")),
	)
	if _, _, area := Build(byID, NewRefs()).Rows(); area != "#orders" {
		t.Errorf("area = %q, want #orders", area)
	}
	panes := android(
		nd(1, "androidx.recyclerview.widget.RecyclerView", rect(0, 200, 400, 2200), scrollable),
		nd(2, "android.widget.TextView", rect(0, 300, 400, 400), text("Inbox")),
		nd(2, "android.widget.TextView", rect(0, 400, 400, 500), text("Sent")),
		nd(1, "androidx.recyclerview.widget.RecyclerView", rect(400, 200, 1080, 2200), scrollable),
		nd(2, "android.widget.TextView", rect(400, 300, 1080, 400), text("Mail 1")),
		nd(2, "android.widget.TextView", rect(400, 400, 1080, 500), text("Mail 2")),
	)
	rows, _, area := Build(panes, NewRefs()).Rows()
	if area != "the scrollable column, the largest of 2 scroll areas" || rowTexts(rows) != "Text \"Mail 1\"\nText \"Mail 2\"" {
		t.Errorf("area = %q, rows %q", area, rowTexts(rows))
	}
}

func TestJoinRowsPinnedHeader(t *testing.T) {
	expectJoin(t, whole("H", "1", "2", "3", "4"), whole("H", "3", "4", "5", "6"), true, "H", "1", "2", "3", "4", "5", "6")
	expectJoin(t, whole("A", "1", "2", "3", "4"), whole("B", "3", "4", "5", "6"), true, "A", "1", "2", "B", "3", "4", "5", "6")
	before := whole("Section A", "1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11", "12", "13")
	after := whole("7", "Section A", "8", "9", "10", "11", "12", "13", "14", "15", "16")
	all, ok := JoinRows(before, after)
	want := "Section A\n1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11\n12\n13\n14\n15\n16"
	if !ok || rowTexts(all) != want {
		t.Fatalf("joined %v:\n%s", ok, rowTexts(all))
	}
	expectJoin(t, all, whole("12", "Section A", "13", "14", "15", "16", "Section B", "17"), true, strings.Split(want+"\nSection B\n17", "\n")...)
}

func TestJoinRowsTransientLastRow(t *testing.T) {
	before := whole("Post 7", "Post 8", "Post 9", "Post 10", "Loading")
	expectJoin(t, before, whole("Post 9", "Post 10", "Post 11", "Post 12"), true, "Post 7", "Post 8", "Post 9", "Post 10", "Post 11", "Post 12")
	after := append([]Row{cut("Post 8 top")}, whole("Post 9", "Post 10", "Post 11")...)
	expectJoin(t, before, after, true, "Post 7", "Post 8", "Post 9", "Post 10", "Post 11")
	expectJoin(t, whole("1", "2", "3", "x", "y", "z"), whole("2", "3", "4"), false, "1", "2", "3", "x", "y", "z", "2", "3", "4")
}

func TestJoinRowsUpwardsWithPinnedHeader(t *testing.T) {
	old := whole("30", "Section B", "31", "32", "33")
	page := whole("22", "Section A", "23", "24", "25", "Section B", "26", "27", "28", "29", "30", "31")
	expectJoin(t, page, old, true, "22", "Section A", "23", "24", "25", "Section B", "26", "27", "28", "29", "30", "31", "32", "33")
}

func TestJoinRowsPinnedHeaderUnderTwoCoveredRows(t *testing.T) {
	before := whole("Section C", "72", "73", "74", "75", "Section D", "76", "77", "78", "79")
	after := whole("74", "75", "Section C", "Section D", "76", "77", "78", "79", "80", "81")
	expectJoin(t, before, after, true, "Section C", "72", "73", "74", "75", "Section D", "76", "77", "78", "79", "80", "81")
}
