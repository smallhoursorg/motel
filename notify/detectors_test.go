package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/caddyserver/caddy/v2"

	"github.com/smallhoursorg/motel/backups/record"
	"github.com/smallhoursorg/motel/liveswap"
	"github.com/smallhoursorg/motel/liveswap/access/accesstest"
)

func TestLifecycleSinkMapsLiveswapsEvents(t *testing.T) {
	te := newTestEngine(t)
	s := lifecycleSink{e: te.engine}
	s.Lifecycle(liveswap.LifecycleEvent{Name: liveswap.EventDeployStarted, App: "blog", Release: "v2"})
	s.Lifecycle(liveswap.LifecycleEvent{Name: liveswap.EventWatchdogThrottled, App: "blog", Release: "v1", Summary: "restart loop"})
	s.Lifecycle(liveswap.LifecycleEvent{Name: liveswap.EventDeployFailed, App: "blog", Release: "v2", DeployedBy: "local:/k", Summary: "deploy of v2 failed"})
	s.Lifecycle(liveswap.LifecycleEvent{Name: liveswap.EventDeployEnded, App: "blog", Release: "v2"})
	got := te.drain()
	byName := map[string]amAlert{}
	var order []string
	for _, a := range got {
		order = append(order, a.Labels["alertname"])
		byName[a.Labels["alertname"]] = a
	}
	want := []string{"deploy_in_progress", "watchdog_throttled", "deploy_failed", "deploy_in_progress"}
	if len(order) != len(want) {
		t.Fatalf("posts = %v", order)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("posts = %v, want %v", order, want)
		}
	}
	if w := byName["watchdog_throttled"]; w.Labels["kind"] != "state" || w.Labels["severity"] != "critical" || w.Labels["app"] != "blog" {
		t.Errorf("watchdog = %v", w.Labels)
	}
	if f := byName["deploy_failed"]; f.Labels["kind"] != "event" || f.Labels["severity"] != "warning" || f.Labels["deployed_by"] != "local:/k" {
		t.Errorf("deploy_failed = %v", f.Labels)
	}
	// The recovery carries the watchdog's hold to the engine.
	s.Lifecycle(liveswap.LifecycleEvent{Name: liveswap.EventWatchdogRecovered, App: "blog", Hold: 2 * time.Minute})
	if st := te.states["liveswap/app:blog/watchdog_throttled"]; st == nil || st.firing || st.hold != 2*time.Minute {
		t.Fatalf("state = %+v", st)
	}
}

// TestPeopleSinkRaisesBoxEvents: liveswap's two announcements about a
// person's key are one-shot box events at warning, from source access,
// keyed by the key's fingerprint, with the summary liveswap wrote, the
// name and the fingerprint's start in it as written (a long name of hex
// would be masked but for Announcement.Safe); a thief cycling networks is
// held to the limit per key (four, then a journal line each), and
// another key is not held by it.
func TestPeopleSinkRaisesBoxEvents(t *testing.T) {
	te := newTestEngine(t)
	s := peopleSink{e: te.engine}
	alice, bob := accesstest.Ed25519().Fingerprint(), accesstest.Ed25519().Fingerprint()
	first := liveswap.Announcement{Name: liveswap.EventPersonNewNetwork, Person: "alice@example.com", Key: alice, From: "203.0.113.0/24", First: true}
	s.Announce(first)
	got := te.drain()
	if len(got) != 1 {
		t.Fatalf("posts %v", got)
	}
	want := map[string]string{"alertname": "person_new_network", "kind": "event", "severity": "warning", "subject": "box", "source": "access",
		"key": "access/box/person_new_network/" + alice, "instance": alice}
	for k, v := range want {
		if got[0].Labels[k] != v {
			t.Errorf("label %s = %q, want %q", k, got[0].Labels[k], v)
		}
	}
	if _, ok := got[0].Labels["app"]; ok {
		t.Errorf("a box event with an app label: %v", got[0].Labels)
	}
	if sum := got[0].Annotations["summary"]; sum != first.Summary() || !strings.Contains(sum, alice[:19]) {
		t.Errorf("summary %q, want %q", sum, first.Summary())
	}
	// A long name that looks generated, cut in the summary: the cut is
	// what the filter is told to leave.
	long := liveswap.Announcement{Name: liveswap.EventPersonNewNetwork, Person: "svc-" + strings.Repeat("7f3e9d2a", 10), Key: bob, From: "203.0.113.0/24"}
	s.Announce(long)
	if got := te.drain(); len(got) != 1 || got[0].Annotations["summary"] != long.Summary() {
		t.Errorf("a long name: %v, want %q", got, long.Summary())
	}
	for i := range 6 {
		s.Announce(liveswap.Announcement{Name: liveswap.EventPersonNewNetwork, Person: "alice@example.com", Key: alice, From: fmt.Sprintf("198.51.%d.0/24", i)})
	}
	s.Announce(liveswap.Announcement{Name: liveswap.EventPersonOutsideList, Person: "bob@example.com", Key: bob, From: "203.0.113.9"})
	got = te.drain()
	if len(got) != 4 || got[3].Labels["alertname"] != "person_outside_list" || got[3].Labels["key"] != "access/box/person_outside_list/"+bob {
		t.Fatalf("posts %v", got)
	}
	if l := te.lines("monitor: event"); len(l) != 9 || l[4].ContextMap()["suppressed"] != nil || l[5].ContextMap()["suppressed"] == nil || l[8].ContextMap()["suppressed"] != nil {
		t.Errorf("journal %v", l)
	}
	s.Announce(liveswap.Announcement{Name: "something_else", Person: "x", Key: alice})
	if got := te.drain(); len(got) != 0 {
		t.Errorf("an unknown announcement posted %v", got)
	}
}

