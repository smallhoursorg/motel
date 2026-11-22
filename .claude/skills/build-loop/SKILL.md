---
name: build-loop
description: How a feature or a ticket's fix is built in motel, from the ticket to a pull request the owner can merge. An Opus subagent builds it in its own worktree, reviews its own branch with /code-review and scores every finding with the triage skill (up to four rounds), opens the pull request, then answers Copilot's reviews in a loop (or an independent reviewer's, when Copilot cannot review) until a round leads to no fix. Use it whenever the owner asks to build, add or implement something, hands tickets to subagents, or says "the usual loop", "self-review and triage" or "Opus subagents", and again before telling the owner that a pull request is ready.
---

# Build loop: from a ticket to a pull request the owner can merge

Four things go wrong when a change is built without a set process.
Code reaches the owner that only its author has read. Findings are
fixed or dropped by mood, not by the rule. Reviews feed themselves:
each round's fixes give the next round something to find. And a fix
for something small breaks something that matters more.

So the work is split between two roles, every finding goes through
the `triage` skill, every loop has a cap, and a fix that brings a
fault in is a reason to go back, not only forward.

| Role | Who | Does |
|---|---|---|
| **Coordinator** | the session the owner talks to | plans, briefs, checks, reports. Writes no code, however small the fix: a fix made by the session that runs the loop has no reader but its author. |
| **Builder** | a subagent on Opus (`model: opus`), one per ticket, in its own worktree | owns the whole flow for its ticket: build, self-review, triage, the pull request, Copilot's rounds. |

| Cap | Default |
|---|---|
| Self-review rounds before the pull request | 4 |
| Copilot rounds | 3, at `balanced` effort (about $1.50 a round) |
| Final round by the coordinator, on a model other than the builder's | 1 |

The owner may name other caps or another effort for a job. Ask when
the job is not like the last one; do not carry a figure over from
another session.

**Which copy of this file.** The Skill tool loads the copy in the
checkout the session runs in, which may sit behind main. The
coordinator reads this file as it is on a freshly fetched
`upstream/main`. A builder reads the copy in its own worktree.

## 1. Before any code (coordinator)

- **Read what is already there.** Read the code on a freshly fetched
  `upstream/main`, the issue and the pull requests that led to it. A
  part of the job may already be built.
- **Find out what others hold.** Ask the owner which parts of the
  code another session or agent is working on, and read the open
  pull requests. Those parts are out of scope, and the brief says so.
- **Score first.** An item from an issue's backlog is scored with the
  triage skill before a branch is cut. One that scores accept is
  closed with its scores, not built.
- **Explain the change and get the owner's calls.** Say what will be
  built and where. Where the design has a real choice, write out all
  the options with what each costs, then recommend one. Code starts
  when those calls are made. A change to docs alone can start at once.
- **Split by independence, not by size.** Each pull request is correct
  and complete with the others absent. A complex fix lands whole in
  its own pull request, never as a smaller version with the rest owed.

## 2. Brief the builder (coordinator)

A builder knows only what its brief says. The brief carries:

- the ticket, the calls the owner made, and what is out of scope;
- a new worktree and a branch cut from a freshly fetched
  `upstream/main` (the fork's `main` goes stale);
- this skill by its path in the builder's own worktree
  (`.claude/skills/build-loop/SKILL.md`, read whole), the caps for
  this job, and where the triage rule is read from (section 4);
- what this machine needs that the repository does not record: how to
  push, how to request Copilot, anything `AGENTS.md` does not say;
- **do not stop to wait.** A builder that reports while CI or a
  review is still pending has finished, and someone has to wake it.
  It waits for them and goes on until the loop is done;
- **when to stop and ask** instead of deciding: a design choice the
  owner did not make, an accept over the bar, a fix flagged one-way
  or wide, anything that widens who may deploy or sign in on a box.
  While it waits it stays on the safe default;
- what to send back: the pull request and its head commit, each
  round's triage table, what was not run, and every statement it did
  not check marked as unchecked.

Builders for independent tickets start together, in one message.

## 3. Build (builder)

- The change and its tests land together.
- **A test for a fix fails without the fix.** Run it once on the code
  with the fix taken out. A test that passes either way proves
  nothing, and reviews have found such tests.
- `AGENTS.md` says what to run locally: the unit tests for the
  modules touched, and `make lint MODULES="..."` before every push.
  Do not hold a push for a local e2e run. CI runs every lane.
- CI scans every commit for secrets, not only the tree. A made-up
  token in a test is marked `gitleaks:allow` in the commit that adds
  it, or the branch has to be rewritten.

## 4. Self-review and triage, up to four rounds (builder)

One round is:

1. Commit, then run `/code-review high <base>..<branch>`. `<base>`
   is the commit id that `git merge-base upstream/main <branch>`
   prints, and `<branch>` is the branch by name. From that commit
   the range is the branch's own change, whatever main has gained
   since, and a commit id does not move when another session
   fetches. `upstream/main` by name can move under a running review,
   and then shows main's later changes as the branch's deletions.
   `HEAD` in the main checkout is not the worktree's, so a range
   that ends in `HEAD` reviews nothing. No merge of main is needed
   for a review.
2. Read the triage rule: the whole of
   `.claude/skills/triage/SKILL.md` **in the worktree of the branch
   under review**, in the same turn as the scoring. Not the Skill
   tool, which loads the main checkout's copy, and that checkout may
   sit on an older commit. Not memory, and not a summary: scores
   given from an earlier reading have come out wrong. Then read the
   subsystem's rows that the rule's first section points to.
3. Score every finding that names a failure, the ones being fixed
   too: one line each. The rule's second section says what is not
   scored.
4. Do what the rule says. A fix is made at once, with the test that
   proves it, and no larger than its score. What is accepted or owed
   is recorded as the rule's fifth section says.

Three scoring mistakes that have each cost a round:

- **Severity 1 is logging only.** A wrong hint or message is 2. And a
  failure whose whole effect is a line in a journal is 1, however
  untidy it looks: it gets a fix only when the fix is size 1 and
  risk 1.
- **A suggestion that names no failure is not scored.** It is a
  feature for the owner to weigh, not a finding to accept or fix.
- **Detection is how the failure shows to the person,** not how easy
  it was for the reviewer to see.

Whether to go round again. The first of these that applies decides:

- **The round found nothing the rule says to fix:** stop.
- **Its findings are faults the last round's fixes brought in:**
  section 9, before any more fixes.
- **It returned only hardening, wording and shape checks** for code
  with no live caller: make the fixes the rule still calls for,
  record the rest as the rule says, and stop. This is the sign that
  the review has begun to feed itself.
- **The cap is reached:** stop going round. The cap holds back more
  rounds of review. It does not turn a finding the rule says to fix
  into an accept: that fix is still made.
- **Otherwise the round made fixes:** go again, since those fixes
  have had no reader. This is how a regression a fix brings in gets
  caught.

Fixes made in a round that is not followed by another are read by
Copilot's rounds and the coordinator's final round.

When the last allowed round still finds real bugs in the product
path, tell the coordinator. The owner may raise the cap, or ask for
a pass by a different model that is given the code and the intent
but not the earlier findings.

## 5. Open the pull request (builder)

- Unit tests and lint are green first.
- Open an ordinary pull request, never a draft: an agent's token
  cannot mark a draft ready, and closing one to open another loses
  its reviews.
- The description says what changes and why, and which issue it
  closes. It holds each round's triage table, and it states the
  review bar in words, since Copilot cannot read the triage skill. A
  fix flagged one-way or wide is named for the person who merges.
  Choices the builder made that the owner did not are listed under
  "For the owner to confirm", each with its alternative.

**A small fix goes the other way round.** For a few lines, a test
alone or a one-line change, open the pull request as soon as it is
green and request Copilot at once. Run the self-review while Copilot
reads. Nothing is gained by putting two reviews of a small change one
after the other.

**A pull request stacked on another that is still open:**

- Branch from the other's branch, and merge that branch in whenever
  it moves: before each review round and before each push. Merge,
  never rebase.
- Review your own diff: `/code-review high <base>..<branch>`, where
  `<base>` is the commit id `git merge-base <base branch> <branch>`
  prints. Main reaches a stacked branch only through its base
  branch: do not merge `upstream/main` into it yourself.
- Open it against `main`, since upstream has no such base branch. Its
  first paragraph says which pull request it is stacked on and is
  merged after, that the diff GitHub shows holds the other's too
  until then, and links its own diff (a compare of the two branches
  on the fork).
- Something wrong in the base pull request's code is not fixed in
  yours: post it as one plain comment on the base pull request,
  scored, and say so in your report.
- When the base is squash-merged, your branch conflicts with main on
  every file both touched. With the base's final head merged into
  your branch first, no conflict is resolved by hand:
  `git merge-tree --write-tree --merge-base=<base's final head> HEAD upstream/main`
  gives the tree; check that your own diff is unchanged with
  `git patch-id`; commit it with both parents; fast-forward; push
  without force. Then take the stacked note out of the description.

## 6. Copilot's reviews, in a loop (builder)

1. Request a review and name the effort every time. A request with no
   effort reuses the one last used on that pull request.

   ```
   gh api -X POST repos/smallhoursorg/motel/pulls/N/copilot-code-review -f review_effort=balanced
   ```

2. Wait for it, then read the review: its threads, its body (a
   finding can be in the body alone) and the pull request's plain
   comments.
3. Read the triage rule again (section 4) and score every finding.
4. Fix, accept or owe as the rule says. Reply on each thread with the
   finding's scores, and keep a "Copilot's review, triaged" table in
   the description as well: a reply on a thread is easy to miss.
5. Push. Request again only if a fix was pushed after the commit
   the review read. Triage rows and the description are not fixes,
   and alone they call for no other round.

Stop when a review whose commit holds every fix leads to no fix, or
at the cap. Most pull requests stop at the second round: the third
is for when the second still found something the rule says to fix.
CI is green on the head commit before the builder reports.

**When Copilot cannot review** (its answer says the quota is spent,
or the request is refused): do not go on without a second reader.
Say so in the report, and the coordinator tells the owner at once.
With the owner's word, an independent reviewer stands in:

- a fresh subagent, not the builder, on the model the owner names;
  with none named, a model other than the builder's, and other than
  the last reviewer's where the two differ;
- given the branch, the range as two commit ids, and what the
  change is meant to do,
  and told not to read the pull request's description, its threads
  or any earlier finding; asked to reproduce what it can with a
  throwaway test outside the branch; it reviews and changes nothing;
- the first round over the whole diff, each later round over the
  commits that fixed the round before;
- as many rounds as the owner allows. The coordinator scores the
  findings and sends the fixes to the builder, and the description
  gets a table for each round.

## 7. Before telling the owner it is ready (coordinator)

- **Check the builder's work, do not relay it.** Read the triage
  rule yourself, as section 4 says, in the turn in which you score
  or check scores: the coordinator is held to the same reading as
  the builder. A statement of fact in a row comes from code someone
  read or a command someone ran. A builder's "presumably" is neither.
- **Run one more round on the final head, when anything was pushed
  after the builder's last self-review round.** Those fixes have had
  no reader of ours, and the coordinator's model is a different
  reader from the builder's. Run `/code-review high` once, over the
  range of section 4's first step, and score the findings. A
  coordinator on the builder's model does not run it: it has a
  fresh subagent on another model run the same command and return
  the findings, and scores those. That needs no word from the
  owner. Fixes go back to the builder by message, and the
  coordinator reads each fix's diff before the report: with no
  round left, that read is all the fix gets, and the report says
  so.
- **Send the builder the failure, not the fix.** Say what goes wrong
  and what must hold afterwards, and let the builder choose the
  change. A design the coordinator dictates has no reader either,
  and a loose word in it ("whatever state") becomes the next fault.
- **Do not merge into a branch, or push to it, while a review of it
  runs.** The branch's tip is the end of the range, and a tip that
  moves under a review changes what it reads. When main must come
  in (a conflict), `git merge upstream/main` once the round is back.
- **Read the plain comments** on the pull request as well as the
  review threads. A finding from another session can sit there.
- **Run what a person will see.** For a change to a screen or a
  command's output, run it once: the TUI's `--demo` in a real
  terminal (tmux in a container will do), or the command itself.
  Tests read the screen through goldens and a simulator, which is
  not a terminal.
- **Check the state on GitHub:** CI green on the head commit, the
  pull request mergeable, not a draft, no auto-merge.

Then report: the pull request and its head, the rounds that ran and
what each found, every accept over the bar, every one-way or wide
flag, and anything that was not run. Say for each part of the change
what last read it: a review round, Copilot, an independent reviewer,
or the coordinator's read of a diff. The owner merges.

**Put choices to the owner in plain words.** Say what a person at
the keyboard would see with each option, not the code's names for
it, then what each costs and which you would take. An option the
owner cannot picture is not a choice.

## 8. Scope the owner adds after the rounds

The caps bound how often the same change is read again. They do not
cover code nobody has read. When the owner adds to a pull request
after its rounds (an answer to an open question, a "do this too"):

- **If it stands on its own,** it is a new ticket and a new pull
  request, with its own loop.
- **If it belongs in the same pull request,** the builder builds it
  in commits of its own. The new commits then get one self-review
  round over their range alone
  (`/code-review high <last reviewed commit>..<branch>`, run before
  main is merged into the branch again, so that the range holds the
  new commits and nothing of main's), scored,
  and the pull request gets one more Copilot round on its final
  head, whatever the caps say: Copilot has not seen the new code.
- Fixes from those two get the coordinator's read of their diff, as
  in section 7.

Do not call the pull request ready until the new scope has had both.
When Copilot cannot review, section 6's independent reviewer takes
its place.

## 9. When a fix brings a fault in

A round whose findings are faults in the last round's fixes is the
triage rule's stop made visible: the guard has begun to cost more
than it buys. Before any more fixes:

1. **Score the first finding again,** the one the fixes were for,
   with the rule read in that turn. A finding scored too high is the
   usual start of the trouble.
2. **If it is severity 1, or was never over the bar,** take the fixes
   back and record it. No question for the owner is needed.
3. **If the rule still says fix,** fix forward once: the builder
   fixes the faults, and the next reading reads those fixes. That is
   a self-review round where the cap leaves one, and otherwise
   Copilot's next round or the coordinator's final round.
4. **If that reading too finds faults its fixes brought in,** stop
   fixing. Put two options to the owner in plain words: go back to
   the last state a review read whole and accept what is left, or
   fix forward with another round. An accept over the bar is the
   owner's call, and the row says so.

Going back:

- `git revert` the fix commits, newest first. No history is
  rewritten and nothing is force-pushed.
- Prove the result. Build the same thing another way in a scratch
  worktree (the last reviewed commit, the fixes that are kept
  cherry-picked, main merged in) and compare trees: they are equal.
  Run the reviewers' throwaway tests for the faults the fixes brought
  in: they pass.
- Record what is left as rows, and file a follow-up issue when the
  owner asks for one: what was found, why it was left, what each
  attempt broke, and what a fix has to get right.
- Correct the description: no table claims a fix the code no longer
  has.
