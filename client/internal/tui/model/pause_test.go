package model_test

import (
	"strconv"
	"testing"
	"time"

	"github.com/smallhoursorg/motel/client/internal/tui/model"
)

// unattended runs e for d from t0 with a key pressed at t0 alone, each
// read taking 50 ms and answered, the list answering apps. It returns
// every read, and the signatures the client's token rule would make: a
// token of 600 s with its iat 30 s back, to the second, signed again
// when less than a minute of it is left (sshkey.TimesLasting,
// PersonToken's minLeft).
func unattended(e *model.Engine, apps []string, d time.Duration) (calls []call, signs int) {
	now := t0
	e.Touch(now)
	var exp time.Time
	for now.Before(t0.Add(d)) {
		j, ok, wait := e.Take(now)
		if !ok {
			if wait <= 0 || wait > time.Second {
				wait = time.Second
			}
			now = now.Add(wait)
			continue
		}
		if exp.IsZero() || exp.Sub(now) < time.Minute {
			signs++
			exp = time.Unix(now.Add(-30*time.Second).Unix(), 0).Add(600 * time.Second)
		}
		calls = append(calls, call{j, now})
		now = now.Add(50 * time.Millisecond)
		if j.Kind == model.KApps {
			e.SetApps(apps, now)
		}
		e.Done(j, now, nil)
	}
	return calls, signs
}

// TestWatchThroughThePause: a viewer of the box who leaves the TUI for
// 8 hours, its key not a hardware one: after the ten idle minutes only
// the box's alerts are read, every minute, 470 reads in the 7 h 50 min
// after (#576's spike measured 480 at 60 s from the start), and the key
// signs about every 510 s of it, 54 or so in all; nothing else is read.
// With a hardware key nothing at all is read after the pause.
func TestWatchThroughThePause(t *testing.T) {
	for _, hw := range []bool{false, true} {
		e := model.NewEngine(t0)
		e.SetWatch(model.WatchBox, t0)
		e.SetHardwareKey(hw)
		calls, signs := unattended(e, names(5), 8*time.Hour)
		boxBucket(t, calls)
		after, other := 0, 0
		for _, c := range calls {
			if c.at.Sub(t0) < model.Idle {
				continue
			}
			if c.j.Kind == model.KBoxAlerts {
				after++
			} else {
				other++
			}
		}
		switch {
		case hw && after+other != 0:
			t.Errorf("a hardware key: %d reads after the pause", after+other)
		case !hw && (after < 465 || after > 475 || other != 0):
			t.Errorf("after the pause: %d alerts reads (want about 470), %d others", after, other)
		case !hw && (signs < 52 || signs > 58):
			t.Errorf("%d signatures in 8 hours, want about 54", signs)
		}
		t.Logf("hardware %v: %d reads in 8 h, %d of the alerts after the pause, %d signatures", hw, len(calls), after, signs)
	}
}

// TestAppWatchThroughThePause: a person with app grants only, left for
// 8 hours: after the pause each app's alerts alone are read, each at
// its interval at the keys and never under a minute, inside the
// engine's budget and the box's limit. The counts are logged for the
// owner's decision (READS.md's B” read every app at 30 s or more:
// 18,142 alerts reads at 40 apps).
func TestAppWatchThroughThePause(t *testing.T) {
	for _, n := range []int{1, 5, 12, 40} {
		e := model.NewEngine(t0)
		e.SetWatch(model.WatchApps, t0)
		calls, signs := unattended(e, names(n), 8*time.Hour)
		boxBucket(t, calls)
		last := map[string]time.Time{}
		after, other := 0, 0
		for _, c := range calls {
			if c.at.Sub(t0) < model.Idle+2*time.Minute {
				continue // the pause's first reads still at the keys' pace
			}
			if c.j.Kind != model.KAlerts {
				other++
				continue
			}
			after++
			if l, ok := last[c.j.App]; ok && c.at.Sub(l) < model.PausedWatchEvery-time.Second {
				t.Errorf("%d apps: %s's alerts read %s apart while paused", n, c.j.App, c.at.Sub(l))
			}
			last[c.j.App] = c.at
		}
		if other != 0 || len(last) != n {
			t.Errorf("%d apps: %d other reads while paused, %d apps' alerts read", n, other, len(last))
		}
		t.Logf("%s apps: %d alerts reads while paused (%.3f a second), %d signatures", strconv.Itoa(n), after, float64(after)/(8*time.Hour-model.Idle-2*time.Minute).Seconds(), signs)
	}
}

// TestWatchBackFromThePause: a key after the pause brings the box's
// alerts read back to every WatchEvery from its last answer, not at the
// paused minute's end.
func TestWatchBackFromThePause(t *testing.T) {
	e := model.NewEngine(t0)
	e.SetWatch(model.WatchBox, t0)
	end := t0.Add(model.Idle + 5*time.Minute)
	calls, _ := unattended(e, names(1), end.Sub(t0))
	var last time.Time
	for _, c := range calls {
		if c.j.Kind == model.KBoxAlerts {
			last = c.at
		}
	}
	now := end
	e.Touch(now)
	for range 200 {
		j, ok, wait := e.Take(now)
		if !ok {
			now = now.Add(max(wait, 100*time.Millisecond))
			continue
		}
		if j.Kind == model.KBoxAlerts {
			if due := maxT(end, last.Add(model.WatchEvery+time.Second)); now.After(due) {
				t.Errorf("read at +%s after the key, its last answer %s before the key", now.Sub(end), end.Sub(last))
			}
			return
		}
		e.Done(j, now, nil)
	}
	t.Error("the box's alerts were not read after the key")
}

// TestAppWatchBackFromThePause: a person with app grants only, paused on
// an app's screen: a key brings that app's alerts read back to every
// AlertsEvery from its last answer, not the paused minute's end.
func TestAppWatchBackFromThePause(t *testing.T) {
	e := model.NewEngine(t0)
	e.SetWatch(model.WatchApps, t0)
	e.Focus("app-0", t0)
	e.AlertsOpen("app-0", t0)
	open := model.Job{Kind: model.KAlerts, App: "app-0"}
	end := t0.Add(model.Idle + 5*time.Minute)
	calls, _ := unattended(e, names(3), end.Sub(t0))
	var last time.Time
	for _, c := range calls {
		if c.j == open {
			last = c.at
		}
	}
	now := end
	e.Touch(now)
	for range 200 {
		j, ok, wait := e.Take(now)
		if !ok {
			now = now.Add(max(wait, 100*time.Millisecond))
			continue
		}
		if j == open {
			if due := maxT(end, last.Add(model.AlertsEvery+time.Second)); now.After(due) {
				t.Errorf("read at +%s after the key, its last answer %s before the key", now.Sub(end), end.Sub(last))
			}
			return
		}
		e.Done(j, now, nil)
	}
	t.Error("the open app's alerts were not read after the key")
}
