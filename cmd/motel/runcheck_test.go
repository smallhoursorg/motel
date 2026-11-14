package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smallhoursorg/motel/notify"
)

// liveswapConfig is a Caddyfile whose liveswap block names root, or
// none for "".
func liveswapConfig(t *testing.T, root string) string {
	t.Helper()
	config, _ := dirs(t)
	line := ""
	if root != "" {
		line = "\t\troot " + root + "\n"
	}
	file := config + "/Caddyfile"
	write(t, file, "{\n\tadmin off\n\tliveswap {\n"+line+"\t}\n}\n", 0o644)
	return file
}

// `motel run` by a user other than the liveswap root's owner (root
// trying a file by hand beside motel.service) is refused before the
// gate writes anything: the service's Alertmanager config is as it was,
// and the one line said is the refusal, with what to do instead.
// validate and reload of the same file go on.
func TestARunByHandIsRefusedBeforeAnythingIsWritten(t *testing.T) {
	root := t.TempDir()
	file := liveswapConfig(t, root)
	state := t.TempDir()
	wasDir, wasSock, wasMetrics := notifyStateDir, notifySocket, metricsStateDir
	notifyStateDir, notifySocket = state, filepath.Join(t.TempDir(), "absent.sock")
	metricsStateDir = filepath.Join(t.TempDir(), "motel-metrics")
	t.Cleanup(func() { notifyStateDir, notifySocket, metricsStateDir = wasDir, wasSock, wasMetrics })
	rendered := "# Rendered by motel\nreceivers: [ops]\n"
	write(t, filepath.Join(state, notify.ConfigName), rendered, 0o640)
	asAnotherUser(t)

	var stderr bytes.Buffer
	if !gate([]string{"run", "--config", file}, &stderr) {
		t.Fatalf("went on: %s", stderr.String())
	}
	if b, _ := os.ReadFile(filepath.Join(state, notify.ConfigName)); string(b) != rendered {
		t.Errorf("the state directory was changed: %q", b)
	}
	said := stderr.String()
	for _, want := range []string{
		"Error: this `motel run` is refused, and nothing was changed: the liveswap root " + root + " is owned by ",
		", not as its owner. On a box, motel runs as motel.service",
		"`motel validate --config <file>`",
		"`journalctl -u motel`",
	} {
		if !strings.Contains(said, want) {
			t.Errorf("said %q; want %q in it", said, want)
		}
	}
	if strings.Count(said, "\n") != 1 {
		t.Errorf("said more than the refusal: %q", said)
	}
	for _, cmd := range []string{"validate", "reload"} {
		stderr.Reset()
		if gate([]string{cmd, "--config", file}, &stderr) || strings.Contains(stderr.String(), "refused") {
			t.Errorf("%s: %s", cmd, stderr.String())
		}
	}
}

// What goes on: the root's owner (motel.service's start), a config with
// no liveswap block, and a root that is not there yet (a first start).
// A root left to its default, or read from the environment, is
// judged where it leads.
func TestARunByTheRootsOwnerOrWithNoRootGoesOn(t *testing.T) {
	wasDir, wasMetrics, wasDefault := notifyStateDir, metricsStateDir, liveswapDefaultRoot
	notifyStateDir = filepath.Join(t.TempDir(), "motel-notify")
	metricsStateDir = filepath.Join(t.TempDir(), "motel-metrics")
	t.Cleanup(func() { notifyStateDir, metricsStateDir, liveswapDefaultRoot = wasDir, wasMetrics, wasDefault })
	run := func(file string) (bool, string) {
		var stderr bytes.Buffer
		stop := gate([]string{"run", "--config", file}, &stderr)
		return stop, stderr.String()
	}

	if stop, said := run(liveswapConfig(t, t.TempDir())); stop || said != "" {
		t.Errorf("the root's owner: stop %v, said %q", stop, said)
	}

	asAnotherUser(t)
	config, _ := dirs(t)
	write(t, config+"/Caddyfile", "{\n\tadmin off\n}\n", 0o644)
	if stop, said := run(config + "/Caddyfile"); stop || said != "" {
		t.Errorf("no liveswap block: stop %v, said %q", stop, said)
	}
	if stop, said := run(liveswapConfig(t, filepath.Join(t.TempDir(), "liveswap"))); stop || said != "" {
		t.Errorf("a root not there yet: stop %v, said %q", stop, said)
	}

	liveswapDefaultRoot = t.TempDir()
	if stop, said := run(liveswapConfig(t, "")); !stop || !strings.Contains(said, liveswapDefaultRoot+" is owned by ") {
		t.Errorf("the default root: stop %v, said %q", stop, said)
	}
	env := t.TempDir()
	t.Setenv("MOTEL_TEST_LIVESWAP_ROOT", env)
	if stop, said := run(liveswapConfig(t, "{$MOTEL_TEST_LIVESWAP_ROOT}")); !stop || !strings.Contains(said, env+" is owned by ") {
		t.Errorf("a root from the environment: stop %v, said %q", stop, said)
	}
}
