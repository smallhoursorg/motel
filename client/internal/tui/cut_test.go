package tui

import (
	"strconv"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/smallhoursorg/motel/client/internal/tui/demo"
	"github.com/smallhoursorg/motel/client/internal/tui/model"
)

// The details above an app's deploys and a record's log tail get at
// most half the window's height (#566): when they have more lines, the
// last row that shows says how many are not shown and that y shows the
// whole answer, so no line is dropped unsaid.

const moreSaid = " more lines: y shows the whole answer"

// detailRows is the rows of the screen's details: from under the crumb
// to the row above the line that opens the table or the tail.
func detailRows(t *testing.T, h *harness, opens string) []string {
	t.Helper()
	var out []string
	for _, r := range screenRows(h)[2:] {
		if strings.HasPrefix(r.text, opens) {
			return out
		}
		out = append(out, r.text)
	}
	t.Fatalf("no row opens with %q:\n%s", opens, h.screen())
	return nil
}

// cutSaid is the number the last row says is not shown, or -1 when that
// row is no such notice.
func cutSaid(rows []string) int {
	if len(rows) == 0 {
		return -1
	}
	n, ok := strings.CutPrefix(strings.TrimRight(rows[len(rows)-1], " "), "… ")
	if !ok {
		return -1
	}
	n, ok = strings.CutSuffix(n, moreSaid)
	if !ok {
		return -1
	}
	i, err := strconv.Atoi(n)
	if err != nil {
		return -1
	}
	return i
}

// TestDetailsCutSaid: an app's details and a record's, cut to half the
// window, end in a row that counts the lines not shown, the row's own
// among them; where every line fits, no row says so.
func TestDetailsCutSaid(t *testing.T) {
	for _, c := range []struct {
		name  string
		view  model.View
		opens string
	}{
		{"an app", app("api"), "─ DEPLOY"},
		{"a record", record("hostile", "v7"), "── log tail"},
	} {
		// At 100x60 the details have their 30 rows, more than either has.
		h := start(t, 100, 60, nil)
		h.open(c.view)
		all := detailRows(t, h, c.opens)
		if n := cutSaid(all); n != -1 {
			t.Fatalf("%s at 100x60: the details fit and still say %d more lines:\n%s", c.name, n, h.screen())
		}
		if len(all) <= 8 {
			t.Fatalf("%s: %d lines of details, which 60x16's 8 rows hold; the test needs more", c.name, len(all))
		}
		h = start(t, 60, 16, nil)
		h.open(c.view)
		rows := detailRows(t, h, c.opens)
		if len(rows) != 8 {
			t.Errorf("%s at 60x16: the details take %d rows, want half the window, 8:\n%s", c.name, len(rows), h.screen())
		}
		if got, want := cutSaid(rows), len(all)-len(rows)+1; got != want {
			t.Errorf("%s at 60x16: the last row of the details says %d more lines, want %d:\n%s", c.name, got, want, h.screen())
		}
	}
}

// TestFailedSentenceCutSaid: an app's "the last read failed" sentence
// that fills half the window (a 409 quoting 200 bidi overrides, laid on
// rows at 60x16) leaves its last row to say what is not shown: the
// sentence's end and the fields under it.
func TestFailedSentenceCutSaid(t *testing.T) {
	long := strings.Repeat("\u202e", 200)
	h := start(t, 100, 60, nil)
	h.open(app("blog"))
	fields := len(detailRows(t, h, "─ DEPLOY"))
	h = start(t, 60, 16, nil)
	h.open(app("blog"))
	h.fake.Set("blog", &model.Failure{Kind: model.FailThis, Message: demo.Host + ` answered 409; the box says: "` + long + `"`})
	h.now = h.now.Add(model.MeAlways)
	h.press(tcell.KeyCtrlR)
	rows := detailRows(t, h, "─ DEPLOY")
	if len(rows) != 8 || !strings.HasPrefix(rows[0], "the last read failed: ") {
		t.Fatalf("the details are not the failed sentence over 8 rows:\n%s", h.screen())
	}
	if n := cutSaid(rows); n <= fields {
		t.Errorf("the last row says %d more lines, want more than the %d fields under the sentence:\n%s", n, fields, h.screen())
	}
}