// TestPeopleSinkBeforeNotifyRuns: an announcement made while no config
// runs notify (a start's first seconds) is kept, and told when one does;
// another event then is not, as before.
func TestPeopleSinkBeforeNotifyRuns(t *testing.T) {
	te := newTestEngine(t)
	te.active = nil
	s := peopleSink{e: te.engine}
	fp := accesstest.Ed25519().Fingerprint()
	for i := range earlyMax + 3 {
		s.Announce(liveswap.Announcement{Name: liveswap.EventPersonNewNetwork, Person: "alice@example.com", Key: fmt.Sprint(fp, i), From: "203.0.113.0/24", First: true})
	}
	te.report(Event{Name: "deploy_failed", Kind: kindEvent, App: "blog", Source: "liveswap", Summary: "x"})
	if got := te.drain(); len(got) != 0 {
		t.Fatalf("posted while no config runs notify: %v", got)
	}
	te.point(&App{}, te.p, te.known, te.logger)
	got := te.drain()
	if len(got) != earlyMax || got[0].Labels["alertname"] != "person_new_network" || got[0].Labels["instance"] != fmt.Sprint(fp, 0) {
		t.Fatalf("told %d once a config runs it: %v", len(got), got[:min(len(got), 1)])
	}
	te.point(&App{}, te.p, te.known, te.logger)
	if got := te.drain(); len(got) != 0 {
		t.Errorf("told again: %v", got)
	}
	// One key cycling networks keeps a bucket's worth, and leaves room
	// for another key's first use.
	te.active = nil
	for i := range earlyMax {
		s.Announce(liveswap.Announcement{Name: liveswap.EventPersonNewNetwork, Person: "mallory@example.com", Key: fp + "cycling", From: fmt.Sprintf("198.51.%d.0/24", i)})
	}
	s.Announce(liveswap.Announcement{Name: liveswap.EventPersonNewNetwork, Person: "bob@example.com", Key: fp + "bob", From: "203.0.113.0/24", First: true})
	te.point(&App{}, te.p, te.known, te.logger)
	if got := te.drain(); len(got) != bucketSize+1 || got[bucketSize].Labels["instance"] != fp+"bob" {
		t.Errorf("told %d, want %d of the cycling key and bob's: %v", len(got), bucketSize, got)
	}
	// Kept past earlyFor (a box with no monitor block for a while), it is
	// not told late as if it were now; and a full buffer of such old ones
	// leaves room for a fresh one, which is told.
	te.active = nil
	for i := range earlyMax {
		s.Announce(liveswap.Announcement{Name: liveswap.EventPersonNewNetwork, Person: "alice@example.com", Key: fmt.Sprint(fp, "old", i), From: "198.51.100.0/24"})
	}
	te.advance(earlyFor + time.Second)
	s.Announce(liveswap.Announcement{Name: liveswap.EventPersonNewNetwork, Person: "alice@example.com", Key: fp + "fresh", From: "198.51.100.0/24"})
	te.point(&App{}, te.p, te.known, te.logger)
	if got := te.drain(); len(got) != 1 || got[0].Labels["instance"] != fp+"fresh" {
		t.Errorf("told %d after %s, want the fresh one alone: %v", len(got), earlyFor, got)
	}
}

