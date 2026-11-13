#!/usr/bin/env python3
"""Says whether a pull request needs ci.yml's build-and-test jobs.

Reads the changed paths, one per line, on stdin (ci.yml's `changes` job
feeds it `git diff --name-only --no-renames` between the PR's merge
commit and its first parent, the base branch), and prints the
`heavy=true|false` line that job writes to $GITHUB_OUTPUT.

heavy=false only when there are paths and every one is markdown, or a
usage ledger file, that no job reads: then lint, test, govulncheck, the e2e lanes, package
and install-test are skipped, and ci-ok accepts them skipped. gitleaks,
usage-check and dependency-review run on every PR whatever this says,
so usage/ and docs/footprint.md (which usage-check renders and diffs)
stay covered. Anything else, or any doubt, is heavy=true: an empty
list too, since a diff that went wrong reads as one. Pushes to main and
the release's workflow_call run everything without asking. A PR is
judged by the base branch's copy of this file, never its own, so an
edit to the rule cannot skip the checks of the PR that makes it.

No image build reads markdown (e2e/Dockerfile's `COPY . .` copies the
nested files, .dockerignore's `*.md` matching the root's alone, and
nothing in the build opens them), but tests and install-test do: READ lists those files, and examples/ is heavy whole,
since examples/templates_test.go checks the links of every .md under
it. A test or a lane that starts reading another markdown file adds it
here, or a PR that changes only that file merges with what reads it
unrun (the full run on main would catch it then, after the merge). The
readers, found with:
  git grep -nE '"[^" ]*\\.md"|\\.md:/|"\\.md"' -- '*.go' Makefile e2e packaging

Accepted residuals (scored by the triage skill: severity, occurrence,
detection, their product; the fix's size and risk):

- A new reader not added to READ. A test or lane starts reading a
  markdown file and READ is not given it; a later PR changing only that
  file skips the reader, and main's full run goes red after the merge,
  naming the test. 2/2/1 = 4; a scan that finds readers is a new file
  of heuristics, size 3, risk 2. The grep above is the check by hand.
- ci-ok's list of gated jobs and the jobs' `if:` lines are two lists. A
  job gated without being added to ci-ok's list leaves ci-ok red on the
  first light PR, naming that job: 2/3/1 = 6, red and named, which the
  single-fault rule asks for. One list of the jobs that must always run
  instead would pass, as skipped, a job skipped for any other reason.
"""
import sys

# The markdown some job reads, and the job:
READ = frozenset({
    "backups/README.md",   # backups/engine/lifecycle_invariants_test.go
    "box/DESIGN-box.md",   # box/units_test.go, box/failure_modes_test.go
    "docs/backups.md",     # install-test: smoke.sh runs its commands
    "docs/hardening.md",   # liveswap/sandbox_promises_test.go
})


def heavy(path):
    if path in READ or path.startswith("examples/"):
        return True
    # A fixture under testdata/ is read by its package's tests whatever
    # its extension.
    if "testdata" in path.split("/"):
        return True
    return not (path.endswith(".md") or path.startswith("usage/"))


def main():
    paths = [line.strip() for line in sys.stdin if line.strip()]
    why = [p for p in paths if heavy(p)]
    if not paths:
        print("heavy: no changed paths read", file=sys.stderr)
    elif why:
        print(f"heavy: {why[0]}" + (f" and {len(why) - 1} more" if len(why) > 1 else ""), file=sys.stderr)
    else:
        print(f"light: {len(paths)} path(s), none read by a skipped job", file=sys.stderr)
    print(f"heavy={'true' if why or not paths else 'false'}")


if __name__ == "__main__":
    main()
