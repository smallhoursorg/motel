package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/user"
	"strconv"
	"syscall"

	"github.com/smallhoursorg/motel/notify"
)

// monitorJSON is a config's monitor app, nil for none or for a config
// that is not JSON, which is Caddy's to refuse.
func monitorJSON(cfg []byte) []byte {
	var c struct {
		Apps struct {
			Monitor json.RawMessage `json:"monitor"`
		} `json:"apps"`
	}
	if json.Unmarshal(cfg, &c) != nil || len(c.Apps.Monitor) == 0 || string(c.Apps.Monitor) == "null" {
		return nil
	}
	return c.Apps.Monitor
}

// warnMonitor says what notify.LoadWarnings finds of the config's
// monitor block (its JSON, nil for none): what setup has not made, at
// validate and reload, whose loads would journal it alone; units that
// differ from this binary's, at run too.
func warnMonitor(cmd string, monitor []byte, stderr io.Writer) {
	for _, w := range notify.LoadWarnings(cmd, monitor) {
		_, _ = fmt.Fprintf(stderr, "WARNING: %s\n", w)
	}
}

// notifyStateDir and notifySocket are setup's state directory and
// Alertmanager's socket (notify's defaults): a config with no monitor
// block names neither. Variables only so a test can stand others in.
var (
	notifyStateDir = notify.DefaultStateDir
	notifySocket   = notify.DefaultSocket
)

// sendNothing is the gate's at `motel run` alone, for a config whose
// monitor block runs no notify (none, or a metrics line alone): it
// starts no notify App, so nothing would take back the render a config
// with channels left, and Alertmanager would end what it holds at
// resolve_timeout with a recovery that did not happen
// (notify.SendNothing). A reload's case is notify's own
// (engine.release); a config that runs notify renders its own at Start.
// A process that does not own the state directory is not the service's
// start, and leaves it alone, saying so (notOwner).
func sendNothing(monitor []byte, stderr io.Writer) {
	if notify.ReadsSecrets(monitor) {
		return
	}
	if why := notOwner(notifyStateDir); why != "" {
		_, _ = fmt.Fprintf(stderr, "WARNING: monitor: this config has no monitor block with channels, and Alertmanager was not given the config that sends nothing: %s; %s\n", why, notOwnerSaid)
		return
	}
	switch changed, err := notify.SendNothing(notifyStateDir, notifySocket); {
	case err != nil:
		_, _ = fmt.Fprintf(stderr, "ERROR: monitor: this config has no monitor block with channels, and Alertmanager was not given the config that sends nothing: what it holds may reach the channels as a recovery that did not happen: %v\n", err)
	case changed:
		_, _ = fmt.Fprintln(stderr, notify.SendNothingMessage)
	}
}

// euid is this process's effective uid. A variable only so a test can
// run as a user that does not own its state directories.
var euid = os.Geteuid

// notOwner is why the gate at `motel run` leaves dir, a state directory
// setup made the motel user's, alone: this process runs as another
// user, so it is not motel.service's start but a run by hand (root
// trying a file on a live box), whose writes would retire what the
// running service rendered without telling it, and leave files that
// user owns, which the service cannot read. "" when it runs as dir's
// owner, or when dir's owner is not known: dir missing (setup has not
// run), which the gate leaves unsaid, or another error, which the write
// it then tries says.
func notOwner(dir string) string {
	fi, err := os.Stat(dir)
	if err != nil {
		return ""
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || int(st.Uid) == euid() {
		return ""
	}
	return fmt.Sprintf("%s is owned by %s, and this motel runs as %s", dir, userText(int(st.Uid)), userText(euid()))
}

// notOwnerSaid ends what the gate says when notOwner leaves a directory
// alone: for root by hand, that the running service is untouched; for
// a service that runs as a user other than setup's, that its starts
// never retire anything.
const notOwnerSaid = "only a start as that directory's owner, motel.service's, acts on it: a run by hand leaves the running service's state as it is, and a motel.service run as another user never acts on it"

// userText is a uid as a message names it: the user's name, or the
// number when it has none.
func userText(uid int) string {
	if u, err := user.LookupId(strconv.Itoa(uid)); err == nil {
		return u.Username
	}
	return "uid " + strconv.Itoa(uid)
}

// reloadForces reports whether `motel reload` must load the config even
// when it is unchanged: a monitor block with channels reads the box's
// secrets file at every load, which the config does not hold, so a
// mended _box.env leaves the Caddyfile as it was, and Caddy's
// unchanged-config check would load nothing (it answers 200, saying
// `config is unchanged` in the server's journal alone), leaving the
// channels a boot dropped dropped. `systemctl reload motel`, which the
// box's push uses after it writes the secrets, passes --force for the
// same reason (packaging/motel.service); this is that, for the command
// an operator types. A --force the command already names, true or
// false, is left as it is.
func reloadForces(args []string, monitor []byte) bool {
	if !notify.ReadsSecrets(monitor) {
		return false
	}
	fs, ok := commandFlags("reload", args)
	return ok && !fs.Changed("force")
}
