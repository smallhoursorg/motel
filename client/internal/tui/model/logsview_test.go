package model_test

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/smallhoursorg/motel/client/internal/tui/demo"
	"github.com/smallhoursorg/motel/client/internal/tui/model"
	"github.com/smallhoursorg/motel/liveswap/wire"
)

// drive makes the reads due at now, at most n, as the TUI's driver
// does: the engine records each, then its answer is applied.
func drive(s *model.Session, f *demo.Fake, now time.Time, n int) {
	for range n {
		j, ok, _ := s.Engine.Take(now)
		if !ok {
			return
		}
		apply, err := s.Fetch(context.Background(), f, j)
		s.Engine.Done(j, now, err)
		apply()
	}
}

// logsCalls is the cursor each logs read sent, in order.
func logsCalls(f *demo.Fake) []string {
	var out []string
	for _, c := range f.Calls {
		if c.Job.Kind == model.KLogs {
			out = append(out, c.After)
		}
	}
	return out
}

// logsBody is a logs answer of n lines, the first numbered from, each a
// second apart from at.
func logsBody(at time.Time, from, n int, more, gap bool, cursor string) string {
	l := wire.Logs{Kept: wire.KeptOn, Lines: []wire.LogLine{}, More: more, Gap: gap, Cursor: cursor}
	for i := range n {
		l.Lines = append(l.Lines, wire.LogLine{At: at.Add(time.Duration(from+i) * time.Second), Release: "7c1e55d0a913", Text: fmt.Sprintf("line %d", from+i)})
	}
	b, _ := json.Marshal(l)
	return string(b)
}

// logsOpen is a session over the fake, whoami and the list read, with
// the logs screen of app opened at now.
func logsOpen(t *testing.T, app string, setup func(*demo.Fake)) (*model.Session, *demo.Fake, *time.Time) {
	t.Helper()
	s, f, now := session(t)
	if setup != nil {
		setup(f)
	}
	drive(s, f, *now, 2)
	s.LogsOpen(app, *now)
	drive(s, f, *now, 20)
	return s, f, now
}

func logsText(s *model.Session, now time.Time, width, height int) string {
	return text(s.LogsViewFit("blog", now, width, height, runes, false))
}

// TestLogsScreenFollows: the logs screen reads a tail when it opens (the
// fixture's newest lines, older ones not sent said first), then every
// LogsEvery the lines after the cursor the last answer gave, each new
// line at the foot of the view; a follow that stopped with lines left
// reads again at once; kept off is said and nothing more is read, until
// ctrl-r.
func TestLogsScreenFollows(t *testing.T) {
	s, f, now := logsOpen(t, "blog", nil)
	if got := logsCalls(f); len(got) != 1 || got[0] != "" {
		t.Fatalf("opening read %q, want one tail", got)
	}
	v := logsText(s, *now, 100, 30)
	for _, want := range []string{"── following: a read every 2 s", "-- older lines were not sent: a read sends at most 200 lines", "-- 2026-11-15 UTC, release e9a0b3f2c118 --",
		"-- 2026-11-15 UTC, release not kept by the journal --", "-- 2026-11-15 UTC, release 7c1e55d0a913 --", "GET / 200 11ms"} {
		if !strings.Contains(v, want) {
			t.Errorf("no %q:\n%s", want, v)
		}
	}
	var first wire.Logs
	if err := json.Unmarshal([]byte(must(t, s, "blog")), &first); err != nil {
		t.Fatal(err)
	}
	f.Body("logs/blog.json", logsBody(*now, 0, 1, false, false, "c2"))
	drive(s, f, *now, 5)
	if n := len(logsCalls(f)); n != 1 {
		t.Errorf("%d reads within LogsEvery of the first", n)
	}
	*now = now.Add(model.LogsEvery)
	drive(s, f, *now, 5)
	if got := logsCalls(f); len(got) != 2 || got[1] != first.Cursor {
		t.Fatalf("the follow sent %q, want the tail's cursor %q", got, first.Cursor)
	}
	rows := strings.Split(strings.TrimRight(logsText(s, *now, 100, 30), "\n"), "\n")
	if last := rows[len(rows)-1]; !strings.HasSuffix(last, "line 0") {
		t.Errorf("the new line is not at the foot: %q", last)
	}
	// More after a follow: the next read at once, with its cursor.
	*now = now.Add(model.LogsEvery)
	f.Body("logs/blog.json", logsBody(*now, 1, 2, true, false, "c3"))
	drive(s, f, *now, 1)
	f.Body("logs/blog.json", logsBody(*now, 3, 1, false, false, "c4"))
	drive(s, f, *now, 5)
	if got := logsCalls(f); len(got) != 4 || got[2] != "c2" || got[3] != "c3" {
		t.Errorf("after more: %q, want c2 then c3 at once", got)
	}
	drive(s, f, now.Add(model.LogsEvery-100*time.Millisecond), 20)
	if n := len(logsCalls(f)); n != 4 {
		t.Errorf("after a read with no more, %d reads within LogsEvery", n)
	}
	// Kept off: said, and nothing more asked until ctrl-r.
	*now = now.Add(model.LogsEvery)
	f.Body("logs/blog.json", `{"kept":"off","lines":[],"more":false,"gap":false}`)
	for range 10 {
		drive(s, f, *now, 5)
		*now = now.Add(time.Second)
	}
	if n := len(logsCalls(f)); n != 5 {
		t.Errorf("%d reads, want 5: none after kept off", n)
	}
	v = logsText(s, *now, 0, 30)
	for _, want := range []string{"deploy.example.com keeps blog's output on the box (deploy_log_lines 0): it sends no line of it. Nothing more is asked.", "── not following", "line 3"} {
		if !strings.Contains(v, want) {
			t.Errorf("kept off: no %q:\n%s", want, v)
		}
	}
	s.LogsAgain(*now)
	s.Engine.Again(*now, model.Job{Kind: model.KLogs, App: "blog"})
	drive(s, f, *now, 5)
	if n := len(logsCalls(f)); n != 6 {
		t.Errorf("ctrl-r: %d reads, want 6", n)
	}
	// Left, nothing is read; opened again, a fresh tail.
	s.LogsShown("", *now)
	*now = now.Add(time.Minute)
	drive(s, f, *now, 20)
	if n := len(logsCalls(f)); n != 6 {
		t.Errorf("%d reads with the screen closed", n)
	}
	f.Body("logs/blog.json", logsBody(*now, 9, 1, false, false, "c9"))
	s.LogsOpen("blog", *now)
	drive(s, f, *now, 20)
	if got := logsCalls(f); len(got) != 7 || got[6] != "" {
		t.Errorf("opened again: %q, want a tail", got)
	}
	if v := logsText(s, *now, 100, 30); strings.Contains(v, "line 3") || !strings.Contains(v, "line 9") {
		t.Errorf("opened again, the lines of the last opening are kept:\n%s", v)
	}
}

