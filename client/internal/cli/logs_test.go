package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/smallhoursorg/motel/client/internal/termtext"
	"github.com/smallhoursorg/motel/client/internal/tui/model"
	"github.com/smallhoursorg/motel/liveswap/wire"
)

// Made-up journal cursors of journalctl's form.
const (
	cur1 = "s=0123456789abcdef0123456789abcdef;i=1;b=fedcba9876543210fedcba9876543210;m=1;t=5f0000000001;x=1"
	cur2 = "s=0123456789abcdef0123456789abcdef;i=2;b=fedcba9876543210fedcba9876543210;m=2;t=5f0000000002;x=2"
	cur3 = "s=0123456789abcdef0123456789abcdef;i=3;b=fedcba9876543210fedcba9876543210;m=3;t=5f0000000003;x=3"
)

// logsStep is one answer of a logsBox: a body, or a status with its
// Retry-After.
type logsStep struct {
	logs       *wire.Logs
	code       int
	retryAfter string
	body       string
}

// logsBox answers GET /_motel/apps/blog/logs with each of its steps in
// turn, the last again once they run out, and keeps each query and body.
type logsBox struct {
	srv     *httptest.Server
	mu      sync.Mutex
	steps   []logsStep
	queries []string
	bodies  [][]byte
}

func newLogsBox(t *testing.T, steps ...logsStep) *logsBox {
	t.Helper()
	lb := &logsBox{steps: steps}
	lb.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lb.mu.Lock()
		defer lb.mu.Unlock()
		if r.URL.Path != "/_motel/apps/blog/logs" {
			t.Errorf("asked %s", r.URL.Path)
		}
		st := lb.steps[min(len(lb.queries), len(lb.steps)-1)]
		lb.queries = append(lb.queries, r.URL.Query().Encode())
		w.Header().Set("Content-Type", "application/json")
		if st.retryAfter != "" {
			w.Header().Set("Retry-After", st.retryAfter)
		}
		b := []byte(st.body + "\n")
		if st.logs != nil {
			b = encode(st.logs)
		}
		lb.bodies = append(lb.bodies, b)
		w.WriteHeader(max(st.code, http.StatusOK))
		_, _ = w.Write(b)
	}))
	t.Cleanup(lb.srv.Close)
	return lb
}

// runLogs runs the client as runKey does, with a person's key, on lb,
// recording its sleeps; ctx is the command's.
func runLogs(t *testing.T, ctx context.Context, lb *logsBox, sl *sleeps, env map[string]string, args ...string) result {
	t.Helper()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "me.pub"), []byte(meLine+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	all := map[string]string{"MOTEL_KEY": "~/me.pub", "MOTEL_URL": lb.srv.URL + "/blog"}
	for k, v := range env {
		all[k] = v
	}
	var out, errOut bytes.Buffer
	code := Main(ctx, args, Env{
		Getenv: func(k string) string { return all[k] }, Stdout: &out, Stderr: &errOut, Dir: t.TempDir(), Home: home,
		Now: func() time.Time { return whoamiNow }, Sleep: sl.sleep,
		Transport: lb.srv.Client().Transport, Run: noPrograms{}, Signer: &fixedSigner{}, Version: "test",
	})
	return result{code, out.String(), errOut.String()}
}

var l0 = time.Date(2026, 11, 20, 23, 59, 58, 123456000, time.UTC)

// line is a line of blog's at l0 and s seconds, of release rel.
func line(s int, rel, text string) wire.LogLine {
	return wire.LogLine{At: l0.Add(time.Duration(s) * time.Second), Release: rel, Text: text}
}

// hostileLine is a line an app wrote to move the cursor, recolour the
// terminal, reverse what follows and end its row: every one is shown
// escaped, never acted on.
const hostileLine = "GET /cart 200 \x1b[31m12ms\x1b[0m\r\u009b2J\u202eevil\ttab"

