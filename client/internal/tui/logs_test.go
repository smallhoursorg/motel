package tui

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/smallhoursorg/motel/client/internal/tui/demo"
	"github.com/smallhoursorg/motel/client/internal/tui/model"
)

// TestLogsScreen: l on an app's screen opens its lines, esc goes back to
// it; :logs <app> goes there from anywhere, the app's screen under it,
// and Tab offers it; c gives the command that follows the same lines; a
// row up holds the view and End follows again; an app with no logs
// grant says which grant, and nothing is asked.
func TestLogsScreen(t *testing.T) {
	h := start(t, 80, 24, nil)
	h.open(app("blog"))
	h.press("l")
	if v := h.top(); v.Screen != model.ScreenLogs || v.App != "blog" {
		t.Fatalf("l opened %+v", v)
	}
	if rows := screenRows(h); !strings.HasPrefix(rows[1].text, "apps › blog › logs") || !strings.Contains(h.screen(), "── following: a read every 2 s") {
		t.Errorf("the logs screen:\n%s", h.screen())
	}
	h.press("k")
	if !strings.Contains(h.screen(), "── held: 0 new lines since · End follows") {
		t.Errorf("k does not hold the view:\n%s", h.screen())
	}
	h.press(tcell.KeyEnd)
	if !strings.Contains(h.screen(), "── following") {
		t.Errorf("End does not follow:\n%s", h.screen())
	}
	h.press("c")
	if s := h.screen(); !strings.Contains(s, "motel logs --box deploy.example.com -- blog") {
		t.Errorf("c:\n%s", s)
	}
	h.press(tcell.KeyEscape, tcell.KeyEscape)
	if v := h.top(); v != app("blog") {
		t.Fatalf("esc went to %+v", v)
	}
	var offered []string
	h.on(func() { h.u.promptMode = ':'; offered = h.u.complete("lo"); h.u.promptMode = 0 })
	if !slices.Contains(offered, "logs blog") {
		t.Errorf("Tab offers %q", offered)
	}
	h.press(":", "logs shop", tcell.KeyEnter)
	var stack []model.View
	h.on(func() { stack = slices.Clone(h.u.stack) })
	if n := len(stack); stack[n-1] != (model.View{Screen: model.ScreenLogs, App: "shop"}) || stack[n-2] != app("shop") {
		t.Fatalf(":logs shop: %+v", stack)
	}
	if s := h.screen(); !strings.Contains(s, "needs a logs grant on shop") {
		t.Errorf("no grant on shop:\n%s", s)
	}
	// Its footer offers no key that scrolls, follows or wraps.
	if rows := screenRows(h); !strings.HasPrefix(rows[len(rows)-1].text, logsSaysFooter) {
		t.Errorf("no grant: the footer %q", rows[len(rows)-1].text)
	}
	for _, c := range h.fake.Calls {
		if c.Job.Kind == model.KLogs && c.Job.App == "shop" {
			t.Errorf("shop's lines were asked for without a grant")
		}
	}
}

// whoamiBody is the demo person's whoami with the roles given.
func whoamiBody(roles string) string {
	return `{"name":"alice@example.com","key":"SHA256:SAMPLE0000000000000000000000000000000000000","roles":[` + roles + `],"box":"deploy.example.com","version":"v0.7.0","protocol":1,"token_expires_at":"@9m"}`
}

// TestLogsKeysGoByWhatIsShown: with lines shown the screen follows them;
// when its grant goes (whoami), the lines are not shown, the footer
// offers no scroll, follow or wrap, and those keys do nothing (k and
// PgUp hold nothing, w wraps nothing); with the grant back it follows,
// not held.
func TestLogsKeysGoByWhatIsShown(t *testing.T) {
	var lines []string
	for i := range 50 {
		lines = append(lines, fmt.Sprintf(`{"at":"@-%ds","release":"v1","text":"line %d"}`, 100-i, i))
	}
	granted := `{"role":"viewer","scope":"box"},{"role":"logs","scope":"app:blog"}`
	h := start(t, 80, 24, func(f *demo.Fake) {
		f.Body("logs/blog.json", `{"kept":"on","lines":[`+strings.Join(lines, ",")+`],"more":false,"gap":false,"cursor":"c1"}`)
		f.Body("whoami.json", whoamiBody(granted))
	})
	h.press(":", "logs blog", tcell.KeyEnter)
	if !strings.Contains(h.screen(), "── following") {
		t.Fatalf("not following:\n%s", h.screen())
	}
	whoami := func(roles string) {
		h.fake.Body("whoami.json", whoamiBody(roles))
		h.now = h.now.Add(model.AgainAfter)
		h.on(func() { h.s.Engine.Again(h.now, model.Job{Kind: model.KWhoAmI}) })
		h.pump()
	}
	whoami(`{"role":"viewer","scope":"box"}`)
	h.press("k", tcell.KeyPgUp, tcell.KeyHome, "w")
	rows := screenRows(h)
	if foot := rows[len(rows)-1].text; foot != logsSaysFooter {
		t.Errorf("no grant: the footer %q", foot)
	}
	var wrapped bool
	h.on(func() { wrapped = h.u.wrap })
	if wrapped {
		t.Error("w wrapped with no line shown")
	}
	whoami(granted)
	if s := h.screen(); !strings.Contains(s, "── following") || strings.Contains(s, "held") {
		t.Errorf("the grant back:\n%s", s)
	}
}

// TestLogsSaysFooterDuring429: the footer of a logs screen that shows no
// lines leaves ctrl-r out while a 429's wait runs, as every footer does.
func TestLogsSaysFooterDuring429(t *testing.T) {
	h := start(t, 80, 24, nil)
	h.press(":", "logs shop", tcell.KeyEnter)
	foot := func() string { rows := screenRows(h); return rows[len(rows)-1].text }
	if f := foot(); !strings.Contains(f, "ctrl-r") {
		t.Fatalf("no ctrl-r in %q", f)
	}
	h.fake.Set("apps", &model.Failure{Kind: model.FailSlowed, Message: "slow down", RetryAfter: 30 * time.Second})
	h.now = h.now.Add(model.AppsEvery)
	h.pump()
	if f := foot(); strings.Contains(f, "ctrl-r") {
		t.Errorf("during a 429's wait: %q", f)
	}
}

// TestLogsHostileWrapped: the hostile app's lines with w on, at both
// sizes (a golden each). The wrap is set before the screen opens: w now
// acts only once lines are shown, and a frame drawn unwrapped first
// leaves a rune in tcell's simulation buffer under the thumbs-up's
// second cell (the same before this change), which the dump would read.
func TestLogsHostileWrapped(t *testing.T) {
	for _, sz := range sizes {
		name := fmt.Sprintf("logs-hostile-wrapped-%dx%d", sz[0], sz[1])
		t.Run(name, func(t *testing.T) {
			h := start(t, sz[0], sz[1], nil)
			h.on(func() { h.u.wrap = true })
			h.press(":", "logs hostile", tcell.KeyEnter)
			golden(t, name, h.screen())
		})
	}
}