func must(t *testing.T, s *model.Session, app string) string {
	t.Helper()
	b, _, ok := s.Body(model.View{Screen: model.ScreenLogs, App: app})
	if !ok {
		t.Fatal("no answer held")
	}
	return string(b)
}

// TestLogsNeedAGrant: without a logs grant on the app, whoami's answer
// says so, the screen says which grant, and nothing is asked; before
// whoami answers about this key, nothing is asked either. A grant whoami
// lists later starts the read.
func TestLogsNeedAGrant(t *testing.T) {
	viewer := `{"name":"alice@example.com","key":"SHA256:SAMPLE0000000000000000000000000000000000000","roles":[{"role":"viewer","scope":"box"}],"box":"deploy.example.com","version":"v0.7.0","protocol":1,"token_expires_at":"@9m"}`
	s, f, now := logsOpen(t, "blog", func(f *demo.Fake) { f.Body("whoami.json", viewer) })
	for range 10 {
		*now = now.Add(time.Second)
		drive(s, f, *now, 10)
	}
	if n := len(logsCalls(f)); n != 0 {
		t.Errorf("%d logs reads without a grant", n)
	}
	if v := logsText(s, *now, 0, 30); !strings.Contains(v, "reading blog's lines needs a logs grant on blog or on the box, and whoami (as of 14:00:00 UTC) lists none for this key here, so nothing is asked of deploy.example.com.") {
		t.Errorf("no grant:\n%s", v)
	}
	if _, _, have := s.Body(model.View{Screen: model.ScreenLogs, App: "blog"}); have {
		t.Error("an answer to show without a grant")
	}
	f.Body("whoami.json", strings.Replace(viewer, `"scope":"box"}`, `"scope":"box"},{"role":"logs","scope":"app:blog"}`, 1))
	*now = now.Add(model.AgainAfter)
	s.Engine.Again(*now, model.Job{Kind: model.KWhoAmI})
	drive(s, f, *now, 10)
	if got := logsCalls(f); len(got) != 1 || got[0] != "" {
		t.Errorf("a grant listed: %q, want a tail", got)
	}
	// whoami not yet known: nothing asked.
	s2, f2, now2 := session(t)
	f2.Set("whoami", &model.Failure{Kind: model.FailNoAnswer, Message: "no answer"})
	drive(s2, f2, *now2, 2)
	s2.LogsOpen("blog", *now2)
	drive(s2, f2, *now2, 10)
	if n := len(logsCalls(f2)); n != 0 {
		t.Errorf("%d logs reads before whoami answered", n)
	}
	if v := logsText(s2, *now2, 0, 30); !strings.Contains(v, "not known until whoami answers") {
		t.Errorf("whoami not known:\n%s", v)
	}
}

