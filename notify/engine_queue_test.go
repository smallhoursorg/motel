package notify

import (
	"context"
	"syscall"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// TestOneShotPastItsLifeIsGivenUp: a one-shot retried past its endsAt
// would be taken by Alertmanager as resolved and told to no one, though
// the POST succeeds. It is given up as a failed delivery instead, both
// at the head (the try at 4:50 fails, the next at 5:20 would succeed)
// and behind it (first tried when the head leaves, older still).
func TestOneShotPastItsLifeIsGivenUp(t *testing.T) {
	t.Run("retried at the head", func(t *testing.T) {
		te := newTestEngine(t)
		before := testutil.ToFloat64(te.failures.c)
		te.p.err = &netErr{syscall.ECONNREFUSED}
		te.report(deployFailed())
		te.attempt(context.Background())
		te.advance(4*time.Minute + 50*time.Second)
		if again, _ := te.attempt(context.Background()); again {
			t.Fatal("given up before five minutes")
		}
		te.advance(30 * time.Second)
		te.p.err = nil // Alertmanager answers again
		te.attempt(context.Background())
		if got := te.p.take(); len(got) != 0 {
			t.Fatalf("posted past its endsAt: %+v", got)
		}
		lines := te.lines("monitor: delivery failed")
		if len(lines) != 1 || lines[0].Level != zap.ErrorLevel || lines[0].ContextMap()["key"] != "liveswap/app:blog/deploy_failed" {
			t.Fatalf("delivery failed lines = %+v", lines)
		}
		if after := testutil.ToFloat64(te.failures.c); after != before+1 {
			t.Fatalf("counter %v → %v", before, after)
		}
		if len(te.queue) != 0 {
			t.Fatal("an expired post stayed queued")
		}
	})
	t.Run("queued behind the head", func(t *testing.T) {
		te := newTestEngine(t)
		te.report(deployFailed())
		te.advance(oneShotLife)
		if got := te.drain(); len(got) != 0 {
			t.Fatalf("posted past its endsAt: %+v", got)
		}
		if lines := te.lines("monitor: delivery failed"); len(lines) != 1 {
			t.Fatalf("delivery failed lines = %+v", lines)
		}
	})
	t.Run("the heartbeat, at debug", func(t *testing.T) {
		te := newTestEngine(t)
		te.tick()
		te.advance(heartbeatLife)
		if got := te.drain(); countAlert(got, alertWatchdog) != 0 {
			t.Fatalf("a dead heartbeat was posted: %+v", got)
		}
		if lines := te.lines("monitor: delivery failed"); len(lines) != 1 || lines[0].Level != zap.DebugLevel {
			t.Fatalf("delivery failed lines = %+v", lines)
		}
	})
}

// TestEndsArePostedLate: a resolve and a mute's end carry endsAt=now
// when they are queued, so their endsAt is past whenever they are
// posted; they are owed all the same, however late.
func TestEndsArePostedLate(t *testing.T) {
	te := newTestEngine(t)
	te.tick() // the heartbeat now, so that none is queued with the ends
	te.report(watchdog(true))
	te.mute("blog", true)
	te.drain()
	te.report(watchdog(false))
	te.advance(minHold)
	te.tick()
	te.mute("blog", false)
	te.advance(10 * time.Minute)
	got := te.drain()
	var resolved, unmuted bool
	for _, a := range got {
		switch a.Labels["alertname"] {
		case "watchdog_throttled":
			resolved = !a.EndsAt.IsZero() && a.EndsAt.Before(te.at)
		case alertDeployMute:
			unmuted = !a.EndsAt.IsZero() && a.EndsAt.Before(te.at)
		}
	}
	if !resolved || !unmuted {
		t.Fatalf("late ends: resolve posted %v, mute's end posted %v; posts %+v", resolved, unmuted, got)
	}
	if lines := te.lines("monitor: delivery failed"); len(lines) != 0 {
		t.Fatalf("an end was given up: %+v", lines)
	}
}

// TestReleaseGivesUpTheQueue: the last config running notify stops with
// a post still waiting for Alertmanager. It is given up then, a line and
// the counter, and is not posted when a later config brings the block
// back, whatever its age: a firing post would fire an alert no state
// backs.
func TestReleaseGivesUpTheQueue(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	e := newEngine()
	before := testutil.ToFloat64(e.failures.c)
	a := &App{}
	e.configure(a, &recPoster{err: &netErr{syscall.ECONNREFUSED}}, nil, zap.New(core))
	e.report(watchdog(true))
	e.release(a)
	e.mu.Lock()
	queued := len(e.queue)
	e.mu.Unlock()
	if queued != 0 {
		t.Fatalf("%d posts left queued after the last config stopped", queued)
	}
	var failed []observer.LoggedEntry
	for _, l := range logs.FilterMessage("monitor: delivery failed").All() {
		if l.Level == zap.ErrorLevel {
			failed = append(failed, l)
		}
	}
	if len(failed) != 1 || failed[0].ContextMap()["key"] != "liveswap/app:blog/watchdog_throttled" {
		t.Fatalf("delivery failed lines = %+v", failed)
	}
	if after := testutil.ToFloat64(e.failures.c); after < before+1 {
		t.Fatalf("counter %v → %v", before, after)
	}
	p := &recPoster{}
	b := &App{}
	e.configure(b, p, nil, zap.NewNop())
	e.release(b)
	if n := countAlert(p.take(), "watchdog_throttled"); n != 0 {
		t.Fatal("the block brought back posted what the stopped one had queued")
	}
}