func certEvent(t *testing.T, name string, data map[string]any) caddy.Event {
	t.Helper()
	e, err := caddy.NewEvent(caddy.Context{Context: context.Background()}, name, data)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestTLSHandler(t *testing.T) {
	te := newTestEngine(t)
	h := tlsHandler{te.engine}
	cases := []struct {
		data map[string]any
		sev  string
	}{
		{map[string]any{"identifier": "a.example.org", "renewal": true, "remaining": 3 * 24 * time.Hour}, SeverityCritical},
		{map[string]any{"identifier": "b.example.org", "renewal": true, "remaining": 20 * 24 * time.Hour}, SeverityWarning},
		{map[string]any{"identifier": "c.example.org", "renewal": false}, SeverityWarning},
	}
	for _, c := range cases {
		if err := h.Handle(context.Background(), certEvent(t, "cert_failed", c.data)); err != nil {
			t.Fatal(err)
		}
	}
	got := te.drain()
	if len(got) != 3 {
		t.Fatalf("posts = %+v", got)
	}
	for i, c := range cases {
		l := got[i].Labels
		if l["severity"] != c.sev || l["instance"] != c.data["identifier"] || l["subject"] != "box" || l["alertname"] != "cert_failed" {
			t.Errorf("case %d labels = %v", i, l)
		}
		if _, ok := l["app"]; ok {
			t.Errorf("a certificate event carries an app label: %v", l)
		}
	}
	if err := h.Handle(context.Background(), certEvent(t, "cert_obtained", map[string]any{"identifier": "a.example.org"})); err != nil {
		t.Fatal(err)
	}
	if st := te.states["tls/box/cert_failed/a.example.org"]; st == nil || st.firing {
		t.Fatal("cert_obtained did not clear its identifier")
	}
}

func writeRecord(t *testing.T, path string, st *record.Status) {
	t.Helper()
	b, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func names(evs []Event) map[string]bool {
	out := map[string]bool{}
	for _, e := range evs {
		k := e.Name
		if e.Instance != "" {
			k += "/" + e.Instance
		}
		if !e.Firing {
			k = "-" + k
		}
		out[k] = true
	}
	return out
}

func TestBackupPoller(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "status.json")
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	var b backupPoller
	if evs := b.poll(now, path); len(evs) != 0 {
		t.Fatalf("no record (backups not set up): %+v", evs)
	}
	old := &record.Snapshot{ID: "a", Time: now.Add(-4 * time.Hour)}
	fresh := &record.Snapshot{ID: "b", Time: now.Add(-time.Hour)}
	writeRecord(t, path, &record.Status{Apps: map[string]*record.App{
		"blog": {Class: record.OK, LastOK: old},
		"shop": {Class: record.Failed, LastOK: fresh},
	}, LastDrill: &record.Drill{Time: now, Detail: "could not begin"}})
	if evs := b.poll(now.Add(time.Minute), path); evs != nil {
		t.Fatal("polled inside five minutes")
	}
	now = now.Add(5 * time.Minute)
	evs := b.poll(now, path)
	got := names(evs)
	for _, want := range []string{"backup_stale/blog", "backup_run_failed", "drill_failed"} {
		if !got[want] {
			t.Errorf("missing %s in %v", want, got)
		}
	}
	for _, e := range evs {
		if e.App != "" || e.subject() != "box" {
			t.Errorf("a backup event is the box's: %+v", e)
		}
		if e.Name == "backup_stale" && e.Severity != SeverityCritical {
			t.Errorf("stale is critical: %+v", e)
		}
	}
	// Everything put right: each clears.
	writeRecord(t, path, &record.Status{Apps: map[string]*record.App{
		"blog": {Class: record.OK, LastOK: &record.Snapshot{ID: "c", Time: now}},
		"shop": {Class: record.OK, LastOK: &record.Snapshot{ID: "d", Time: now}},
	}})
	now = now.Add(5 * time.Minute)
	got = names(b.poll(now, path))
	for _, want := range []string{"-backup_stale/blog", "-backup_run_failed", "-drill_failed"} {
		if !got[want] {
			t.Errorf("missing %s in %v", want, got)
		}
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	now = now.Add(5 * time.Minute)
	if got := names(b.poll(now, path)); !got["backup_unreadable"] {
		t.Errorf("an unreadable record: %v", got)
	}
}

// TestBackupPollerFollowsMotelBackupsVerdicts: a run is failed unless
// every app is ok or pending (motel-backup run's own exit), and a drill
// that did not prove an app is drill_failed, as status reads both.
func TestBackupPollerFollowsMotelBackupsVerdicts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "status.json")
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	fresh := &record.Snapshot{ID: "a", Time: now.Add(-time.Hour)}
	for _, class := range []record.Class{record.Incomplete, record.DataMissing, record.NotAttempted, record.Failed} {
		var b backupPoller
		writeRecord(t, path, &record.Status{Apps: map[string]*record.App{"blog": {Class: class, LastOK: fresh}}})
		if got := names(b.poll(now, path)); !got["backup_run_failed"] {
			t.Errorf("class %q: %v", class, got)
		}
	}
	for _, class := range []record.Class{record.OK, record.Pending} {
		var b backupPoller
		writeRecord(t, path, &record.Status{Apps: map[string]*record.App{"blog": {Class: class, LastOK: fresh}}})
		if got := names(b.poll(now, path)); got["backup_run_failed"] {
			t.Errorf("class %q is no failure: %v", class, got)
		}
	}
	var b backupPoller
	writeRecord(t, path, &record.Status{Apps: map[string]*record.App{
		"blog": {Class: record.OK, LastOK: fresh, RestoreDrill: &record.Drill{Time: now, Detail: "the restored database fails its integrity check"}},
		"shop": {Class: record.OK, LastOK: fresh, RestoreProven: &record.Drill{Time: now}},
	}})
	evs := b.poll(now, path)
	if got := names(evs); !got["drill_failed"] {
		t.Fatalf("a per-app drill that proved nothing: %v", got)
	}
	for _, e := range evs {
		if e.Name == "drill_failed" && (!strings.Contains(e.Summary, "blog") || strings.Contains(e.Summary, "shop") || strings.Contains(e.Summary, "integrity")) {
			t.Fatalf("summary = %q", e.Summary)
		}
	}
}

