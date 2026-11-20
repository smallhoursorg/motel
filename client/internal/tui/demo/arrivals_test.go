package demo

import (
	"context"
	"testing"
	"time"

	"github.com/smallhoursorg/motel/liveswap/wire"
)

// TestArrivePinsTimes: each step of the script sets the box's alerts
// with every time pinned: the fixture's alerts keep the starts they had
// at the first read, and the script's keep the start of the step they
// fired at, step after step; the alert resolved is gone.
func TestArrivePinsTimes(t *testing.T) {
	base := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	now := base
	f := &Fake{Now: func() time.Time { return now }}
	read := func() wire.BoxAlerts {
		t.Helper()
		g, err := f.BoxAlerts(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return g.V
	}
	starts := func(b wire.BoxAlerts) map[string]time.Time {
		m := map[string]time.Time{}
		for _, a := range b.Alerts {
			m[a.Name] = a.StartsAt
		}
		return m
	}
	first := starts(read())
	fired := map[string]time.Time{}
	for i, st := range Script {
		now = base.Add(st.At)
		f.Arrive(i)
		if st.Fire != nil {
			fired[st.Fire.Name] = now
		}
		delete(fired, st.Resolve)
		now = now.Add(15 * time.Second)
		got := starts(read())
		for n, at := range first {
			if !got[n].Equal(at) {
				t.Errorf("step %d: %s starts %v, at the first read %v", i, n, got[n], at)
			}
		}
		for n, at := range fired {
			if !got[n].Equal(at) {
				t.Errorf("step %d: %s starts %v, want %v", i, n, got[n], at)
			}
		}
		if len(got) != len(first)+len(fired) {
			t.Errorf("step %d: %d alerts, want %d", i, len(got), len(first)+len(fired))
		}
	}
}

// TestArrivalsPlays: Arrivals plays every step, in order, at the times
// its clock gives, and stops when its context ends.
func TestArrivalsPlays(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	f := &Fake{Now: func() time.Time { return now }}
	var waits []time.Duration
	at := func(d time.Duration) <-chan time.Time {
		waits = append(waits, d)
		c := make(chan time.Time, 1)
		c <- now
		return c
	}
	Arrivals(context.Background(), f, at)
	if len(waits) != len(Script) || waits[0] != Script[0].At || waits[1] != Script[1].At-Script[0].At {
		t.Errorf("waited %v", waits)
	}
	if len(f.firing) != 1 || f.firing[0].Name != "app_memory_high" {
		t.Errorf("at the end, firing %v", f.firing)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	g := &Fake{Now: func() time.Time { return now }}
	Arrivals(ctx, g, func(time.Duration) <-chan time.Time { return nil })
	if len(g.firing) != 0 {
		t.Error("played after its context ended")
	}
}