// TestLogsTail: motel logs <app> is a tail: the newest lines, each its
// time and its text, a divider at the start and wherever the release or
// the day changes; the app's control characters and colour codes shown
// escaped; --json the box's answer as received; older lines not sent
// said before the lines.
func TestLogsTail(t *testing.T) {
	lb := newLogsBox(t, logsStep{logs: &wire.Logs{Kept: wire.KeptOn, More: true, Cursor: cur1, Lines: []wire.LogLine{
		line(0, "v42", "listening on :8080"),
		line(1, "v42", hostileLine),
		line(2, "v42", "a new day"),
		line(3, "", "fatal: cannot start"),
		line(4, "v43", "  indented, kept as it is"),
	}}})
	r := runLogs(t, context.Background(), lb, &sleeps{}, nil, "logs", "blog")
	if r.code != 0 || r.stderr != "" {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	const want = `-- older lines were not sent: a read sends at most 200 lines or 64 KiB of them, and none reaches further back --
-- 2026-11-20 UTC, release v42 --
23:59:58  listening on :8080
23:59:59  GET /cart 200 \x1b[31m12ms\x1b[0m\x0d\u009b2J\u202eevil\x09tab
-- 2026-11-21 UTC, release v42 --
00:00:00  a new day
-- 2026-11-21 UTC, release not kept by the journal --
00:00:01  fatal: cannot start
-- 2026-11-21 UTC, release v43 --
00:00:02    indented, kept as it is
`
	if r.stdout != want {
		t.Errorf("stdout:\n%s\nwant:\n%s", r.stdout, want)
	}
	if bad := unsafeOutput.FindString(r.stdout); bad != "" {
		t.Errorf("stdout holds %q", bad)
	}
	if lb.queries[0] != "" {
		t.Errorf("asked %q, want no query: the box's default count", lb.queries[0])
	}
	// --json: the box's answer, made safe as every --json is; the count
	// named is sent.
	r = runLogs(t, context.Background(), lb, &sleeps{}, nil, "logs", "blog", "--lines", "50", "--json")
	// The older lines left are said on stderr.
	if r.code != 0 || r.stdout != string(withNewline(termtext.JSON(lb.bodies[1]))) ||
		r.stderr != "motel: older lines were not sent: a read sends at most 50 lines or 64 KiB of them, and none reaches further back.\n" {
		t.Fatalf("--json: exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
	if bad := unsafeOutput.FindString(strings.TrimSuffix(r.stdout, "\n")); bad != "" {
		t.Errorf("--json holds %q", bad)
	}
	if lb.queries[1] != "lines=50" {
		t.Errorf("asked %q, want lines=50", lb.queries[1])
	}
}

// TestLogsSays: what the one read says beside lines: an app that keeps
// its output on the box, exit 0, said on stderr (and --json prints the
// box's answer); no line, said on stderr; a word for what it keeps the
// client does not know, exit 3; a count outside the box's set, and
// MOTEL_TOKEN without --key, refused before anything is sent; a 403
// said as the box's refusal, exit 5; a busy box's 503 not waited out by
// one read, exit 3.
func TestLogsSays(t *testing.T) {
	for _, c := range []struct {
		name   string
		step   logsStep
		args   []string
		env    map[string]string
		code   int
		stderr string
		stdout string
		sent   int
	}{
		{"kept off", logsStep{logs: &wire.Logs{Kept: wire.KeptOff, Lines: []wire.LogLine{}}}, nil, nil, 0,
			"motel: 127.0.0.1 keeps blog's output on the box (deploy_log_lines 0): it sends no line of it.\n", "", 1},
		{"kept off, json", logsStep{logs: &wire.Logs{Kept: wire.KeptOff, Lines: []wire.LogLine{}}}, []string{"--json"}, nil, 0,
			"motel: 127.0.0.1 keeps blog's output on the box (deploy_log_lines 0): it sends no line of it.\n", `{"kept":"off","lines":[],"more":false,"gap":false}` + "\n", 1},
		{"older lines, json", logsStep{logs: &wire.Logs{Kept: wire.KeptOn, More: true, Lines: []wire.LogLine{line(0, "v42", "one")}}}, []string{"--json"}, nil, 0,
			"motel: older lines were not sent: a read sends at most 200 lines or 64 KiB of them, and none reaches further back.\n",
			`{"kept":"on","lines":[{"at":"2026-11-20T23:59:58.123456Z","release":"v42","text":"one"}],"more":true,"gap":false}` + "\n", 1},
		{"no line", logsStep{logs: &wire.Logs{Kept: wire.KeptOn, Lines: []wire.LogLine{}}}, nil, nil, 0,
			"motel: 127.0.0.1 holds no line of blog in its journal.\n", "", 1},
		{"a word not known", logsStep{body: `{"kept":"rotated","lines":[]}`}, nil, nil, 3,
			"motel: 127.0.0.1 answered about blog's lines in a word for what it keeps this client does not know (rotated): it cannot tell whether they are sent\n", "", 1},
		{"a count not the box's", logsStep{}, []string{"--lines", "100"}, nil, 2,
			"motel: --lines \"100\" is not one of 200, 50, 1000\n", "", 0},
		{"a count with a control in it", logsStep{}, []string{"--lines", "5\x1b0"}, nil, 2,
			"motel: --lines \"5\\x1b0\" is not one of 200, 50, 1000\n", "", 0},
		{"a deploy token", logsStep{}, nil, map[string]string{"MOTEL_TOKEN": "x.y.z"}, 2,
			"motel: logs is asked with your own ssh key", "", 0},
		{"no logs grant", logsStep{code: http.StatusForbidden, body: `{"error":"this needs the logs role on this app","needs":"logs","scope":"app"}`}, nil, nil, 5,
			"motel: 127.0.0.1 does not allow your key this: it needs the logs role on this app.", "", 1},
		{"busy", logsStep{code: http.StatusServiceUnavailable, retryAfter: "1", body: `{"error":"the box is running as many logs reads as it runs at once; retry after 1 s"}`}, nil, nil, 3,
			"answered 503, not known to be moteld's own answer", "", 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			lb := newLogsBox(t, c.step)
			sl := &sleeps{}
			r := runLogs(t, context.Background(), lb, sl, c.env, append([]string{"logs", "blog"}, c.args...)...)
			if r.code != c.code || !strings.Contains(r.stderr, c.stderr) || c.stderr == "" && r.stderr != "" {
				t.Errorf("exit %d, stderr %q; want %d, %q", r.code, r.stderr, c.code, c.stderr)
			}
			if r.stdout != c.stdout {
				t.Errorf("stdout %q, want %q", r.stdout, c.stdout)
			}
			if len(lb.queries) != c.sent || len(sl.d) != 0 {
				t.Errorf("%d reads and sleeps %v, want %d reads and none", len(lb.queries), sl.d, c.sent)
			}
		})
	}
}

// TestLogsFollow: --follow tails, then reads after the cursor every
// LogsEvery; at once after a follow that stopped with lines left; keeps
// its cursor over a read that took no line; after a gap with a cursor,
// reads after that one; after a gap with none, tails again; waits out a
// busy box's 503 and a 429 by their Retry-After, saying so once; and
// ends, exit 0, when the box says the app keeps its output on the box.
// Each notice is a divider among the lines.
func TestLogsFollow(t *testing.T) {
	on := func(more, gap bool, cursor string, lines ...wire.LogLine) logsStep {
		return logsStep{logs: &wire.Logs{Kept: wire.KeptOn, Lines: append([]wire.LogLine{}, lines...), More: more, Gap: gap, Cursor: cursor}}
	}
	lb := newLogsBox(t,
		on(true, false, cur1, line(0, "v42", "one")),   // a tail with older lines left
		on(true, false, cur2, line(1, "v42", "two")),   // a follow with lines left: at once
		on(false, false, "", line(2, "v42", "")),       // no cursor, a line: kept where it was (cannot happen; held so)
		on(false, false, ""),                           // nothing new
		on(false, true, cur3, line(5, "v43", "three")), // a gap, a fresh tail
		on(false, true, ""),                            // a gap and no cursor: tail next
		logsStep{code: http.StatusServiceUnavailable, retryAfter: "1", body: `{"error":"busy"}`},
		// A 429 the client waits out itself, then one that stands, which
		// the follow waits out; the wait said once in the run.
		logsStep{code: http.StatusTooManyRequests, retryAfter: "7", body: `{"error":"slow down"}`},
		logsStep{code: http.StatusTooManyRequests, retryAfter: "7", body: `{"error":"slow down"}`},
		on(false, false, cur1, line(9, "v43", "four")), // the tail
		logsStep{logs: &wire.Logs{Kept: wire.KeptOff, Lines: []wire.LogLine{}}},
	)
	sl := &sleeps{}
	r := runLogs(t, context.Background(), lb, sl, nil, "logs", "blog", "--follow", "--lines", "50")
	if r.code != 0 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	q := func(after string) string {
		return "after=" + strings.NewReplacer(";", "%3B", "=", "%3D").Replace(after) + "&lines=50"
	}
	wantQ := []string{"lines=50", q(cur1), q(cur2), q(cur2), q(cur2), q(cur3), "lines=50",
		"lines=50", // the first 429, after the 503's wait
		"lines=50", // the second, after the client's own wait
		"lines=50", // after the follow's
		q(cur1)}
	if strings.Join(lb.queries, "\n") != strings.Join(wantQ, "\n") {
		t.Errorf("asked\n%s\nwant\n%s", strings.Join(lb.queries, "\n"), strings.Join(wantQ, "\n"))
	}
	e := model.LogsEvery
	wantS := []time.Duration{e, e, e, e, e, time.Second, 7 * time.Second, 7 * time.Second, e}
	if len(sl.d) != len(wantS) {
		t.Fatalf("slept %v, want %v", sl.d, wantS)
	}
	for i := range wantS {
		if sl.d[i] != wantS[i] {
			t.Errorf("slept %v, want %v", sl.d, wantS)
			break
		}
	}
	const want = `-- older lines were not sent: a read sends at most 50 lines or 64 KiB of them, and none reaches further back --
-- 2026-11-20 UTC, release v42 --
23:59:58  one
23:59:59  two
-- 2026-11-21 UTC, release v42 --
00:00:00  ` + `
-- a gap: the box no longer holds the line this follow had read to, so lines since then may be missing; these are its newest --
-- 2026-11-21 UTC, release v43 --
00:00:03  three
-- a gap: the box no longer holds the line this follow had read to, so lines since then may be missing; these are its newest --
-- 2026-11-21 UTC, release v43 --
00:00:07  four
`
	if r.stdout != want {
		t.Errorf("stdout:\n%s\nwant:\n%s", r.stdout, want)
	}
	for _, s := range []string{
		"motel: following blog's lines, a read every 2 s; ctrl-c stops.\n",
		"motel: 127.0.0.1 answered 503 with a wait of 1 s: following waits it out, and asks again until it answers.\n",
		"motel: 127.0.0.1 keeps blog's output on the box (deploy_log_lines 0): it sends no line of it.\n",
	} {
		if !strings.Contains(r.stderr, s) {
			t.Errorf("stderr lacks %q:\n%s", s, r.stderr)
		}
	}
	if strings.Count(r.stderr, "with a wait of") != 1 {
		t.Errorf("the wait is said more than once in a row:\n%s", r.stderr)
	}
}

// TestLogsFollowJSON: --follow --json prints each line as the box sent
// it, one JSON object a line, made safe as every --json is, and nothing
// else on stdout: a gap, older lines not sent and the end are the
// client's sentences on stderr, and a read that fails ends the follow
// with its exit and no object on stdout.
func TestLogsFollowJSON(t *testing.T) {
	lb := newLogsBox(t,
		logsStep{logs: &wire.Logs{Kept: wire.KeptOn, More: true, Cursor: cur1, Lines: []wire.LogLine{line(0, "v42", "one"), line(1, "", hostileLine)}}},
		// Spaces and line breaks inside a line's object, as another server
		// might write them: each line is still one line of stdout.
		logsStep{body: "{\"kept\":\"on\",\"lines\":[{\"at\":\"2026-11-21T00:00:00Z\",\r\n \"release\":\"v42\", \"text\":\"two\"}],\"gap\":true,\"cursor\":\"" + cur2 + "\"}"},
		logsStep{code: http.StatusInternalServerError, body: `{"error":"the app's journal could not be read; the box's journal says why"}`},
	)
	r := runLogs(t, context.Background(), lb, &sleeps{}, nil, "logs", "blog", "--follow", "--json")
	if r.code != 3 {
		t.Fatalf("exit %d, want 3; stderr %q", r.code, r.stderr)
	}
	got := strings.Split(strings.TrimSuffix(r.stdout, "\n"), "\n")
	if len(got) != 3 {
		t.Fatalf("stdout holds %d lines, want 3:\n%s", len(got), r.stdout)
	}
	for i, want := range []wire.LogLine{line(0, "v42", "one"), line(1, "", hostileLine), {At: time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC), Release: "v42", Text: "two"}} {
		var l wire.LogLine
		if err := json.Unmarshal([]byte(got[i]), &l); err != nil || !l.At.Equal(want.At) || l.Release != want.Release || l.Text != want.Text {
			t.Errorf("line %d: %v, %+v, want %+v", i, err, l, want)
		}
		if bad := unsafeOutput.FindString(got[i]); bad != "" {
			t.Errorf("line %d holds %q", i, bad)
		}
	}
	if got[2] != `{"at":"2026-11-21T00:00:00Z","release":"v42","text":"two"}` {
		t.Errorf("the box's line is %q, want its own JSON with no space", got[2])
	}
	for _, s := range []string{
		"motel: following blog's lines, a read every 2 s; ctrl-c stops.\n",
		"motel: older lines were not sent: a read sends at most 200 lines or 64 KiB of them, and none reaches further back.\n",
		"motel: a gap: the box no longer holds the line this follow had read to, so lines since then may be missing; these are its newest.\n",
		"answered 500, not known to be moteld's own answer; it says: \"the app's journal could not be read; the box's journal says why\" (following stopped).\n",
	} {
		if !strings.Contains(r.stderr, s) {
			t.Errorf("stderr lacks %q:\n%s", s, r.stderr)
		}
	}
	// A refusal of the arguments in this mode prints no object either.
	r = runLogs(t, context.Background(), lb, &sleeps{}, nil, "logs", "blog", "--follow", "--json", "--lines", "7")
	if r.code != 2 || r.stdout != "" {
		t.Errorf("a count not the box's: exit %d, stdout %q", r.code, r.stdout)
	}
	// Without --follow, a failure prints its object, as every --json does.
	r = runLogs(t, context.Background(), lb, &sleeps{}, nil, "logs", "blog", "--json", "--lines", "7")
	if r.code != 2 || !strings.HasPrefix(r.stdout, `{"error":`) {
		t.Errorf("--json alone: exit %d, stdout %q", r.code, r.stdout)
	}
}

// TestLogsFollowEnds: a refusal mid-follow ends it with that read's
// exit, saying following stopped; ctrl-c ends it, 130, nothing more on
// stdout.
func TestLogsFollowEnds(t *testing.T) {
	first := logsStep{logs: &wire.Logs{Kept: wire.KeptOn, Cursor: cur1, Lines: []wire.LogLine{line(0, "v42", "one")}}}
	lb := newLogsBox(t, first, logsStep{code: http.StatusForbidden, body: `{"error":"this needs the logs role on this app","needs":"logs","scope":"app"}`})
	r := runLogs(t, context.Background(), lb, &sleeps{}, nil, "logs", "blog", "--follow")
	if r.code != 5 || !strings.HasSuffix(r.stderr, "it needs the logs role on this app. `motel whoami` lists the roles you hold from here (following stopped).\n") {
		t.Errorf("a 403: exit %d, stderr %q", r.code, r.stderr)
	}
	ctx, cancel := context.WithCancel(context.Background())
	lb = newLogsBox(t, first)
	stop := &sleeps{}
	var out, errOut bytes.Buffer
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "me.pub"), []byte(meLine+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"MOTEL_KEY": "~/me.pub", "MOTEL_URL": lb.srv.URL + "/blog"}
	code := Main(ctx, []string{"logs", "blog", "--follow"}, Env{
		Getenv: func(k string) string { return env[k] }, Stdout: &out, Stderr: &errOut, Dir: t.TempDir(), Home: home,
		Now: func() time.Time { return whoamiNow },
		Sleep: func(ctx context.Context, d time.Duration) error {
			_ = stop.sleep(ctx, d)
			cancel()
			return ctx.Err()
		},
		Transport: lb.srv.Client().Transport, Run: noPrograms{}, Signer: &fixedSigner{}, Version: "test",
	})
	if code != 130 || out.String() != "-- 2026-11-20 UTC, release v42 --\n23:59:58  one\n" || len(lb.queries) != 1 {
		t.Errorf("ctrl-c: exit %d, stdout %q, %d reads", code, out.String(), len(lb.queries))
	}
}