// TestLogsLayout: each row of the lines is a line's time, a divider or
// a note of the client's at the left edge, or, wrapped, the rest of a
// line or a note indented past where either begins; every control
// character an app wrote is an escape; cut at the edge, a line is
// marked.
func TestLogsLayout(t *testing.T) {
	s, _, now := logsOpen(t, "hostile", nil)
	for _, wrap := range []bool{false, true} {
		v := s.LogsViewFit("hostile", *now, 60, 200, runes, wrap)
		lines := 0
		for _, l := range v[1:] {
			r := l.Text()
			switch {
			case len(r) > 10 && r[2] == ':' && r[5] == ':' && r[8:10] == "  ":
				lines++
			case strings.HasPrefix(r, "-- "):
			case wrap && strings.HasPrefix(r, "          "):
			default:
				t.Errorf("wrap %v: a row that is no line, divider or note: %q", wrap, r)
			}
			if wrap && partEscape.MatchString(r) {
				t.Errorf("a row ends inside an escape: %q", r)
			}
			if strings.ContainsFunc(r, func(c rune) bool { return c < 0x20 || c == 0x7f || c >= 0x80 && c < 0xa0 || c == '\u202e' }) {
				t.Errorf("a control character reached the screen: %q", r)
			}
			if !wrap && utf8.RuneCountInString(r) > 60 {
				t.Errorf("a row wider than the window: %q", r)
			}
		}
		if lines != 10 {
			t.Errorf("wrap %v: %d lines' first rows, want 10", wrap, lines)
		}
		joined := text(v)
		if !strings.Contains(joined, `osc52 \x1b]52;c;`) || !strings.Contains(joined, `\u202elmth.exe`) {
			t.Errorf("wrap %v: the escapes are not shown:\n%s", wrap, joined)
		}
		if !wrap && !strings.Contains(joined, "xxxxxxxx…\n") {
			t.Errorf("a long line is not cut and marked:\n%s", joined)
		}
		for _, r := range strings.Split(joined, "\n") {
			if strings.Contains(r, "a line of its") && !strings.HasPrefix(r, "          ") {
				t.Errorf("wrap %v: a padded line's rest begins a row: %q", wrap, r)
			}
		}
	}
}

// partEscape is a row that ends with part of one of termtext's escapes.
var partEscape = regexp.MustCompile(`\\(x[0-9a-f]?|u[0-9a-f]{0,3})$`)

// TestLogsBudget: the logs screen's follow is counted in the budget the
// other apps share: with it open, a viewer of the box's other apps keep
// 30 s to 23 apps, as the README says, and not to 24.
func TestLogsBudget(t *testing.T) {
	for _, c := range []struct {
		n    int
		at30 bool
	}{{23, true}, {24, false}} {
		e := model.NewEngine(t0)
		e.SetWatch(model.WatchBox, t0)
		e.LogsOpen("app-0", t0)
		e.SetApps(names(c.n), t0)
		if got := e.Interval("app-1") == model.BackgroundEvery; got != c.at30 {
			t.Errorf("%d apps: every %s", c.n, e.Interval("app-1"))
		}
	}
}

