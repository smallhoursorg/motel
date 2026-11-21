package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/smallhoursorg/motel/client/internal/termtext"
	"github.com/smallhoursorg/motel/client/internal/tui/demo"
	"github.com/smallhoursorg/motel/client/internal/tui/hostile"
	"github.com/smallhoursorg/motel/client/internal/tui/model"
)

// away lets d pass with no key pressed, the clock's tick drawing a
// frame each second and the reads made as they come due.
func (h *harness) away(d time.Duration) {
	for end := h.now.Add(d); h.now.Before(end); {
		h.now = h.now.Add(time.Second)
		h.pump()
		h.sync()
	}
}

// TestPausedStillWatches: a person away for twenty minutes, the key not
// a hardware one: past the pause the title bar says the alerts are
// still read, an alert that fires then is counted, moves the mark and
// rings the bell, and the key that resumes sees it.
func TestPausedStillWatches(t *testing.T) {
	crit := alertRow("cert_failed", "critical", t0.Add(-time.Hour))
	h, tty := bellTTY(t, true, func(f *demo.Fake) { f.Body("box-alerts.json", boxAlerts(crit)) })
	h.away(model.Idle + 5*time.Minute)
	if _, r := h.s.Title(h.now); r.Text() != "PAUSED (idle 10m), alerts still read: any key resumes" {
		t.Fatalf("the title bar's word: %q", r.Text())
	}
	if m := h.s.Mark(h.now); m.Long.Text() != "✖ 1 firing" {
		t.Errorf("paused, the mark: %q (not old: the alerts are read)", m.Long.Text())
	}
	h.fake.Body("box-alerts.json", boxAlerts(alertRow("disk_low", "warning", h.now), crit))
	h.away(model.PausedWatchEvery + time.Second)
	if n := hostile.BareBELs(tty.Bytes()); n != 1 {
		t.Errorf("paused, an alert arrives: %d bells", n)
	}
	if m := h.s.Mark(h.now); m.Long.Text() != "✖ 2 firing, 1 new" {
		t.Errorf("paused, the mark: %q", m.Long.Text())
	}
	h.away(5 * time.Minute)
	if m := h.s.Mark(h.now); m.Long[0].C != model.LoudBad {
		t.Errorf("five minutes on, still away: the mark is not held reversed: %v", m.Long[0].C)
	}
	h.press("x")
	if m := h.s.Mark(h.now); m.Long.Text() != "✖ 2 firing" || m.Long[0].C != model.Bad {
		t.Errorf("after the key that resumes: %q (%v)", m.Long.Text(), m.Long[0].C)
	}
	if _, r := h.s.Title(h.now); strings.HasPrefix(r.Text(), "PAUSED") {
		t.Errorf("after the key the title bar still says %q", r.Text())
	}
}

// TestPausedHardwareKey: with a hardware key every read stops at the
// pause, the watch's too: the title bar keeps today's word, the mark
// says its count is old, an alert that fires meanwhile rings nothing,
// and who am I says why.
func TestPausedHardwareKey(t *testing.T) {
	crit := alertRow("cert_failed", "critical", t0.Add(-time.Hour))
	// The token's own life: it ends during the pause, and who am I still
	// says why nothing is read.
	h, tty := bellTTY(t, true, func(f *demo.Fake) { f.Body("box-alerts.json", boxAlerts(crit)) }, func(s *model.Session) { s.Key.Hardware = true })
	h.away(model.Idle + time.Second)
	before := len(h.fake.Calls)
	h.fake.Body("box-alerts.json", boxAlerts(alertRow("disk_low", "warning", h.now), crit))
	h.away(5 * time.Minute)
	if n := len(h.fake.Calls) - before; n != 0 {
		t.Errorf("a hardware key, paused: %d reads", n)
	}
	if _, r := h.s.Title(h.now); r.Text() != "PAUSED (idle 10m): any key resumes" {
		t.Errorf("the title bar's word: %q", r.Text())
	}
	if m := h.s.Mark(h.now); m.Short.Text() != "✖ 1?" {
		t.Errorf("the mark: %q", m.Short.Text())
	}
	if n := hostile.BareBELs(tty.Bytes()); n != 0 {
		t.Errorf("%d bells while paused", n)
	}
	var me strings.Builder
	for _, l := range h.s.MeView(h.now) {
		me.WriteString(l.Text() + " ")
	}
	if !strings.Contains(me.String(), "a hardware key is not asked for a touch while you are away, so the alerts are not read then") {
		t.Errorf("who am I does not say why:\n%s", me.String())
	}
}

// TestPausedWordEveryMark: beside the paused word that says the alerts
// are still read, 53 cells, every mark the title bar can draw is drawn
// first, in the form that fits, and the word whole at the edge of 60
// columns.
func TestPausedWordEveryMark(t *testing.T) {
	word := model.Line{{T: termtext.From("PAUSED (idle 10m), alerts still read: any key resumes")}}
	left := model.Line{{T: termtext.From("motel · deploy.example.com │ alice@example.com · viewer of the box")}}
	own := func(s string) model.Line { return model.Line{{T: termtext.From(s)}} }
	for _, m := range []model.MarkForms{
		{Long: own("✖ 2 firing"), Short: own("✖ 2"), Tiny: own("✖2"), Glyph: own("✖")},
		{Long: own("✖ 12 firing, 3 new"), Short: own("✖ 12"), Tiny: own("✖12"), Glyph: own("✖")},
		{Long: own("▲ 500+ firing"), Short: own("▲ 500+"), Tiny: own("▲500+"), Glyph: own("▲")},
		{Long: own("✖ 2 firing as of 3m ago"), Short: own("✖ 2?"), Tiny: own("✖2?"), Glyph: own("✖?")},
		{Long: own("0 firing as of 3m ago"), Short: own("0?"), Tiny: own("0?"), Glyph: own("?")},
		{Long: own("0+ firing"), Short: own("0+"), Tiny: own("0+"), Glyph: own("?")},
		{Long: own("alerts not known"), Short: own("alerts ?"), Tiny: own("?"), Glyph: own("?")},
		{Long: own("alerts…"), Short: own("alerts…"), Tiny: own("…"), Glyph: own("…")},
	} {
		got := bar(m, left, word, 60).Text()
		if !strings.HasSuffix(got, " "+word.Text()) || len([]rune(got)) != 60 {
			t.Errorf("%q: the word is not whole at the edge: %q", m.Long.Text(), got)
		}
		drawn := false
		for _, f := range []model.Line{m.Long, m.Short, m.Tiny, m.Glyph} {
			drawn = drawn || strings.HasPrefix(got, f.Text()+" ")
		}
		if !drawn {
			t.Errorf("%q: no form of the mark is drawn first: %q", m.Long.Text(), got)
		}
	}
}

