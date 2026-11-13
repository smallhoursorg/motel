#!/usr/bin/env python3
"""Says whether a pull request needs ci.yml's build-and-test jobs.

Reads the changed paths, one per line, on stdin (ci.yml's `changes` job
feeds it `git diff --name-only --no-renames` between the PR's merge
commit and its first parent, the base branch), and prints the
`heavy=true|false` line that job writes to $GITHUB_OUTPUT.

heavy=false only when every path is markdown, or a usage ledger file,
that no job reads: then lint, test, govulncheck, the e2e lanes, package
and install-test are skipped, and ci-ok accepts them skipped. gitleaks,
usage-check and dependency-review run on every PR whatever this says,
so usage/ and docs/footprint.md (which usage-check renders and diffs)
stay covered. Anything else, or any doubt, is heavy=true. Pushes to
main and the release's workflow_call run everything without asking.

No image reads markdown (.dockerignore keeps *.md out of every build
context, which soak.yml's paths-ignore leans on too), but tests and
install-test do: READ lists those files, and examples/ is heavy whole,
since examples/templates_test.go checks the links of every .md under
it. A test or a lane that starts reading another markdown file adds it
here, or a PR that changes only that file merges with what reads it
unrun (the full run on main would catch it then, after the merge). The
readers, found with:
  git grep -nE '"[^" ]*\\.md"|\\.md:/|"\\.md"' -- '*.go' Makefile e2e packaging
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
    if why:
        print(f"heavy: {why[0]}" + (f" and {len(why) - 1} more" if len(why) > 1 else ""), file=sys.stderr)
    else:
        print(f"light: {len(paths)} path(s), none read by a skipped job", file=sys.stderr)
    print(f"heavy={'true' if why else 'false'}")


if __name__ == "__main__":
    main()