// TestLogsHeldAndCapped: scrolled up, the view stays on the rows it
// showed while lines arrive, and the rule counts them; End follows
// again. Past LogsKeep lines the oldest are dropped, and said.
func TestLogsHeldAndCapped(t *testing.T) {
	s, f, now := logsOpen(t, "blog", func(f *demo.Fake) { f.Body("logs/blog.json", logsBody(t0, 0, 1000, false, false, "c1")) })
	view := func() []string {
		return strings.Split(strings.TrimRight(logsText(s, *now, 80, 12), "\n"), "\n")
	}
	s.LogsScroll("blog", -3, 80, runes, false)
	held := view()
	for i := 1; i <= 5; i++ {
		f.Body("logs/blog.json", logsBody(t0, 1000*i, 1000, false, false, fmt.Sprintf("c%d", i+1)))
		*now = now.Add(model.LogsEvery)
		drive(s, f, *now, 5)
	}
	got := view()
	if !strings.Contains(got[1], "held: 5,000 new lines since · End follows") {
		t.Errorf("the rule: %q", got[1])
	}
	if got[0] != "this screen holds the newest 5,000 lines: 1,000 older ones were dropped." {
		t.Errorf("the head: %q", got[0])
	}
	// What it showed was dropped: it holds at the oldest line held.
	if !strings.Contains(got[3], "line 1000") {
		t.Errorf("held past the cap, the view begins %q", got[2:4])
	}
	_ = held
	s.LogsEnd("blog")
	if got := view(); !strings.HasSuffix(got[len(got)-1], "line 5999") || !strings.Contains(got[1], "following") {
		t.Errorf("End: %q", got)
	}
	// Held within what is kept: the same rows, whatever arrives.
	s.LogsScroll("blog", -20, 80, runes, false)
	before := view()
	f.Body("logs/blog.json", logsBody(t0, 6000, 3, false, false, "c9"))
	*now = now.Add(model.LogsEvery)
	drive(s, f, *now, 5)
	after := view()
	if strings.Join(before[2:], "\n") != strings.Join(after[2:], "\n") || !strings.Contains(after[1], "held: 3 new lines since") {
		t.Errorf("held, the view moved:\n%s\n---\n%s", strings.Join(before, "\n"), strings.Join(after, "\n"))
	}
	// Scrolled down to the newest row: following again.
	s.LogsScroll("blog", 100, 80, runes, false)
	if got := view(); !strings.Contains(got[1], "following") || !strings.HasSuffix(got[len(got)-1], "line 6002") {
		t.Errorf("scrolled to the end: %q", got)
	}
}

// TestLogsFallenBehind: a follow whose reads at once have brought
// LogsKeep lines and still have more goes on from a tail, said once.
func TestLogsFallenBehind(t *testing.T) {
	s, f, now := logsOpen(t, "blog", func(f *demo.Fake) { f.Body("logs/blog.json", logsBody(t0, 0, 1000, true, false, "c")) })
	*now = now.Add(model.LogsEvery)
	drive(s, f, *now, 40)
	if got := logsCalls(f); strings.Join(got, ",") != ",c,c,c,c,c," {
		t.Errorf("the reads sent %q: a tail, five follows at once, then a tail", got)
	}
	v := logsText(s, *now, 0, 6000)
	skip := strings.Index(v, "-- lines skipped: this follow fell more than the 5,000 lines this screen holds behind the app")
	if skip < 0 || strings.Count(v, "-- lines skipped") != 1 || strings.Contains(v[skip:], "older lines were not sent") {
		t.Errorf("the notes: skipped at %d, %d times; the tail's own note after it: %v", skip, strings.Count(v, "-- lines skipped"), skip >= 0 && strings.Contains(v[skip:], "older lines were not sent"))
	}
}

// TestLogsBusy: a busy box's 503 (FailBusy) has the logs read asked
// again after its wait, the rest of the session not slowed and the box
// not said to refuse; a gap is said among the lines.
func TestLogsBusy(t *testing.T) {
	s, f, now := logsOpen(t, "blog", nil)
	*now = now.Add(model.LogsEvery)
	f.Set("logs:blog", &model.Failure{Kind: model.FailBusy, Message: "deploy.example.com answered 503", RetryAfter: 5 * time.Second})
	drive(s, f, *now, 20)
	f.Set("logs:blog", nil)
	if n := len(logsCalls(f)); n != 2 {
		t.Fatalf("%d reads, want the tail and the busy one", n)
	}
	drive(s, f, now.Add(5*time.Second-100*time.Millisecond), 20)
	if n := len(logsCalls(f)); n != 2 {
		t.Errorf("read again within the box's 5 s")
	}
	if s.Engine.RateNow() != model.Rate {
		t.Errorf("the rate slowed to %v", s.Engine.RateNow())
	}
	if _, r := s.Title(*now); strings.Contains(r.Text(), "refused") || strings.Contains(r.Text(), "slowed") {
		t.Errorf("a busy box said in the title bar: %q", r.Text())
	}
	if v := logsText(s, *now, 0, 30); !strings.Contains(v, "the last read failed: deploy.example.com answered 503") {
		t.Errorf("busy:\n%s", v)
	}
	f.Body("logs/blog.json", logsBody(*now, 0, 1, false, true, "c7"))
	*now = now.Add(5 * time.Second)
	drive(s, f, *now, 5)
	if v := logsText(s, *now, 0, 30); !strings.Contains(v, "-- a gap: the box no longer holds the line") || strings.Contains(v, "the last read failed") {
		t.Errorf("a gap:\n%s", v)
	}
}
