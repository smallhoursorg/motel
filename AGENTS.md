# Notes for coding agents

## Testing locally

CI is the gate: every pull request runs every lane, on x86 and arm64.
Locally, run only what covers your change, and let CI run the rest.
Everything runs in Docker through the Makefile; no host Go is needed.

- **Unit tests:** `make test PKGS=./notify/...` for the module you
  changed; `make test` for all of them (~1 min warm).
- **Lint, before every push:** `make lint MODULES="notify"`, naming
  each module you touched. It runs both tag sets.
- **Integration:** `make test-integration` (~2.5 min) only when your
  change touches what those tests drive for real: systemd units,
  restic, sqlite3, the box applier.
- **e2e:** run the lane for the code you changed, and in it only the
  split or suite you need. Each lane's `run.sh` lists its parts.

| Lane | Choose a part | A bare run |
|---|---|---|
| `make e2e-notify` | `NOTIFY_SPLITS=core`, `reload` or `heartbeat` | core and reload (~4 min). heartbeat is notify 15, motel's 5-minute hold (~9.5 min): name it only when you change the hold, `heartbeatHoldAfter` and the code that holds and resumes the heartbeat in `notify/channelfailed.go`. CI always runs it. |
| `make e2e-metrics` | `METRICS_SPLITS=view`, `vmalert`, `store` or `rules` | all four (~5 min) |
| `make e2e-backup` | `BACKUP_SUITES=setup`, `backup`, `units`, `status`, `restore` or `check` | all six |
| `make e2e` | | the whole lane |

`e2e-notify` and `e2e-metrics` share the receiver: do not run them at
once on one host.