// TestLogsFollowSays: a follow whose first read brings no line says the
// journal holds none, and waits; a 429 that names no wait is said as
// such, and waited 30 s.
func TestLogsFollowSays(t *testing.T) {
	off := logsStep{logs: &wire.Logs{Kept: wire.KeptOff, Lines: []wire.LogLine{}}}
	lb := newLogsBox(t, logsStep{logs: &wire.Logs{Kept: wire.KeptOn, Lines: []wire.LogLine{}}}, logsStep{logs: &wire.Logs{Kept: wire.KeptOn, Lines: []wire.LogLine{}}}, off)
	r := runLogs(t, context.Background(), lb, &sleeps{}, nil, "logs", "blog", "--follow")
	if r.code != 0 || strings.Count(r.stderr, "motel: 127.0.0.1 holds no line of blog in its journal; following waits for its first.\n") != 1 || r.stdout != "" {
		t.Errorf("no line: exit %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
	lb = newLogsBox(t, logsStep{code: http.StatusTooManyRequests, body: `{"error":"slow down"}`}, off)
	sl := &sleeps{}
	r = runLogs(t, context.Background(), lb, sl, nil, "logs", "blog", "--follow")
	if r.code != 0 || !strings.Contains(r.stderr, "motel: 127.0.0.1 answered 429 and named no wait: following waits 30 s, and asks again until it answers.\n") ||
		len(sl.d) != 1 || sl.d[0] != 30*time.Second {
		t.Errorf("a 429 with no wait: exit %d, stderr %q, slept %v", r.code, r.stderr, sl.d)
	}
}
