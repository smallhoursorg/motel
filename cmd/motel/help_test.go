package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestHelpNamesTheBinary: the usage lines Caddy's CLI prints name the
// binary, motel, not caddy (the deploy of a8038b4 to hotserve-1 found
// `caddy monitor setup [--metrics]` in the help). main runs in a child,
// since Caddy's commands are built once a process.
func TestHelpNamesTheBinary(t *testing.T) {
	if args := os.Getenv("MOTEL_HELP_ARGS"); args != "" {
		os.Args = append([]string{"motel"}, strings.Fields(args)...)
		main()
		return
	}
	for _, c := range []struct{ args, want string }{
		{"monitor --help", "  motel monitor [command]\n"},
		{"monitor setup --help", "  motel monitor setup [--metrics] [flags]\n"},
		{"monitor send --help", "  motel monitor send --source <name>"},
		{"--help", "  motel [command]\n"},
	} {
		child := exec.Command(os.Args[0], "-test.run=^TestHelpNamesTheBinary$")
		child.Env = append(os.Environ(), "MOTEL_HELP_ARGS="+c.args)
		out, err := child.CombinedOutput()
		if err != nil || !strings.Contains(string(out), "Usage:\n"+c.want) {
			t.Errorf("motel %s: %v\n%s\nwant the usage %q", c.args, err, out, c.want)
		}
		if strings.Contains(string(out), "caddy monitor") || strings.Contains(string(out), "Use \"caddy ") {
			t.Errorf("motel %s names caddy:\n%s", c.args, out)
		}
	}
}
