package main

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/caddyserver/caddy/v2"

	"github.com/smallhoursorg/motel/liveswap/backupdecl"
	"github.com/smallhoursorg/motel/notify"
)

// liveswapDefaultRoot is the liveswap root of a config that names none
// (backupdecl.DefaultRoot). A variable only so a test can stand another
// in: whether this machine has one is not the test's business.
var liveswapDefaultRoot = backupdecl.DefaultRoot

// refuseRunByHand refuses a `motel run` whose config has a liveswap
// block when this process is not the owner of that block's root: on a
// box that is root, or another user, starting a second motel beside
// motel.service, whose motel user owns the root (packaging/postinstall.sh).
// Such a run takes the service's admin socket before it fails on the
// ports, and liveswap's start can retire the sockets the service's
// apps are routed through (liveswap/DESIGN-liveswap.md, Accepted
// residuals). It runs first at `run`, before the gate writes anything,
// and says what to do instead. A config with no liveswap block, a root
// that does not exist yet (a first start) or one this process cannot
// look at, which it cannot write under either, goes on
// (notify.NotOwner). A config that is not JSON is Caddy's to refuse.
func refuseRunByHand(cfg []byte, stderr io.Writer) (stop bool) {
	var c struct {
		Apps struct {
			Liveswap *struct {
				Root string `json:"root"`
			} `json:"liveswap"`
		} `json:"apps"`
	}
	if json.Unmarshal(cfg, &c) != nil || c.Apps.Liveswap == nil {
		return false
	}
	// As liveswap's Provision reads it, from this environment, which the
	// envfiles have joined.
	root := caddy.NewReplacer().ReplaceKnown(c.Apps.Liveswap.Root, "")
	if root == "" {
		root = liveswapDefaultRoot
	}
	why := notify.NotOwner(root, euid())
	if why == "" {
		return false
	}
	_, _ = fmt.Fprintf(stderr, "Error: this `motel run` is refused, and nothing was changed: the liveswap root %s. On a box, motel runs as motel.service: systemctl starts and restarts it. To check a config, run `motel validate --config <file>`; to see why motel will not start, `journalctl -u motel`.\n", why)
	return true
}
