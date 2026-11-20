package demo

import (
	"context"
	"encoding/json"
	"io/fs"
	"strconv"
	"time"
)

// The demo's alerts arriving (#576): under --demo, from its start, a
// critical alert fires at 20 s, a warning one at 70 s, and the first
// resolves at 130 s, so the title bar's mark, its movement and the bell
// can be seen with no box. Each shows at the watch's next read of the
// box's alerts, as on a box.
//
// The box's alerts body is set whole at each step with every time
// pinned: a body a test or this script sets has its "@-6m" times read
// from when it was set (Body), which would give every alert in it a new
// start, and the watch would take each for a new alert.

// Script is the demo's steps, each at its time from the start.
var Script = []Step{
	{At: 20 * time.Second, Fire: &Arrival{Name: "relaunch_refused", Severity: "critical", App: "blog", Summary: "blog: the watchdog may not relaunch it: its restart budget is spent"}},
	{At: 70 * time.Second, Fire: &Arrival{Name: "app_memory_high", Severity: "warning", App: "shop", Summary: "shop: memory over 90% of its limit for 10m"}},
	{At: 130 * time.Second, Resolve: "relaunch_refused"},
}

// Step is one step of the script: an alert that fires, or the name of
// one that resolves.
type Step struct {
	At      time.Duration
	Fire    *Arrival
	Resolve string
}

// Arrival is an alert the script fires, active from the step's time.
type Arrival struct {
	Name, Severity, App, Summary string
	start                        time.Time
}

// Arrivals plays Script on f until ctx ends, waiting with after
// (time.After).
func Arrivals(ctx context.Context, f *Fake, after func(time.Duration) <-chan time.Time) {
	prev := time.Duration(0)
	for i, st := range Script {
		select {
		case <-ctx.Done():
			return
		case <-after(st.At - prev):
		}
		prev = st.At
		f.Arrive(i)
	}
}

// Arrive plays step i of Script on f: the box's alerts are then the
// fixture's, their times pinned to the fake's first read, with the
// script's alerts firing at this step in front of them, newest first,
// each from the step it fired at.
func (f *Fake) Arrive(i int) {
	st := Script[i]
	f.mu.Lock()
	now := f.Now()
	if f.anchor.IsZero() {
		f.anchor = now
	}
	anchor := f.anchor
	if st.Fire != nil {
		a := *st.Fire
		a.start = now
		f.firing = append([]Arrival{a}, f.firing...)
	}
	var firing []Arrival
	for _, a := range f.firing {
		if a.Name != st.Resolve {
			firing = append(firing, a)
		}
	}
	f.firing = firing
	f.mu.Unlock()
	base, err := fs.ReadFile(fixtures, "fixtures/box-alerts.json")
	if err != nil {
		return
	}
	pinned := token.ReplaceAllStringFunc(string(base), func(m string) string {
		p := token.FindStringSubmatch(m)
		n, _ := strconv.Atoi(p[1])
		return `"` + anchor.Add(time.Duration(n)*units[p[2]]).UTC().Format(time.RFC3339) + `"`
	})
	var doc map[string]any
	if json.Unmarshal([]byte(pinned), &doc) != nil {
		return
	}
	alerts, _ := doc["alerts"].([]any)
	var fired []any
	for _, a := range firing {
		fired = append(fired, map[string]any{"name": a.Name, "severity": a.Severity, "app": a.App, "summary": a.Summary,
			"state": "active", "starts_at": a.start.UTC().Format(time.RFC3339)})
	}
	doc["alerts"] = append(fired, alerts...)
	b, err := json.Marshal(doc)
	if err != nil {
		return
	}
	f.Body("box-alerts.json", string(b))
}