// TestPausedWatchFails: while paused, the watch's read getting no answer
// is not said as alerts still read: the title bar keeps the plain
// PAUSED word, and the mark says its count is old; once it answers
// again, the word says they are read.
func TestPausedWatchFails(t *testing.T) {
	h := start(t, 100, 30, nil)
	h.away(model.Idle + time.Minute)
	h.fake.Set("box-alerts", fiveHundred())
	h.away(10 * time.Minute)
	if _, r := h.s.Title(h.now); r.Text() != "PAUSED (idle 10m): any key resumes" {
		t.Errorf("paused, the alerts reads failing: the title bar says %q", r.Text())
	}
	if m := h.s.Mark(h.now); !strings.HasSuffix(m.Short.Text(), "?") {
		t.Errorf("the mark says %q", m.Short.Text())
	}
	h.fake.Set("box-alerts", nil)
	h.away(2 * time.Minute)
	if _, r := h.s.Title(h.now); r.Text() != "PAUSED (idle 10m), alerts still read: any key resumes" {
		t.Errorf("answered again: the title bar says %q", r.Text())
	}
}

// TestMeSaysThePause: who am I's token sentence says what is read while
// paused: only the alerts, each at most once a minute, for a key that
// needs no touch; nothing for a hardware key, and why; nothing to watch
// for a person with no grant here.
func TestMeSaysThePause(t *testing.T) {
	for _, c := range []struct {
		name, want string
		with       func(*model.Session)
		setup      func(*demo.Fake)
	}{
		{"an agent key", "only the alerts are read, each at most once a minute, and it is signed for them", nil, nil},
		{"a hardware key", "a hardware key is not asked for a touch while you are away, so the alerts are not read then", func(s *model.Session) { s.Key.Hardware = true }, nil},
		{"no grant, a hardware key", "signed again a minute before it ends, and only while you use the screen", func(s *model.Session) { s.Key.Hardware = true }, func(f *demo.Fake) {
			f.Body("whoami.json", `{"name":"alice@example.com","key":"`+demo.Key.Fingerprint+`","roles":[],"box":"deploy.example.com","version":"v0.7.0","protocol":1}`)
		}},
	} {
		with := func(s *model.Session) {
			if c.with != nil {
				c.with(s)
			}
		}
		h := start(t, 100, 30, c.setup, with)
		var me strings.Builder
		for _, l := range h.s.MeView(h.now) {
			me.WriteString(l.Text() + " ")
		}
		if got := strings.Join(strings.Fields(me.String()), " "); !strings.Contains(got, c.want) || c.name == "no grant, a hardware key" && strings.Contains(got, "the alerts are") {
			t.Errorf("%s: who am I says:\n%s", c.name, got)
		}
	}
}

// TestPausedRefusalReleased: a viewer of the box whose alerts are
// refused (a 403) while paused has them read again, with no key, once
// whoami lists the box again: whoami is read through the pause too.
func TestPausedRefusalReleased(t *testing.T) {
	h := start(t, 100, 30, nil)
	h.away(model.Idle + time.Minute)
	h.fake.Set("box-alerts", &model.Failure{Kind: model.FailForbidden, Message: "no"})
	h.away(2 * time.Minute)
	if f := h.s.Firing(h.now); f.Why != model.WasRefused {
		t.Fatalf("refused: %+v", f)
	}
	h.fake.Set("box-alerts", nil)
	h.away(model.MeAlways + time.Minute)
	if f := h.s.Firing(h.now); f.Why != model.Known {
		t.Errorf("the box's grant back, still paused: %+v", f)
	}
}

// TestPausedGrantBack: a viewer of the box whose grant drops out of
// whoami while paused (another network, say) and comes back has the
// box's alerts watched again with no key: whoami is read through the
// pause whatever the watch.
func TestPausedGrantBack(t *testing.T) {
	h := start(t, 100, 30, nil)
	h.away(model.Idle + time.Minute)
	h.fake.Body("whoami.json", `{"name":"alice@example.com","key":"`+demo.Key.Fingerprint+`","roles":[],"box":"deploy.example.com","version":"v0.7.0","protocol":1}`)
	h.away(model.MeAlways + time.Minute)
	if f := h.s.Firing(h.now); !f.Nothing {
		t.Fatalf("no grant: %+v", f)
	}
	delete(h.fake.Bodies, "whoami.json")
	h.away(model.MeAlways + time.Minute)
	if f := h.s.Firing(h.now); f.Why != model.Known || f.N != 2 || f.Old {
		t.Errorf("the grant back, still paused: %+v", f)
	}
}