// TestBackupPollerUnreadableKeepsWhatFired: a record that cannot be read
// says nothing was put right.
func TestBackupPollerUnreadableKeepsWhatFired(t *testing.T) {
	path := filepath.Join(t.TempDir(), "status.json")
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	writeRecord(t, path, &record.Status{Apps: map[string]*record.App{}, LastDrill: &record.Drill{Time: now, Detail: "could not begin"}})
	var b backupPoller
	if got := names(b.poll(now, path)); !got["drill_failed"] {
		t.Fatalf("first poll: %v", got)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := names(b.poll(now.Add(5*time.Minute), path))
	if got["-drill_failed"] || !got["drill_failed"] || !got["backup_unreadable"] {
		t.Fatalf("unreadable: %v", got)
	}
}

func TestDiskLevel(t *testing.T) {
	cases := []struct {
		prev string
		used float64
		want string
	}{
		{"", 50, ""}, {"", 79.9, ""}, {"", 80, SeverityWarning}, {"", 90, SeverityCritical},
		{SeverityWarning, 76, SeverityWarning}, {SeverityWarning, 74.9, ""},
		{SeverityCritical, 86, SeverityCritical}, {SeverityCritical, 84, SeverityWarning},
		{SeverityCritical, 70, ""},
	}
	for _, c := range cases {
		if got := diskLevel(c.prev, c.used); got != c.want {
			t.Errorf("diskLevel(%q, %v) = %q, want %q", c.prev, c.used, got, c.want)
		}
	}
}

func TestDiskPollerOncePerFilesystem(t *testing.T) {
	used := map[string]float64{"/var/lib/liveswap": 85, "/var/lib/motel": 85, "/var/lib/motel-notify": 40}
	dev := map[string]uint64{"/var/lib/liveswap": 1, "/var/lib/motel": 1, "/var/lib/motel-notify": 2}
	d := diskPoller{stat: func(p string) (diskNumbers, error) { return diskNumbers{usedPct: used[p], dev: dev[p]}, nil }}
	paths := []string{"/var/lib/liveswap", "/var/lib/motel", "/var/lib/motel-notify"}
	evs := d.poll(paths)
	if len(evs) != 1 || evs[0].Instance != "/var/lib/liveswap" || evs[0].Severity != SeverityWarning || !evs[0].Firing {
		t.Fatalf("events = %+v", evs)
	}
	used["/var/lib/liveswap"] = 60
	evs = d.poll(paths)
	if len(evs) != 1 || evs[0].Firing {
		t.Fatalf("after the clean-up: %+v", evs)
	}
}

// TestSeverityChangeInsideTheHold: a key cleared and fired again at
// another severity before its hold ends is the same case.
func TestSeverityChangeInsideTheHold(t *testing.T) {
	te := newTestEngine(t)
	ev := Event{Name: alertDiskLow, Kind: kindState, Severity: SeverityWarning, Source: "disk", Instance: "/", Summary: "85%", Firing: true}
	te.report(ev)
	te.drain()
	ev.Firing = false
	te.report(ev)
	te.advance(10 * time.Second)
	ev.Firing, ev.Severity = true, SeverityCritical
	te.report(ev)
	got := te.drain()
	if len(got) != 2 || got[0].Labels["severity"] != "warning" || got[0].EndsAt.IsZero() || got[1].Labels["severity"] != "critical" {
		t.Fatalf("posts = %+v", got)
	}
	te.advance(time.Minute)
	te.tick()
	for _, a := range te.drain() {
		if a.Labels["alertname"] == alertDiskLow && !a.EndsAt.IsZero() {
			t.Fatalf("a resolve after the re-fire: %+v", a)
		}
	}
}

// TestPruneEndsWhatNoSourceClears: an app taken out of the config, or
// the backup record turned off, leaves no key re-posted for ever.
func TestPruneEndsWhatNoSourceClears(t *testing.T) {
	te := newTestEngine(t)
	te.report(watchdog(true))
	te.report(Event{Name: "drill_failed", Kind: kindState, Severity: SeverityCritical, Source: "backup", Summary: "x", Firing: true})
	te.report(Event{Name: alertDiskLow, Kind: kindState, Severity: SeverityWarning, Source: "disk", Instance: "/var/lib/liveswap", Summary: "x", Firing: true})
	te.mute("blog", true)
	te.drain()
	a := mustParse(t, "monitor {\nchannel ops slack {secret.NOTIFY_OPS_URL}\noperator ops\nbackup_record off\n}")
	r, err := a.resolve(map[string][]TrustSource{"shop": nil}, testSecrets())
	if err != nil {
		t.Fatal(err)
	}
	a.cfg, a.diskPaths = r, []string{"/var/lib/motel"}
	te.setConfig(a, te.p, te.known, te.logger)
	got := te.drain()
	ended := map[string]bool{}
	for _, x := range got {
		if !x.EndsAt.IsZero() && !x.EndsAt.After(te.at) {
			ended[x.Labels["alertname"]] = true
		}
	}
	for _, n := range []string{"watchdog_throttled", "drill_failed", alertDiskLow, alertDeployMute} {
		if !ended[n] {
			t.Errorf("%s not ended: %+v", n, got)
		}
	}
	if len(te.states) != 0 || len(te.mutes) != 0 {
		t.Fatalf("left: %v %v", te.states, te.mutes)
	}
}

func TestSummaryKeepsAShaRelease(t *testing.T) {
	te := newTestEngine(t)
	sha := "3f2a9c1e8b7d6a5f4e3d2c1b0a9f8e7d6c5b4a39"
	te.report(Event{Name: "deploy_failed", Kind: kindEvent, App: "blog", Source: "liveswap", Release: sha, Summary: "deploy of " + sha + " failed"})
	got := te.drain()
	if len(got) != 1 || !strings.Contains(got[0].Annotations["summary"], sha) {
		t.Fatalf("summary = %q", got[0].Annotations["summary"])
	}
}

func TestSeverityChangeEndsTheOldAlert(t *testing.T) {
	te := newTestEngine(t)
	ev := Event{Name: alertDiskLow, Kind: kindState, Severity: SeverityWarning, Source: "disk", Instance: "/", Summary: "82%", Firing: true}
	te.report(ev)
	te.drain()
	ev.Severity, ev.Summary = SeverityCritical, "91%"
	te.report(ev)
	got := te.drain()
	if len(got) != 2 || got[0].Labels["severity"] != "warning" || got[0].EndsAt.IsZero() ||
		got[1].Labels["severity"] != "critical" || !got[1].EndsAt.IsZero() {
		t.Fatalf("posts = %+v", got)
	}
}

func TestDiskPollerKeepsAPathItCannotMeasure(t *testing.T) {
	fail := false
	d := diskPoller{stat: func(string) (diskNumbers, error) {
		if fail {
			return diskNumbers{}, os.ErrPermission
		}
		return diskNumbers{usedPct: 92, dev: 1}, nil
	}}
	if evs := d.poll([]string{"/var/lib/liveswap"}); len(evs) != 1 || !evs[0].Firing {
		t.Fatalf("first: %+v", evs)
	}
	fail = true
	evs := d.poll([]string{"/var/lib/liveswap"})
	if len(evs) != 1 || !evs[0].Firing || evs[0].Severity != SeverityCritical {
		t.Fatalf("a failed statfs cleared the alert: %+v", evs)
	}
}

// A statfs that hangs (a network mount whose server went away) holds
// back no poll, which runs on the engine's goroutine: the path is not
// measured, so one that fired keeps firing and one that did not fires
// nothing, the other paths are, and the polls meanwhile start no other
// statfs of it (Copilot round 3 on #429).
func TestDiskPollerStatThatHangs(t *testing.T) {
	defer func(w time.Duration) { diskStatWait = w }(diskStatWait)
	diskStatWait = 50 * time.Millisecond
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	var hang atomic.Bool
	var calls atomic.Int32
	used := map[string]float64{"/srv/hang-fired": 92, "/srv/hang-quiet": 40, "/var/lib/hang-other": 85}
	dev := map[string]uint64{"/srv/hang-fired": 1, "/srv/hang-quiet": 2, "/var/lib/hang-other": 3}
	d := diskPoller{stat: func(p string) (diskNumbers, error) {
		if hang.Load() && p != "/var/lib/hang-other" {
			calls.Add(1)
			<-release
		}
		return diskNumbers{usedPct: used[p], dev: dev[p]}, nil
	}}
	paths := []string{"/srv/hang-fired", "/srv/hang-quiet", "/var/lib/hang-other"}
	poll := func() []Event {
		t.Helper()
		got := make(chan []Event, 1)
		go func() { got <- d.poll(paths) }()
		select {
		case evs := <-got:
			return evs
		case <-time.After(5 * time.Second):
			t.Fatal("a poll waited on a statfs that hangs")
			return nil
		}
	}
	// Each event as instance=severity, or instance=resolved.
	state := func(evs []Event) string {
		var out []string
		for _, e := range evs {
			v := e.Severity
			if !e.Firing {
				v = "resolved"
			}
			out = append(out, e.Instance+"="+v)
		}
		slices.Sort(out)
		return strings.Join(out, " ")
	}
	firing := "/srv/hang-fired=critical /var/lib/hang-other=warning"
	if got := state(poll()); got != firing {
		t.Fatalf("first: %s", got)
	}
	// The fired path's filesystem cleans up and the quiet one fills,
	// while both hang: neither is measured, so what fires is as it was.
	hang.Store(true)
	used["/srv/hang-fired"], used["/srv/hang-quiet"] = 10, 95
	for range 3 {
		if got := state(poll()); got != firing {
			t.Fatalf("a poll over a hung statfs changed what fires: %s", got)
		}
	}
	if n := calls.Load(); n != 2 {
		t.Fatalf("%d statfs of the hung paths, want one each", n)
	}
	used["/var/lib/hang-other"] = 50
	if got := state(poll()); got != "/srv/hang-fired=critical /var/lib/hang-other=resolved" {
		t.Fatalf("the path that answers is measured: %s", got)
	}
	hang.Store(false)
	close(release)
	// The two released statfs come back each in its own time, and a poll
	// between them would see one path measured and the other not: wait
	// until neither is out, then one poll measures both.
	deadline := time.Now().Add(5 * time.Second)
	for {
		diskPollStats.mu.Lock()
		out := diskPollStats.busy["/srv/hang-fired"] || diskPollStats.busy["/srv/hang-quiet"]
		diskPollStats.mu.Unlock()
		if !out {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the released statfs did not come back")
		}
		time.Sleep(time.Millisecond)
	}
	if got := state(poll()); got != "/srv/hang-fired=resolved /srv/hang-quiet=critical" {
		t.Fatalf("after it came back: %s", got)
	}
}

// TestTLSKeyUnreportedIsCleared: CertMagic reports cert_failed on every
// failed attempt for a name it manages, at least every six hours; a
// name taken out of the config is never reported again, and its key is
// cleared once tlsStale has passed.
func TestTLSKeyUnreportedIsCleared(t *testing.T) {
	te := newTestEngine(t)
	h := tlsHandler{te.engine}
	fail := func() {
		if err := h.Handle(context.Background(), certEvent(t, "cert_failed", map[string]any{"identifier": "gone.example.org", "renewal": false})); err != nil {
			t.Fatal(err)
		}
	}
	fail()
	te.drain()
	key := "tls/box/cert_failed/gone.example.org"
	// Retried and failed again six hours on: still firing past 13 h
	// from the first report.
	te.advance(6 * time.Hour)
	fail()
	te.advance(7 * time.Hour)
	te.tick()
	if st := te.states[key]; st == nil || !st.firing {
		t.Fatal("cleared while CertMagic was still reporting it")
	}
	// No report for 13 h: taken as cleared, then resolved after the hold.
	te.advance(6 * time.Hour)
	te.tick()
	te.advance(31 * time.Second)
	te.tick()
	resolved := false
	for _, a := range te.drain() {
		if a.Labels["alertname"] == "cert_failed" && !a.EndsAt.IsZero() && !a.EndsAt.After(te.at) {
			resolved = true
		}
	}
	if !resolved || te.states[key] != nil {
		t.Fatalf("not resolved after %v unreported (state %+v)", tlsStale, te.states[key])
	}
}

// TestDiskLevelHeldByDevice: the path that stands for a filesystem can
// change with the config (liveswap added or removed); its hysteresis is
// the filesystem's, so a held critical stays critical above 85 %.
func TestDiskLevelHeldByDevice(t *testing.T) {
	used := map[string]float64{"/var/lib/liveswap": 92, "/var/lib/motel": 92}
	d := diskPoller{stat: func(p string) (diskNumbers, error) { return diskNumbers{usedPct: used[p], dev: 7}, nil }}
	if evs := d.poll([]string{"/var/lib/liveswap", "/var/lib/motel"}); len(evs) != 1 || evs[0].Severity != SeverityCritical {
		t.Fatalf("first: %+v", evs)
	}
	used["/var/lib/motel"] = 86
	for _, e := range d.poll([]string{"/var/lib/motel"}) {
		if e.Firing && e.Severity != SeverityCritical {
			t.Fatalf("a held critical became %s when its path changed: %+v", e.Severity, e)
		}
	}
}

// unhealthyRig is a backup poller with a credential file set up
// setUpAgo before now, and a record beside it.
type unhealthyRig struct {
	b    backupPoller
	path string
	now  time.Time
}

func newUnhealthyRig(t *testing.T, setUpAgo time.Duration) *unhealthyRig {
	t.Helper()
	dir := t.TempDir()
	r := &unhealthyRig{path: filepath.Join(dir, "status.json"), now: time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)}
	r.b.envFile = filepath.Join(dir, "repository.env")
	if setUpAgo > 0 {
		if err := os.WriteFile(r.b.envFile, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		at := r.now.Add(-setUpAgo)
		if err := os.Chtimes(r.b.envFile, at, at); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

// poll writes st (nil: no record) and polls five minutes on.
func (r *unhealthyRig) poll(t *testing.T, st *record.Status) []Event {
	t.Helper()
	if st == nil {
		_ = os.Remove(r.path)
	} else {
		writeRecord(t, r.path, st)
	}
	r.now = r.now.Add(backupEvery)
	return r.b.poll(r.now, r.path)
}

// soundRecord is a box status calls healthy at now: a run ten minutes
// ago that backed blog up, a listing since, a restore proven two days
// ago and a clean check of the repository three days ago.
func soundRecord(now time.Time) *record.Status {
	at := func(d time.Duration) *time.Time { t := now.Add(-d); return &t }
	s := &record.Snapshot{ID: "aaaaaaaa1111", Time: now.Add(-10 * time.Minute), Seen: at(9 * time.Minute)}
	return &record.Status{
		Started: now.Add(-11 * time.Minute), Finished: now.Add(-9 * time.Minute), Listed: at(9 * time.Minute),
		Apps: map[string]*record.App{"blog": {Class: record.OK, Looked: "/var/lib/liveswap/blog/shared", Snapshot: s, LastOK: s, LastSnapshot: s,
			RestoreProven: &record.Drill{Snapshot: *s, Time: now.Add(-48 * time.Hour)}}},
		LastDrill:  &record.Drill{Time: now.Add(-72 * time.Hour)},
		LastCheck:  &record.Check{Time: now.Add(-72 * time.Hour), Group: "38/52", Class: record.CheckClean},
		CheckSince: at(30 * 24 * time.Hour),
	}
}

func summaryOf(evs []Event, name string) (string, bool) {
	for _, e := range evs {
		if e.Name == name && e.Firing {
			return e.Summary, true
		}
	}
	return "", false
}

// TestBackupUnhealthyTheSilentCases: each case #590 found status calling
// unhealthy with no alert raises backup_unhealthy, alone, critical, with
// status's first problem line after the pointer.
func TestBackupUnhealthyTheSilentCases(t *testing.T) {
	const day = 24 * time.Hour
	cases := []struct {
		name   string
		setUp  time.Duration
		change func(st *record.Status, now time.Time)
		says   string
	}{
		{"A: a check that never came to a verdict", 60 * day, func(st *record.Status, now time.Time) {
			since := now.Add(-9 * day)
			st.LastCheck, st.CheckSince = nil, &since
		}, "no check of the repository has come to a verdict since"},
		{"A: a clean check that is old", 60 * day, func(st *record.Status, now time.Time) {
			st.LastCheck.Time = now.Add(-9 * day)
		}, ": clean: old, more than 8 days ago"},
		{"A: a check that ended unreachable", 60 * day, func(st *record.Status, _ time.Time) {
			st.LastCheck.Class, st.LastCheck.Detail = record.CheckUnreachable, "the storage did not answer"
		}, "(data group 38/52): unreachable: the storage did not answer"},
		{"A: no check since setup", 60 * day, func(st *record.Status, _ time.Time) {
			st.LastCheck, st.CheckSince, st.LastDrill = nil, nil, nil
		}, "the repository has not been checked since backups were set up"},
		{"B: a proven restore that is old", 60 * day, func(st *record.Status, now time.Time) {
			st.Apps["blog"].RestoreProven.Time = now.Add(-9 * day)
		}, "blog: restore last proven: "},
		{"C: a repository not listed for over three hours", 60 * day, func(st *record.Status, now time.Time) {
			u := now.Add(-4 * time.Hour)
			st.Unlisted = &u
		}, "the repository has not been listed since"},
		{"E: no run finished in three hours, every app pending", 60 * day, func(st *record.Status, now time.Time) {
			st.Started, st.Finished = now.Add(-5*time.Hour), now.Add(-4*time.Hour)
			st.Apps = map[string]*record.App{"blog": {Class: record.Pending, Looked: "/var/lib/liveswap/blog/shared"}}
		}, "no run has finished since"},
		{"E: no run finished in three hours, no app declared", 60 * day, func(st *record.Status, now time.Time) {
			st.Started, st.Finished = now.Add(-5*time.Hour), now.Add(-4*time.Hour)
			st.Apps = map[string]*record.App{}
		}, "no run has finished since"},
		{"an app with no result on the record", 60 * day, func(st *record.Status, _ time.Time) {
			st.Apps["shop"] = nil
		}, "shop: no result on record"},
		{"an app past pending with no complete backup", 60 * day, func(st *record.Status, _ time.Time) {
			a := st.Apps["blog"]
			a.LastOK = nil // its only snapshot was made by a restore
		}, "blog: no complete backup yet"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newUnhealthyRig(t, c.setUp)
			st := soundRecord(r.now.Add(backupEvery))
			c.change(st, r.now.Add(backupEvery))
			evs := r.poll(t, st)
			if len(evs) != 1 || evs[0].Name != "backup_unhealthy" || !evs[0].Firing || evs[0].Severity != SeverityCritical || evs[0].Instance != "" || evs[0].subject() != "box" {
				t.Fatalf("want backup_unhealthy alone, critical, the box's: %+v", evs)
			}
			if !strings.HasPrefix(evs[0].Summary, "backups unhealthy; motel backup status says what (on the box: motel-backup status), first: ") || !strings.Contains(evs[0].Summary, c.says) {
				t.Fatalf("summary = %q, want it to say %q", evs[0].Summary, c.says)
			}
		})
	}
}

// TestBackupUnhealthyNoRunSinceSetUp is case D: a credential file set up
// more than three hours ago and no record. Inside the three hours, and
// with no credential file, nothing.
func TestBackupUnhealthyNoRunSinceSetUp(t *testing.T) {
	r := newUnhealthyRig(t, 4*time.Hour)
	s, ok := summaryOf(r.poll(t, nil), "backup_unhealthy")
	if !ok || !strings.Contains(s, "no run yet, and backups were set up") {
		t.Fatalf("set up four hours ago, no run: %q", s)
	}
	r = newUnhealthyRig(t, 2*time.Hour)
	if evs := r.poll(t, nil); len(evs) != 0 {
		t.Fatalf("set up two hours ago: %+v", evs)
	}
	r = newUnhealthyRig(t, 0)
	if evs := r.poll(t, nil); len(evs) != 0 {
		t.Fatalf("not set up: %+v", evs)
	}
}

// TestBackupUnhealthyQuiet: no alert for a healthy box, a box with no
// backups set up (whatever a record left behind says: status says "not
// set up", and judges nothing), a new app pending its first data, or one
// whose first good backup the run drilled there and then.
func TestBackupUnhealthyQuiet(t *testing.T) {
	r := newUnhealthyRig(t, 60*24*time.Hour)
	if evs := r.poll(t, soundRecord(r.now.Add(backupEvery))); len(evs) != 0 {
		t.Fatalf("a healthy box: %+v", evs)
	}
	st := soundRecord(r.now.Add(backupEvery))
	st.Apps["shop"] = &record.App{Class: record.Pending, Looked: "/var/lib/liveswap/shop/shared"}
	if evs := r.poll(t, st); len(evs) != 0 {
		t.Fatalf("a new app pending its first data: %+v", evs)
	}
	st = soundRecord(r.now.Add(backupEvery))
	first := *st.Apps["blog"]
	first.RestoreProven = &record.Drill{Snapshot: *first.LastOK, Time: first.LastOK.Time.Add(time.Minute)}
	st.Apps["shop"] = &first
	if evs := r.poll(t, st); len(evs) != 0 {
		t.Fatalf("a new app, its first good backup drilled by the run: %+v", evs)
	}

	r = newUnhealthyRig(t, 0)
	st = soundRecord(r.now.Add(backupEvery))
	u := r.now.Add(-4 * time.Hour)
	st.Unlisted = &u
	if evs := r.poll(t, st); len(evs) != 0 {
		t.Fatalf("no credential file: %+v", evs)
	}
}

// TestBackupUnhealthyStandsAsideForTheFour: where one of the four
// existing alerts explains the box, it fires alone. A critical one
// clears backup_unhealthy, which comes back once it has cleared; a
// warning (backup_run_failed) raises none over a quiet box, and clears
// none that fires, so that a channel taking critical alerts alone is
// never told it resolved while status is still unhealthy.
func TestBackupUnhealthyStandsAsideForTheFour(t *testing.T) {
	r := newUnhealthyRig(t, 60*24*time.Hour)
	at := func() time.Time { return r.now.Add(backupEvery) }
	unlisted := func(st *record.Status) *record.Status {
		u := at().Add(-4 * time.Hour)
		st.Unlisted = &u
		return st
	}
	runFailed := func(st *record.Status) *record.Status { st.Error = "the repository refused"; return st }
	if evs := r.poll(t, soundRecord(at())); len(evs) != 0 {
		t.Fatalf("healthy: %+v", evs)
	}
	if got := names(r.poll(t, runFailed(soundRecord(at())))); !got["backup_run_failed"] || len(got) != 1 {
		t.Fatalf("a failed run over a quiet box: %v", got)
	}
	if got := names(r.poll(t, unlisted(soundRecord(at())))); !got["backup_unhealthy"] || !got["-backup_run_failed"] || len(got) != 2 {
		t.Fatalf("unlisted: %v", got)
	}
	evs := r.poll(t, runFailed(unlisted(soundRecord(at()))))
	if got := names(evs); !got["backup_run_failed"] || !got["backup_unhealthy"] || got["-backup_unhealthy"] {
		t.Fatalf("a failed run beside a firing backup_unhealthy: %v", got)
	}
	if s, _ := summaryOf(evs, "backup_unhealthy"); !strings.Contains(s, "first: the repository has not been listed since") {
		t.Fatalf("summary = %q", s)
	}
	if got := names(r.poll(t, unlisted(soundRecord(at())))); !got["-backup_run_failed"] || !got["backup_unhealthy"] {
		t.Fatalf("the run put right: %v", got)
	}
	for _, c := range []struct {
		alert  string
		change func(st *record.Status)
	}{
		{"backup_stale/blog", func(st *record.Status) { st.Apps["blog"].LastOK.Time = at().Add(-4 * time.Hour) }},
		{"drill_failed", func(st *record.Status) {
			st.Apps["blog"].RestoreDrill = &record.Drill{Time: at(), Detail: "the restored database fails its integrity check"}
		}},
	} {
		st := unlisted(soundRecord(at()))
		c.change(st)
		got := names(r.poll(t, st))
		if !got[c.alert] || !got["-backup_unhealthy"] || len(got) != 2 {
			t.Errorf("%s explains the box: %v", c.alert, got)
		}
		got = names(r.poll(t, unlisted(soundRecord(at()))))
		if !got["-"+c.alert] || !got["backup_unhealthy"] {
			t.Errorf("after %s clears: %v", c.alert, got)
		}
	}
	// backup_unreadable: what fired stays fired.
	if err := os.WriteFile(r.path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.now = r.now.Add(backupEvery)
	if got := names(r.b.poll(r.now, r.path)); !got["backup_unhealthy"] || !got["backup_unreadable"] || got["-backup_unhealthy"] {
		t.Fatalf("unreadable: %v", got)
	}
}

// TestBackupUnhealthyClearsAndKeepsItsKey: the first problem line
// changing is the same alert, its summary new, never a clear and a
// fire; a healthy record clears it; the ages ticking change nothing.
func TestBackupUnhealthyClearsAndKeepsItsKey(t *testing.T) {
	r := newUnhealthyRig(t, 60*24*time.Hour)
	st := soundRecord(r.now.Add(backupEvery))
	u := r.now.Add(-4 * time.Hour)
	st.Unlisted = &u
	first, _ := summaryOf(r.poll(t, st), "backup_unhealthy")
	again, _ := summaryOf(r.poll(t, st), "backup_unhealthy")
	if first == "" || again != first {
		t.Fatalf("five minutes on, the same record: %q, then %q", first, again)
	}
	st.LastCheck.Class = record.CheckFailed
	evs := r.poll(t, st)
	got := names(evs)
	s, _ := summaryOf(evs, "backup_unhealthy")
	if got["-backup_unhealthy"] || !got["backup_unhealthy"] || s == first || !strings.Contains(s, "the repository check of") {
		t.Fatalf("another first line: %v %q", got, s)
	}
	if got := names(r.poll(t, soundRecord(r.now.Add(backupEvery)))); !got["-backup_unhealthy"] || len(got) != 1 {
		t.Fatalf("healthy again: %v", got)
	}
	if evs := r.poll(t, soundRecord(r.now.Add(backupEvery))); len(evs) != 0 {
		t.Fatalf("and after: %+v", evs)
	}
}

// TestBackupSummaries: the wording of the four messages that point to
// status (backup_run_failed, drill_failed in its two forms and
// backup_unhealthy), the laptop client's read first, the box's command
// second; and the pointer survives the cut at maxSummary, the line or
// the apps cut after it.
func TestBackupSummaries(t *testing.T) {
	const ptr = "motel backup status says %s (on the box: motel-backup status)"
	r := newUnhealthyRig(t, 60*24*time.Hour)
	st := soundRecord(r.now.Add(backupEvery))
	st.Error = "the repository refused"
	s, _ := summaryOf(r.poll(t, st), "backup_run_failed")
	if want := "the last backup run failed or ended early; " + fmt.Sprintf(ptr, "what"); s != want {
		t.Errorf("backup_run_failed:\n got %q\nwant %q", s, want)
	}
	st = soundRecord(r.now.Add(backupEvery))
	st.LastDrill.Detail = "could not begin"
	s, _ = summaryOf(r.poll(t, st), "drill_failed")
	if want := "the last restore drill proved nothing or found the repository damaged; " + fmt.Sprintf(ptr, "what"); s != want {
		t.Errorf("drill_failed:\n got %q\nwant %q", s, want)
	}
	st = soundRecord(r.now.Add(backupEvery))
	long := strings.Repeat("x", 60)
	for _, n := range []string{"blog", long + "1", long + "2", long + "3"} {
		a := *st.Apps["blog"]
		a.RestoreDrill = &record.Drill{Time: r.now, Detail: "no"}
		st.Apps[n] = &a
	}
	s, _ = summaryOf(r.poll(t, st), "drill_failed")
	if want := "the last restore drill did not prove these apps; " + fmt.Sprintf(ptr, "why") + ": blog, " + long + "1"; !strings.HasPrefix(s, want) {
		t.Errorf("drill_failed of apps:\n got %q\nwant it to begin %q", s, want)
	}
	if c := cleanText(nil, s); !strings.Contains(c, fmt.Sprintf(ptr, "why")) {
		t.Errorf("the cut took the pointer: %q", c)
	}
	st = soundRecord(r.now.Add(backupEvery))
	st.LastCheck.Class, st.LastCheck.Detail = record.CheckFailed, strings.Repeat("restic said something long ", 20)
	s, _ = summaryOf(r.poll(t, st), "backup_unhealthy")
	if want := "backups unhealthy; " + fmt.Sprintf(ptr, "what") + ", first: the repository check of "; !strings.HasPrefix(s, want) {
		t.Errorf("backup_unhealthy:\n got %q\nwant it to begin %q", s, want)
	}
	if c := cleanText(nil, s); len(c) > maxSummary || !strings.Contains(c, fmt.Sprintf(ptr, "what")) {
		t.Errorf("cut: %q", c)
	}
}

// startEngine is theEngine as the process starts, before any test
// replaces it.
var startEngine = theEngine

// TestTheEngineJudgesTheBoxsCredentialFile: the process's poller takes
// status's verdict from the box's credential file; a test's never reads
// the host's.
func TestTheEngineJudgesTheBoxsCredentialFile(t *testing.T) {
	if startEngine.backup.envFile != DefaultBackupEnvFile || newEngine().backup.envFile != "" {
		t.Fatalf("theEngine's %q, a test's %q", startEngine.backup.envFile, newEngine().backup.envFile)
	}
}

// TestBackupUnhealthyAcrossARestart: a process's first poll does not
// know whether the last process had backup_unhealthy firing beside a
// warning, so it keeps it firing: never a recovery told to a channel
// that takes critical alerts alone while status is unhealthy. The cost,
// a failed run alone told critical once a restart lands on it, is an
// accepted row.
func TestBackupUnhealthyAcrossARestart(t *testing.T) {
	r := newUnhealthyRig(t, 60*24*time.Hour)
	st := soundRecord(r.now.Add(backupEvery))
	u := r.now.Add(-4 * time.Hour)
	st.Unlisted, st.Error = &u, "the repository refused"
	if got := names(r.poll(t, st)); !got["backup_unhealthy"] || !got["backup_run_failed"] || len(got) != 2 {
		t.Fatalf("a restart's first poll, a failed run beside an unhealthy box: %v", got)
	}
}
