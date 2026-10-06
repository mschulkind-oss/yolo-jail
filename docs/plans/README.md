# Active plans & designs

**Status:** CURRENT — the index, **rebuilt against the tree 2026-08-23.** The rows for the macOS track, the
guest-notch handoff, `pack-system`, `environment-manager-plan`, `agent-config-packs`,
`antigravity-agy-support` and `cli-color-audit` (both retired 2026-09-27) were re-checked against the code that day, and
**four were wrong in the direction that matters** (D1 retired, D2 reverted, D3 superseded, and
`cli-color-audit` was finished — that row listed two remaining items, both of which had landed). The remaining rows carry their doc's own dated status,
unverified here. **If a row disagrees with the doc it points at, trust the doc and fix the row.**

**Extended 2026-09-12** with a section for [the five designs the last sprint built](#the-2026-09-sprint--five-designs-all-built),
which had no rows at all — the same failure one level up, since a doc this file never names cannot
be caught disagreeing with it.

**Re-checked 2026-09-24** against the docs the weekly refresh touched: rows whose doc changed state
were corrected, and the provider section gained rows for its four live docs it did not list.

**Re-checked 2026-09-25** for the docs whose status changed that day: the wire-bridge-gateway,
sso-backed-bedrock, base-home-legacy-state, provider-switching, host-tool-provisioning and
docs-website rows, plus a row for agent-footer, which had none.

This directory holds the **active** work — plans and designs we're currently
implementing or still discussing. Reference docs (how live systems work) live in
[`../reference/`](../reference); [`../design/`](../design) holds designs, and [`../research/`](../research)
holds investigations. Done or obsolete working docs are deleted, and
`git log --follow -- <path>` recovers any of them. How a built design moves into
`../reference/` is [the graduation rules](#oq-dt1).

> **Where to start:** [`roadmap.md`](roadmap.md) — the living forward plan, and the
> only doc here that answers "what is left?". Everything else in this directory is
> either a design/handoff for one piece of work or a historical record.
>
> [`sequencing-2026-07.md`](sequencing-2026-07.md) is the retired predecessor: a
> 2026-07-22 snapshot of the dependency ordering, kept for "why was this done in
> that order" rather than "what is next".
>
> **For the composed-config / packs cluster specifically**, start at
> [`BACKLOG.md`](BACKLOG.md): that cluster's design spans 8 docs, and BACKLOG is the
> only place that lists the implementable items in order, with a pointer per item to
> the doc holding its reasoning.

<!-- Three docs outside this file link the anchor below by its old literal text
     (AGENTS.md, roadmap.md, further-roadmap-ideas.md). The heading dropped its count
     when a sixth check was added; this keeps those links resolving. Do not delete it
     without fixing all three. -->
<a id="keeping-this-corpus-honest--the-five-checks-so-they-are-re-runnable"></a>

## Keeping this corpus honest — the checks, so they are re-runnable

The 2026-08-23 audit ran the first five by hand; four of them found something, and the fifth is
worth keeping because it is now clean and would not stay that way silently. The 2026-09-12 sprint
close-out ran all five again and added a **sixth**, which found a class the other five are
structurally unable to see. A **seventh** was added on 2026-09-13, when a census of the status slot
found twelve different words in it. They are not wired into `just`
yet — that proposal, with the allowlists it needs, is
[`further-roadmap-ideas.md`](further-roadmap-ideas.md) §I1. Until then, run them when a sprint
closes; the drift clusters there rather than spreading evenly.

```console
# 1. Every relative doc link resolves.              (found: 5, now 0)
# 2. Every live open question is countable.         (found: 6 invisible to the first regex)
$ rg -c '^(#{2,4} |\s*[0-9]+[a-z]?\. |\s*[-*] )(<a id="[^"]*"></a> ?)?💬' docs/ --sort path
# 3. Every backticked SHA resolves in THIS repo.    (found: 3 phantoms cited as evidence
#    on 2026-08-23; 2 more on 2026-09-13 — see the note below for why it keeps finding them)
$ for s in $(rg -o '`[0-9a-f]{7,40}`' docs/ internal/ -g '!.claude' \
    | sed 's/.*`\([0-9a-f]*\)`.*/\1/' | grep -v '^[0-9]*$' | sort -u); do   # drop CI run ids
    git rev-parse --verify --quiet "$s^{commit}" >/dev/null || echo "UNRESOLVED: $s"; done
# 4. Every backticked code path exists.             (found: 1 real rot among 15 hits)
# 5. Every in-doc heading link resolves.            (clean — but only with a CORRECT slugger:
#    GitHub maps each space to its own hyphen, so an em-dash heading yields `--`. A naive
#    slugger collapses them and reports 67 false positives.)
# 6. Every file:line citation points where it says. (no one-liner — see below)
# 7. Every status is in vocabulary.                 (found: 79 of 87 docs — 58 off-vocabulary
#    status lines, 21 docs with no status line at all, 8 frontmatter values — see below)
$ rg -n '^\*\*Status:\*\* ' docs/design docs/plans | rg -v \
  '\*\*Status:\*\* (SKETCH|DESIGN|DECIDED|BUILT|GRADUATED|SUPERSEDED), \d{4}-\d{2}-\d{2}|CURRENT —'
```

**Checks 3 and 4 need an allowlist or they cry wolf**: upstream `flake.lock` revs and other
projects' source are legitimately unresolvable, and a doc *recording a deletion* is supposed to name
the thing it deleted. The signal is a path or SHA offered as **evidence**, not one named as history.

**What check 3's allowlist actually holds**, swept 2026-09-13: four kinds, and only the first two
are the ones the paragraph above anticipated. Upstream revs (nixpkgs, via `flake.lock`); other
projects' commits quoted in research (opencode, the boundary broker); **git *tree* hashes**
(`kubernetes/kubernetes`' `master:hack` in
[`agent-config-distribution.md`](../research/agent-config-distribution.md)), which fail `^{commit}`
by construction rather than by rot; and **podman image IDs** in
[`minimal-disk-footprint.md`](../design/minimal-disk-footprint.md) and
[`disk-levers-and-backfill.md`](../design/disk-levers-and-backfill.md), which are not git objects at
all. The last two are why the check prints a hex string it cannot classify instead of calling one a
defect.

**Why the class recurs — the half worth knowing.** Commits here are rewritten between being authored
in a jail and landing on the remote, so a SHA can be **correct when typed and dead by the time
anyone reads it**. No amount of author care removes it, which is what makes check 3 a standing sweep
rather than a one-off cleanup. The 2026-09-13 run is the evidence: both phantoms it found were
written on 2026-09-04, *after* the 2026-08-23 sweep had pronounced the corpus clean — a doc cited
`8631caeb` and `04b3f039` for two `packs/zai` fixes that had landed as `3d9b1aa2` and `8e901423`.
Everything else it flagged was allowlist.

**`uvx vantage-check docs/` subsumes checks 1 and 5** and is the gate a doc commit passes:
`link/missing-target` is check 1, and `link/dead-section-anchor` is check 5 for both same-document
and cross-document anchors (verified 2026-09-12). It does **not** subsume 2, 3, 4 or 6 — it
validates that links resolve, never that a claim is true.

### Check 6 — every `file:line` citation points where it says it does

Added 2026-09-12. It is the only one of the six that finds **moved** things: checks 3 and 4 ask
whether a SHA or a path *exists*, and a citation whose file exists and whose line has drifted
eighty lines passes both of them while being exactly the wrong kind of wrong — a `file:line` is
where a reader stops checking.

There is no one-liner. It is a five-stage pipeline, and the stages exist because each one's output
is the next one's input:

1. **Extract.** Walk `docs/**/*.md` and match `<path>.<ext>:<start>[-<end>]` for
   `go|nix|sh|py|ts|jsonc|json|toml|lua|md`, recording the citing doc, its line number, the raw
   citation, and the whole source line as context. Track fenced code blocks and **flag** the
   citations inside them rather than dropping them — a pasted transcript is not a claim.
2. **Resolve.** Build a basename index of the repo, skipping `.git`, `vendor`, `dist-go`, `.yolo`,
   `node_modules`, `bin`, `.direnv`, `result`, `.claude` and `examples`, then map each citation to a
   real file: exact relative path first, then unique basename, then unique suffix match. The
   basename step carries most of the corpus, which writes citations short (`apply.go:170`,
   `run.go:82`) far more often than it writes them from the repo root.
3. **Disambiguate.** A basename with several candidates is resolved from the **citing document's
   own context** — the nearest other citation, or bare path mention, in that same doc that names
   one of the candidates wins. This is what decides *which* `prism.go` a bare `prism.go:494-511`
   meant, and it is why the pipeline cannot be one pass.
4. **Check bounds.** A citation whose start line is past the resolved file's EOF is stale, full
   stop. This half needs no judgement and should be treated as a finding, not a candidate.
5. **Check the symbol.** Take the backticked identifiers adjacent to the citation on its own line,
   keeping only plausible code names — CamelCase or `_`-bearing, five characters or more, so
   `HostRenderResult` counts and `the` does not — and ask whether **any** of them appears within
   **±6 lines** of the cited range. If none does, find where the nearest one actually lives and
   record the distance. **That distance is the triage order**: a symbol nine hundred lines from its
   citation is drift, and a symbol seven lines away is the window being one line too tight.

**The output is candidates, not findings.** The ±6 window is a guess and the near end of the
distance ranking is full of honest near-misses, so every row wants a human look. What the check buys
is the ranking: it takes every `file:line` in the corpus down to a couple of hundred worth reading,
sorted worst-first.

**What it found, and where the residue is.** Run against the tree at `1118cc52^` — before the
sprint's two citation sweeps — the corpus held 1,831 `file:line` citations outside code fences and
the pipeline flagged **234**: 128 in `docs/design/`, 93 in `docs/plans/`, 13 in `docs/research/`.
Re-run on 2026-09-12 against the swept tree it flagged **137**, and the split is the whole finding
— `docs/design/` fell from 128 to 33, `docs/plans/` moved from 93 to 91, and `docs/research/` did
not move at all. **The residue is a queue, not a false-positive tail**: the sweeps walked the
sprint's own design docs and never walked the other two trees, which is where the next run starts.
Both totals move whenever anyone edits a doc, so re-run rather than trust them; the durable part is
the concentration, not the count.

**Where the scripts are.** They were written in the close-out session's scratchpad as
`extract.py` → `resolve.py` → `disambig.py` → `check.py` → `symcheck2.py`, passing JSON between
stages. A scratchpad does not survive its session, which is why the method is written above as prose
precise enough to rebuild from: a check that lives only in a scratchpad is not re-runnable, and this
file carries no scripts for the other five either.

### Check 7 — the status vocabulary, and the one thing a `BUILT` line must say

Added 2026-09-13, after a census of the status slot. `docs/reference/` has exactly **one** status
value across all 41 files (`status: current`) and nobody ever wrote that down — evergreen could only
ever have one state. The planning tree is the opposite: the prose `**Status:**` line held **twelve**
distinct words, two of which (`STORIES`, `INVENTORY`) are **genre** labels rather than lifecycle
states, and `BUILT` and `SHIPPED` were exact synonyms split across six docs by nothing but which
agent typed the line.

#### The vocabulary — seven words, and the word names what is **owed**

That is the whole rule, and it is why there are four states and not a percentage: a reader opening a
planning doc is asking *"is there something here for me?"*, and the stage answers it.

| Stage | The doc is | What is owed |
| :--- | :--- | :--- |
| `SKETCH` | exploratory — it may be abandoned whole, and nobody builds from it | nothing yet |
| `DESIGN` | a live proposal | a **ruling** |
| `DECIDED` | settled and unbuilt, wholly or in part | **work** |
| `BUILT` | in the tree, with no unbuilt step left | nothing |
| `GRADUATED` | a stub — the settled body moved to [`../reference/`](../reference), and what stays is the residue | nothing (terminal) |
| `SUPERSEDED` | replaced by another doc, and kept for the argument it still carries | nothing (terminal) |
| `CURRENT` | not a proposal at all — an index, a roadmap, a living record, a runbook | it is kept true (evergreen) |

**The stage lives in top-level frontmatter, `stage: WORD`**, spelled exactly as in the table, where
Vantage and the checks below read it. The prose line, `**Status:** YYYY-MM-DD — …`, stays on its own
line under the H1 and carries the date and the evidence, but **does not repeat the word**: two copies
of a stage are one that goes stale. A doc not yet migrated keeps the legacy form,
`**Status:** WORD, YYYY-MM-DD — …`, which stays valid until the doc is next touched; migrating it moves
the word into `stage:` and out of the line. Either way the line is never buried mid-sentence in a
`·`-separated metadata line, where a check cannot find it. `CURRENT` is the one stage whose line takes
**no** date: an evergreen doc has no moment, and
[`retired-decisions.md`](retired-decisions.md) had already reasoned its way there on its own — *"a
link pass or a typo fix moves the file without changing a single retirement, so the form was always
going to be wrong, and a wrong date on a history file is worse than none."*

**How much already shipped is prose, never the word.** `DECIDED` is where a partly-built design
lives, and its line says which part: *"nine of ten rulings built"* tells a reader more than
`MOSTLY BUILT` did, because it says there is work left **and** how much.

**The tie-breaker, because the ladder would otherwise be wrong twice.** A question the doc has
explicitly **parked** — deferred to a later slice by its own ruling, handed to a successor, or filed
against a sibling's subject — is not a ruling *this* design owes, so it does not hold the doc at
`DESIGN`. The line names the parked question anyway, so a reader can check the judgement rather than
take it.

**`CURRENT` is the one word that is not a lifecycle state**, and it is here because the genre exists:
[`roadmap.md`](roadmap.md), [`BACKLOG.md`](BACKLOG.md), [`retired-decisions.md`](retired-decisions.md),
this file, and the [`runbooks/`](runbooks) all describe no proposal and never settle. It is
deliberately the same word [`../reference/`](../reference) uses, for the same reason.

#### A `BUILT` line says whether anyone has watched it run

This is the distinction the `BUILT`/`SHIPPED` synonym pair was smuggling, and a word choice is the
worst way to carry it — nothing enforces it and no reader can tell which sense was meant. On
2026-09-12 the two macOS designs sat at `BUILT` with **zero runtime observation** (implemented from a
Linux jail, where there is no `sandbox-exec` and no `_yolojail` account) while
[`report-tiers.md`](../reference/report-tiers.md) said `SHIPPED` with its central claim
measured against a control — 30 lines where the same run had printed 278.

So: **every `BUILT` doc's status line carries a `MEASURED:` or `UNMEASURED:` clause** naming what has
and has not been observed running, anywhere in its status block: the status paragraph and those after
it, up to the next heading, blockquote or rule. Under
`stage: BUILT`, the lines read:

```markdown
**Status:** 2026-09-12 — MEASURED: the default report is 30 lines where it was 278.
**Status:** 2026-09-12 — UNMEASURED: every runtime claim; both halves were implemented
from a Linux jail, and a Mac has to run them.
```

`UNMEASURED` is not a defect — it is the honest state of anything host-gated, and it is the label
that lets a graduation assessment sort the queue without reopening each doc.
`\bMEASURED\b` does not match inside `UNMEASURED`, so the check below reads them apart.

#### Frontmatter is a second axis, not a second spelling

`stage:` sits beside `status:` in the frontmatter, and the two stay separate axes. The frontmatter
`status:` answers a different question — *is the argument closed?* — and its
vocabulary is Vantage's own, because Vantage renders it as a chip:
`draft | in-review | accepted | deprecated`. The rule is mechanical:

- **a `SKETCH` → `draft`, always**: nobody is meant to act on it, so its argument is not closed
  whether or not it has filed a question;
- **otherwise, one or more unanswered `💬` → `in-review`**;
- **zero → `accepted`**, or **`deprecated`** for a doc that has been retired or superseded.

`DECIDED`, `BUILT` and `GRADUATED` therefore all sit under `accepted`, and the two axes are allowed
to disagree in exactly one informative way: a `BUILT` line over an `in-review` frontmatter is a doc
that is in the tree **and** still owes one ruling. That pair is legal, and the line must name the
question.

Two defects the census found, worth naming because both are invisible to a reader:

- **Prose appended to the machine-readable value** — `status: accepted # BUILT 2026-09-12; see …`.
  YAML keeps it as part of the value, so the chip silently stops rendering. Prose belongs in the
  status **line**.
- **A genre word in the lifecycle slot** — `STORIES`, `INVENTORY`, `HANDOFF`, `RUNBOOK`, `HISTORY`,
  `INDEX`. The genre goes in the title or in `tags:`; the slot gets a real state.

⚠ **`status: superseded` and `status: current` render no chip** (Vantage's set is the four above), so
`vantage-check` reports `vantage/status-chip-stale` if either appears under `status-chip: true`.

#### Running it

```console
# Shared by 7a and 7b: one frontmatter key's value, and a doc's status block (its **Status:**
# line, or the roadmap's **Reconciled:** line, up to the next heading, blockquote or rule).
$ fm() { awk -v k="$2" 'NR==1 && $0!="---" {exit} NR>1 && $0=="---" {exit}
    NR>1 && index($0, k": ")==1 {sub(/^[^:]*: */, ""); gsub(/"/, ""); print}' "$1"; }
$ block() { awk '/^\*\*(Status|Reconciled):\*\* /{p=1} p && /^(#|>|---$)/{exit} p' "$1"; }
$ words='SKETCH|DESIGN|DECIDED|BUILT|GRADUATED|SUPERSEDED|CURRENT'

# 7a. A doc with a stage names a word in vocabulary, keeps a status line, and does not repeat
#     a stage word in that line's paragraph; a design or plan with no stage keeps the legacy line.
$ for f in docs/*/*.md docs/plans/runbooks/*.md; do
    stage=$(fm "$f" stage); line=$(block "$f" | head -1)
    if [ -n "$stage" ]; then
      echo "$stage" | rg -qx "$words" || echo "stage out of vocabulary: $f: $stage"
      [ -n "$line" ] || echo "no status line: $f"
      block "$f" | awk 'NR>1 && /^$/{exit} 1' | rg -q "^\*\*Status:\*\* ($words)\b|\b$stage\b" \
        && echo "stage word repeated in the status line: $f"
    else case "$f" in docs/design/*|docs/plans/*)
      echo "$line" | rg -q "^\*\*Status:\*\* (($words), \d{4}-\d{2}-\d{2}|CURRENT —)" \
        || echo "no stage, and no legacy status line: $f";; esac
    fi
  done

# 7b. Every BUILT doc, by its stage or by a legacy line, says whether anyone watched it run.
$ for f in docs/*/*.md docs/plans/runbooks/*.md; do
    stage=$(fm "$f" stage)
    { [ "$stage" = BUILT ] || { [ -z "$stage" ] && block "$f" | head -1 | rg -q '^\*\*Status:\*\* BUILT'; }; } \
      && ! block "$f" | rg -q '\b(MEASURED|UNMEASURED)\b' && echo "BUILT, no MEASURED/UNMEASURED: $f"
  done

# 7c. No prose in a frontmatter status value, and the value is in vocabulary.
$ rg -n '^status: ' docs/design docs/plans docs/research \
    | rg -v '^[^:]+:[0-9]+:status: (draft|in-review|accepted|deprecated|current)$'
```

**Run 2026-09-30:** 7a, 7b and 7c report nothing. 7b reaches every BUILT doc through its `stage:`, since no legacy `BUILT` line is
left in the tree.

**7a and 7c are findings; 7b is a finding; the state itself is not checkable by any of them.** A
status line is a **claim**, and this repo's own lesson is that it is the one nobody re-checks —
the 2026-09-09 triage re-run found *~20 status lines that were FALSE against the code, in both
directions*. So the sweep that introduced this check re-verified every doc it moved to `BUILT`
against the tree rather than re-spelling what the line said, and that is the part of the check a
person has to do.

### Check 2 has two known errors, and a convention question under each

Both were found on 2026-09-12 by tallying two docs by hand against what the regex scored them.
Neither is fixable by editing the regex alone, because each rests on a convention the corpus has
not settled.

**It counts 💬 only, so a 🔒 question is invisible.**
[`minimal-disk-footprint.md`](../design/minimal-disk-footprint.md) states in its own header that
[`OQ-DF4`](../design/minimal-disk-footprint.md#112-open-questions) is its **only live question**
and that it is blocked on a measurement rather than undecided. The question is written
`4. 🔒 **…**`, so the regex scores that file **zero** and the doc does not appear in the output at
all. A check whose stated purpose is *every live open question is countable* cannot be blind to the
marker that means **live, but blocked**. — **The convention question:** is 🔒 a state a *question*
can be in, in which case the regex adds it and the corpus-wide count goes up? Or is 🔒 reserved for
roadmap **rows**, in which case a blocked question stays 💬 and names its blocker in prose? Both
readings are in use today, and one has to lose before the regex can be corrected.

**It over-reads where 💬 marks sequencing rather than a question — and the specimen is spent,
which does not settle it.** The worked case was the
broker-CA design (graduated 2026-09-23 into
[`claude-oauth-interposition.md`](../reference/claude-oauth-interposition.md#the-brokers-ca-minting-rotation-and-a-nested-host)),
which scored **5** while holding **3** questions: its Open Questions section had three, and its sequencing list then
used 💬 as a **status marker** on two build-order items that each pointed back at one of the same
three. The regex counts list items beginning with 💬 and could not see that two of them were
references to questions counted already. That doc closed on 2026-09-20 — every question ruled, the
sequencing list compacted — so it now scores zero, and **a sweep of `docs/design` and `docs/plans`
finds no remaining file using 💬 in a sequencing or build-order list.** — **The convention question
is unchanged:** does 💬 mean *an open question lives here* or *this item is not done*? Open
Questions sections use it for the first and nothing currently uses it for the second, so the
corpus agrees by accident rather than by rule. The next author who reaches for it as a status
marker re-creates the over-read, silently, and the count that catches drift goes wrong in the one
direction nobody audits — upward, which reads as more work rather than as a bug.

### One cluster of `vantage-check` errors is data, not rot

**Almost every error the corpus has left is in a single file, and all of them are deliberate.**
[`../research/vantage-check-0.5.9-findings.md`](../research/vantage-check-0.5.9-findings.md) was
**49 of the 103 errors** `uvx vantage-check docs/` reported at the start of the 2026-09-12
close-out, and **48 of the 53** it reported an hour later, after the other clusters had been
worked. Its own number barely moves while the corpus total collapses toward it, so any figure
written here is stale on arrival — **the shape is the durable fact, not the count**: this one file
is essentially the whole residual, and every one of its errors is `ref/unlinked-section` or
`ref/unlinked-oq` fired on an unlinked section number or open-question id.

That document is a defect report **about vantage-check's own reference rules**, written for
upstream, and every one of those bare ids is a **specimen** it quotes to show what the rules do to
them. They are data, not references: linking a specimen destroys the thing being shown. The doc
says so in a note at its top and files the behaviour as its own defect D12. (This paragraph
deliberately does not quote one, for the same reason — quoting a specimen here would move the
cluster rather than describe it.)

A future reader working the corpus toward zero needs this, or they will make a doc's examples wrong
in the course of making its link check green — and the closer the rest of the corpus gets to clean,
the more this one file looks like the only thing left to fix. It is not. (The doc's own header
quotes a figure of its own, taken when it was written; it drifts for the same reason.)

**So run the check with that path excluded BY NAME**, which is the form this section was missing and
the reason it kept failing to stop anyone:

```console
$ uvx vantage-check $(git ls-files '*.md' | grep '^docs/' \
    | grep -v 'docs/research/vantage-check-0.5.9-findings.md')
✓ 197 files checked, nothing to fix
```

⚠ **Prose alone did not work.** On 2026-09-22 an agent widened its check to `docs/research/` for the
first time, saw a 50-error cluster, read it as rot and dispatched four fixers at it — the exact
mistake the paragraph above predicts. Two of them refused on their own, citing this section and the
file's own note; nothing was damaged. But the 2026-09-18 graduation sweep *had* already rewritten a
path inside `D10`'s specimen, so that is twice a sweep has arrived here. A rule that only explains
itself is one every new sweep has to be told; the command above is the half that travels.

**Do not read a change in that file's count as new work.** It was 46 when written, 49 then 48 during
the 2026-09-12 close-out, and 50 under vantage-check 0.6.2 — with the file unchanged throughout. The
number measures the checker, not the document.

⚠ **And the mechanical fix is not merely unnecessary there, it is WRONG.** Re-verified against 0.6.2
on 2026-09-22, and stated without quoting an example for the reason this section already gives: the
checker still reports a **suffixed** id or section number as its unsuffixed stem, and collapses a
**range** of ids onto its first member. So applying the fix at the reported column appends the
dropped suffix *outside* the link it just created, silently turning a citation of one ruling into a
citation of the ruling it supersedes half of. Those are the report's own `D1` and `D7`, still live,
and they are why it is a report rather than a cleanup task.

<a id="oq-dt1"></a>

### Graduating a doc into `../reference/` — the local rules

A **graduation** is the last phase of the `system-doc` genre: a built design's settled body is
rewritten as an evergreen reference in [`../reference/`](../reference), and the design doc is
deleted or cut to a stub. The rules below are this repo's, stricter than the genre's. They came out
of three triage runs (2026-07-03, 2026-09-09, 2026-09-12/13) recorded in `doc-triage.md`, which was
retired on 2026-09-25 once they were moved here; `git log --follow -- docs/plans/doc-triage.md`
recovers the record.

**[`OQ-DT1`](#oq-dt1), ruled 2026-09-13: graduate one at a time, and do not hold a set for its slowest
member.** Each move rewrites content, re-points dozens of references and closes a roadmap row, and
batching them is how an anchor gets missed; the triage record found that failure in both
directions across its three runs. The argument for batching was that it saves one
[`AGENTS.md`](../../AGENTS.md) edit, and that is not worth the missed anchor.

- **Every reference carries `verified_commit:`.** The stamp tells a reader how far to trust the
  rest of the file, so a doc that cannot honestly carry one — behavior nobody has watched run — is
  not a candidate. That is what held the two macOS-user designs until a Mac ran them.
- **A reference carries no live question.** A doc with live questions leaves a **stub at its old
  path** holding them, so the roadmap rows that route them keep resolving, and only the built body
  moves. The precedent is [`composed-file-permissions.md`](../design/composed-file-permissions.md)
  (2026-09-09).
- **Fold into an existing reference rather than mint a second authority.** When a reference already
  owns the subsystem, the design's surviving rulings go into that reference's why-appendix and the
  design is deleted. `lua-transform-removal.md` was folded into
  [`pack-system.md`](../reference/pack-system.md#oq-lt1) and `darwin-image-provenance.md` into
  [`image-staging-vs-baking.md`](../reference/image-staging-vs-baking.md#why-its-this-way). The
  2026-09-09 run found one subsystem described three times, and one fact asserted in four copies of
  which three were wrong.
- **Rule ids are an API.** `OQ-N`, `P1`, `R3` and their kind are cited from Go comments, sibling
  docs and the roadmap. A ruling that survives keeps its original id in the reference's
  `## Why it's this way` appendix, because once the design doc is gone that appendix is the only
  place the id resolves.
- **Go `§N` citations are repaired line by line, never by `sed`.** A reference drops numbered
  sections, so every Go comment citing the design by section number has to be re-pointed. A
  mechanical substitution corrupts citations to *other* docs: during the `report-tiers.md`
  graduation, one section number in `hostapplysurvey.go` meant two different docs on two lines of
  one file, and `configpromote_test.go`'s section-5 citations all belonged to a doc it never names.
  Attribution is per line, by reading.

#### The dangling `§N` citations in Go comments

Measured 2026-09-13 during the `report-tiers.md` graduation: **211 numbered-section citations from
the Go tree dangled into reference docs that have no numbered sections**, across 17 docs, led by
`wire-bridge.md` (51) and `pack-system.md` (49). Every earlier graduation left its own behind, and
`vantage-check` cannot see Go comments, so nothing catches this. The count is a dated measurement;
re-run it before trusting it. The open question — whether Go comments should cite a reference by
*named* section, and whether the rest get the same repair — has no id yet and is listed in
[`roadmap.md`](roadmap.md). The house has both conventions today:
[`self-documenting-cli.md`](../reference/self-documenting-cli.md) keeps numbered sections, and so
does [`security-shim.md`](../reference/security-shim.md) (its component headings).

---

## macOS revival + distribution

The two macos-user designs the 2026-09 sprint built are listed with the rest of that sprint, in
[The 2026-09 sprint](#the-2026-09-sprint--five-designs-all-built) below.

| Doc | What it is | Status |
|---|---|---|
| [roadmap.md](roadmap.md) | **THE forward plan.** One ordered list of the work still to do, each entry a link to the document that owns its state and next step, with a one-clause reason for its place, followed by the work that waits on external conditions and the queue's boundaries. | **Start here for "what is left?"** |
| [../design/configurable-workspace-root.md](../design/configurable-workspace-root.md) | Why `macos-user`'s workspace root cannot simply be made configurable: the profile's read policy is `(allow default)` with a deny on `/Users`, so sibling projects are hidden by WHERE they sit and a root outside `/Users` silently loses that. Proposes a second read-deny derived from the configured root — which also closes a hole under today's default — plus the whitelist that closes four lexical bypasses in the neutral-ground check. | **Design** — the check now refuses the case-changed and firmlinked spellings of a home; the whitelist and the derived deny are not built. Four questions, prioritized in [roadmap.md](roadmap.md) |
| [setup-support-gaps.md](setup-support-gaps.md) | **The per-setup gap backlog**, from a cell-by-cell audit of four setups (`podman`/Linux, `podman`/macOS, `container`/macOS, `macos-user`) crossed with every closed vocabulary yolo has — config keys, pack kinds, loopholes — plus the user-facing capabilities that are not keys. Every `impossible` and `ruled-wontfix` cell was then handed to an adversarial refuter, and **nearly all of them fell**: what looked like a wall of limitations is a ranked backlog of mostly-small gaps, each row naming an existing seam to build on. Also carries the silent-drop table (a key accepted that does nothing, with no notice) and the unmeasured cells grouped by the instrument that settles them. | **Start here for "what is missing, per setup?"** — the user-facing half is [USER_GUIDE § What works in each setup](../../userguide/reference/settings-per-setup.md#what-works-in-each-setup) |
| [further-roadmap-ideas.md](further-roadmap-ideas.md) | **Candidates, not a queue.** Seven proposals from the 2026-08-23 doc audit — five build, two rule-first — plus **three rows that should LEAVE the roadmap** ([§4](further-roadmap-ideas.md#4-two-rows-already-on-the-roadmap-that-i-would-drop), §[4a](further-roadmap-ideas.md#4a-a-third-row-i-would-drop-added-after-the-deeper-pass)) and §[4b](further-roadmap-ideas.md#4b-what-the-verification-pass-changed-about-this-file), which records what an adversarial re-check did to the file itself. Nothing here is committed. | **Read when the queue is empty** |
| [handoff-macos-user-open-threads.md](handoff-macos-user-open-threads.md) | What the FIRST end-to-end hardware run of the macos-user runbook left open (2026-09-12). All ten items now measured and three defects fixed that day, so this is not a "does it work" doc — it is one confirmed product defect (`lsp_servers` installs nothing here, and the ruling on whether to wire it or restore the warning is the maintainer's), the twin suite poisoning itself in test order on a persistent Mac, three items with no automated twin, and an unfiled `--dry-run` secret-disclosure question. | **Handoff** — one ruling needed; the rest is work nobody has done. |
| [handoff-guest-notch-macos.md](handoff-guest-notch-macos.md) | The `guest` notch (env-manager Phase 7) plus every other Mac-gated item, in one place so one trip to a Mac can close all of it. Its old lead — the G3 bug, macos-user rendering ZERO pack surfaces — was **fixed on 2026-08-12** (`2bb792ff`); [`OQ-GN2`](handoff-guest-notch-macos.md#9-open-questions) closed 2026-09-11, so all four of its questions are answered. | **DECIDED** — Phase 7 not built; host/Mac-gated; no question open. |
| [macos-revival-and-distribution-plan.md](macos-revival-and-distribution-plan.md) | The macOS-backend revival + source-distribution roadmap (Tracks J/D/M). | **DECIDED, in progress** (restamped 2026-09-24): D1 was retired, D2 reverted and D3 superseded. Track M's drift closed 2026-09-10. Track L part 1's host half is done (every admitted host daemon starts on `macos-user` since 2026-09-17); its jail half waits on [`OQ-DP8`](../design/declaration-parity.md#OQ-DP8)/[`OQ-DP9`](../design/declaration-parity.md#OQ-DP9). D4 needs only the Mac download proof. |
| [handoff-cachix-cache.md](handoff-cachix-cache.md) | Procedure to publish the prebuilt OCI image to a Cachix binary cache (= revival plan **D4**). | **Working** (re-verified 2026-09-24): every release since `v0.8.0` has pushed its image closures and `.#imageCopier` on both arches. The push is **tag-triggered only**, and since the image stopped containing yolo a release's cached image stays current until `flake.nix`, `flake.lock` or a `packages:` list changes. Only the **Mac download proof** remains (hardware-gated). |
| [../research/macos-layer-reusing-image-delivery.md](../research/macos-layer-reusing-image-delivery.md) | Whether podman on macOS and Apple Container can get the per-layer reuse `skopeo copy` gives podman on Linux. A **delta archive** through the loaders yolo already calls leaves out every layer the store holds: measured on Linux, a 27.8 MB archive replaces the 3.45 GB one. Apple Container still rebuilds its ext4 snapshot for every new image, and no option helps a first load. | **Ruled; delta archive built, first-load work not started** — [`OQ-LR1`](../research/macos-layer-reusing-image-delivery.md#OQ-LR1)–[`OQ-LR3`](../research/macos-layer-reusing-image-delivery.md#OQ-LR3) ruled 2026-09-24; the Mac-side claims are SOURCED, not measured. |

## Post-Go-port backlog

The archived `go-port-post-transition.md` (git history) queued work for after the
Python→Go cutover. Its distribution section landed, and the one item this section tracked
has since graduated:

| Doc | What it is | Status |
|---|---|---|
| [../reference/cli-color.md](../reference/cli-color.md) | How a yolo command decides whether its output carries ANSI: messages written once in rich markup, one shared renderer (`internal/richtext`), one color gate (`tty.Color`) that honors `NO_COLOR`, and one ioctl terminal probe, held by the emitter inventory and the no-private-probe tests. | **GRADUATED 2026-09-27** from `cli-color-audit.md` to [../reference/cli-color.md](../reference/cli-color.md), now the authority. One terminal observation is recorded: in a nested jail on 2026-07-22, a pty run of `yolo check` colored and a piped run did not; the tests drive the pipe, `/dev/null` and `NO_COLOR` cases. The terminal-half gap for the provisioning and ⚡ lines stays with [cli-visual-polish.md](cli-visual-polish.md#implementation-decisions--no_color-2026-09-26). The plan was deleted. |

## Test-suite speed

| Doc | What it is | Status |
|---|---|---|
| [integration-parallelism.md](integration-parallelism.md) | Bounded `t.Parallel()` for the container suite, after per-test GlobalStorage isolation unsticks the shared `last-load` sentinel race. | **Parked** — CI is free + the fast local loop skips these tests; the launch-merges (done 2026-07-20) were the cheaper win. Pick up only if the full local `just test` becomes a friction. |

## Other

| Doc | What it is | Status |
|---|---|---|
| [agent-settings-composition.md](agent-settings-composition.md) | Design of record: layered regeneration of any generated config (agent settings + MCP/LSP/mise/identity) + a Lua transform (format-agnostic, user-scope-only, no source mutation). | **DECIDED 2026-07-20, and overtaken in shape** (postscript 2026-09-24): surfaces are pack declarations rendered by `entrypoint.ConfigurePackSurfaces`, only core's `mise/config` is built in, MCP/LSP reach agents through each pack's derive rather than as surfaces, git identity is host-composed and mounted `:ro`, and the additive path is the `config-list` kind rather than [§4](agent-settings-composition.md#4-layers-and-scope)'s per-keypath `append`. No open questions. Its one unbuilt piece, [§4](agent-settings-composition.md#4-layers-and-scope)'s `workspace` layer, is filed in [`pack-system.md`](../reference/pack-system.md#config-surfaces-and-the-compose-engine) since 2026-10-01, where [`OQ-PK1`](../reference/pack-system.md#oq-pk1) decides whether anything fills it. |
| [cache-relocation.md](cache-relocation.md) | User-scope-only `cache_relocations` so a large cold cache subdir (`huggingface`, 185 GiB) can live on other storage, mounted read-write nested inside `.cache`. Read straight from the user config — never the merged config or the jail-writable snapshot. Also unblinds `prune`/`purge` and fixes the hint that recommends the symlink trick that dangles in-jail. | **Implemented 2026-07-21** — work items 1–10 landed and verified end to end in a nested jail; `yolo cache relocate` (item 11) deferred; one host-gated acceptance step (a real cross-filesystem move) outstanding. |
| `antigravity-agy-support.md` (retired 2026-09-27; `git log -- docs/plans/antigravity-agy-support.md`) | Support Google Antigravity CLI (`agy`) as a native agent inside `yolo-jail`. | **RETIRED 2026-09-27**: agy is [`packs/agy/pack.json`](../../packs/agy/pack.json), and the agent registry the plan targeted is deleted. Its one uncarried trap (agy's workspace state is all of `~/.gemini`, not `antigravity-cli/`) is a warning in [../reference/agent-credentials.md](../reference/agent-credentials.md#agys-paths-under-gemini). The plan was deleted. |
| [agent-config-packs.md](agent-config-packs.md) | Proposal: share agent environment config (skills, AGENTS.md fragments, settings) between people by `(repo, path, branch)` with no PR — user-scope `packs`, host-side blobless fetch, content-addressed trees, pin/rollback, cross-agent projection. Includes the scope verdict (in yolo-jail, one extractable package) and the landscape research it rests on. | **DESIGN, largely OVERTAKEN** — the `packs` key, host-side fetch and the lockfile shipped (the origin gate shipped and was later deleted by [`OQ-TP9`](../design/trust-paths.md#decision-ledger)), so read this for the *landscape research* and the scope verdict rather than as a plan. One question is open, [`OQ-ACP4`](agent-config-packs.md#OQ-ACP4); [`OQ-ACP1`](agent-config-packs.md#-oq-acp1--what-happens-when-two-people-attach-to-the-same-jail-with-different-pack-sets) is answered by [`OQ-PK2`](../reference/pack-system.md#oq-pk2), and [`OQ-ACP3`](agent-config-packs.md#-oq-acp3--whether-the-prism-should-become-a-standalone-tool-that-also-manages-host-configs) is answered by reference to [`host-render-target.md`](../design/host-render-target.md). |
| [../reference/pack-system.md](../reference/pack-system.md) | The pack system, whole: the `contributes[]` manifest, the kinds + footprints + conflict rules, the one-writer rule, the compose engine + `derive`, selection/fetch/lock. | **Shipped** — this is the current design of record for authoring/debugging/changing a pack (the reform that produced it is complete and its plan is retired). |
| [environment-manager-plan.md](environment-manager-plan.md) | Sequences [../design/yolo-as-environment-manager.md](../design/yolo-as-environment-manager.md) into buildable phases: the render-path collapse (= BACKLOG Stage G), the `confinement` dial, `apply`/`describe`/`--at`/`--sealed`, dep-provisioning, the `guest` notch, self-describing briefings. | **Mostly BUILT** — Phases 0–6, 8 and 9 have shipped, 5.3 `yolo config promote` and 6.4's confirm-gated install included (both 2026-09-12; the plan's table says so since 2026-09-24). **Phase 7 (the `guest` notch) is the only unbuilt phase** and is host-gated, not design-blocked. |
| [notch-convergence.md](notch-convergence.md) | The host notch, the container jail and macos-user reach one declaration through different code for flags, pack selection, providers, env order and render. The plan gives each concern one code path, with the notch as an input, and first makes every credential-serving loopback service authenticate its caller, since `network.mode: "host"` and macos-user already share the host's loopback. | **DESIGN, 2026-09-28** — owed: two rulings, then work. Most of the ordered build list is built, the bridge's caller auth and the host `-p` grammar included. Item 16 waits on [`OQ-NC12`](notch-convergence.md#OQ-NC12) and [`OQ-NC13`](notch-convergence.md#OQ-NC13), filed 2026-09-30 ([NC-D68](notch-convergence.md#NC-D68)), and every other unbuilt item on another doc's question; all are prioritized in [roadmap.md](roadmap.md). |
| [../reference/perf-logging.md](../reference/perf-logging.md) | Performance logging across the yolo lifecycle behind `--timing` and the global `--verbose`; `internal/perf` spans the launch, the child window (with podman-cleanup attribution), and both shutdown arms, because "who holds my shell prompt 30s after the agent exits?" had no answerable spelling. | **BUILT 2026-09-06** (`f9eee104`..`03b18afb`), and the reference is the authority: the build plan was retired 2026-09-26 (`git log -- docs/plans/perf-logging.md`). [The motivating symptom](../reference/perf-logging.md#the-motivating-symptom-narrowed-to-one-arm-then-attributed) is narrowed to one arm, then attributed. |
| [install-capture.md](install-capture.md) | Capture-and-repackage for the installer class ([`program-delivery.md` §6.3](../design/program-delivery.md#63-installers-that-just-do-whatever-capture-the-install-then-treat-the-capture-as-the-package)): run an installer that writes wherever it likes once in a throwaway capture jail, record what it wrote, and treat the capture as the package. | **DECIDED, 2026-09-26** — owed: slice 6's hand-off [H4](install-capture.md#hand-offs--what-is-not-wired-and-the-exact-line-that-wires-it), a capture store a `macos-user` launch can read. H4 needs a ruling first, because every way to wire it changes what that backend's sandbox may read, so no launch there materializes a capture yet. Hand-off H2, the relocation rewrite, landed 2026-09-26 and is measured on Linux only. Slices 1–5 and 7 are built and were measured in a nested jail on 2026-09-04; slice 6 built its recording half and, with H2, its rewrite. One uid-mapping confirmation on a real rootless host is unrecorded. Not a graduation candidate until H4 lands or is retired. |
| [../design/forked-programs-as-packs.md](../design/forked-programs-as-packs.md) · [plan](../design/forked-programs-as-packs-plan.md) | A fork satisfies neither shipped delivery route, so distributing one means installing it by hand everywhere. Adds a pinned source plus a build recipe, built once in a throwaway capture jail and served from the existing capture store. The hard part is relocation, not the build. | **DECIDED, 2026-09-30** — the six original questions were ruled 2026-09-22, and the three they opened ([`OQ-FP7`](../design/forked-programs-as-packs.md#OQ-FP7)–FP9) were decided as implementation choices on 2026-09-30. The plan was promoted against the tree the same day, with the choices it made recorded as [FP-D4](../design/forked-programs-as-packs.md#FP-D4)–[FP-D9](../design/forked-programs-as-packs.md#FP-D9). Steps 1–6 of the build, the jail notch, landed 2026-09-30; step 7, the host notch, stopped at its measurement ([Step 7 needs](../design/forked-programs-as-packs-plan.md#step-7-needs)), and its measurement is prioritized in [roadmap.md](roadmap.md). |
| [../design/base-home-legacy-state.md](../design/base-home-legacy-state.md) | Podman mounts one machine-wide base home, `<state>/home`, read-only at `/home/agent` in every jail, and sharing it is the defect: a jail needs nothing in it, yet one workspace's pack dirs, `host_files` links and old bytes show up in every jail. Each podman jail instead gets its own read-only skeleton (mountpoints and redirect links only) built from its SELECTED packs; `<state>/home` stays as the machine-wide store for the shared dirs and the Claude login seed, and `seedAgentDir` is deleted. Legacy bytes are left in place, unmounted and unread — nothing is moved. | **DECIDED, 2026-09-25** — every question ruled. [§8](../design/base-home-legacy-state.md#8-build-order-and-done-conditions) steps 1–5 and the review follow-ups are built, and the skeleton was MEASURED rootless in CI on both arches; [`OQ-BH15`](../design/base-home-legacy-state.md#OQ-BH15) (`host_files`' surface reservation narrows to the selected packs) and [`OQ-BH16`](../design/base-home-legacy-state.md#OQ-BH16) (a launch removes its own skeleton at exit) are built, and the Apple Container seed fix still owes a Mac run. The old [`OQ-BH1`](../design/base-home-legacy-state.md#10-decision-ledger)–BH8 are superseded in its ledger. |
| [../reference/agent-program-runtimes.md](../reference/agent-program-runtimes.md) | The Node that runs an npm-delivered agent CLI is chosen by the *workspace's* `mise.toml`, because the generated launcher execs a `#!/usr/bin/env node` symlink and mise's shims precede `/bin` — so a repo pinning Node 20 makes pi fail at `import` with an error naming a module export and neither the pack nor the pin. Adds a declared Node floor to a `program` contribution and resolves one interpreter into the generated launcher, leaving the workspace pin authoritative for everything except that one process. `opencode-ai`'s native ELF is why the pin must be opt-in per program. | **GRADUATED 2026-09-25** — the settled body moved to [../reference/agent-program-runtimes.md](../reference/agent-program-runtimes.md), now the authority: the `node_floor` declaration, [the resolution order](../reference/agent-program-runtimes.md#resolution) and why resolving is split from installing, the launcher's exec prefix, [the refusal](../reference/agent-program-runtimes.md#the-refusal), and the rulings [OQ-AR1](../reference/agent-program-runtimes.md#oq-ar1)–[OQ-AR4](../reference/agent-program-runtimes.md#oq-ar4) and [AR-L2](../reference/agent-program-runtimes.md#ar-l2). Its two integration tests passed 2026-09-30 in a nested jail; no real workload is measured. The three questions a stub then held, decided and built 2026-09-30, **graduated 2026-10-01** into the same appendix: [OQ-AR5](../reference/agent-program-runtimes.md#oq-ar5)–[OQ-AR7](../reference/agent-program-runtimes.md#oq-ar7) and [AR-L3](../reference/agent-program-runtimes.md#ar-l3)–[AR-L7](../reference/agent-program-runtimes.md#ar-l7). The file at [../design/agent-program-runtimes.md](../design/agent-program-runtimes.md) is a pointer that owes nothing. The companion plan was deleted. |
| [../reference/host-agent-environment.md](../reference/host-agent-environment.md#the-launch-path-and-which-copy-of-a-program-runs) | What `yolo host` reads for PATH. By the maintainer's ruling [HE-DIR1](../reference/host-agent-environment.md#he-dir1), yolo's checks at the host read the PATH yolo was started with, plus a user-scope `host_path` list, and look in no other folder, so a launcher with a bare PATH can get a different answer from a terminal, and a miss prints one line naming the PATH searched and the fix. What stays fixed is what yolo provides: a selected pack's agent runs from the [host agent floor](../design/host-tool-provisioning.md#defined-terms) ([HP-DIR4](../design/host-tool-provisioning.md#HP-DIR4)), and the agent is handed that PATH with the floor's `bin/` last. | **GRADUATED 2026-10-01** from `host-launch-environment.md` into [`host-agent-environment.md`'s launch PATH section](../reference/host-agent-environment.md#the-launch-path-and-which-copy-of-a-program-runs), now the authority, with [HE-DIR1](../reference/host-agent-environment.md#he-dir1), [`OQ-HE0`](../reference/host-agent-environment.md#oq-he0), [`OQ-HE10`](../reference/host-agent-environment.md#oq-he10) ruled (c), [`OQ-HE11`](../reference/host-agent-environment.md#oq-he11) ruled (a) and the retired [`OQ-HE1`](../reference/host-agent-environment.md#oq-he1) to [`OQ-HE9`](../reference/host-agent-environment.md#oq-he9) in its why-appendix. The stub at [../design/host-launch-environment.md](../design/host-launch-environment.md) keeps the build record, `HE-D1` to `HE-D10`, among them [HE-D10](../design/host-launch-environment.md#he-d10), the reversible answer to [`OQ-HE6`](../reference/host-agent-environment.md#oq-he6) (API keys in the shell). UNMEASURED: no launch from a real bare-PATH launcher since the build. |
| [../design/context-mounts.md](../design/context-mounts.md) | A read-write form of `mounts`, a `YOLO_CONTEXT_DIR` on every backend, and delivering context dirs on macos-user by root-owned link plus Seatbelt rules. Leaves where an rw mount may be declared to the workspace-config trust doc. | **DESIGN** 2026-09-25 — [§4](../design/context-mounts.md#4-staging-and-the-tests-that-pin-each-piece) steps 1–3 BUILT 2026-09-30 ([CX-D7](../design/context-mounts.md#CX-D7)–[CX-D16](../design/context-mounts.md#CX-D16)); steps 4–5, the macos-user delivery, need a Mac and are not built. One question open, [`OQ-CX7`](../design/context-mounts.md#OQ-CX7) |
| [../design/workspace-config-trust.md](../design/workspace-config-trust.md) | Where a writable host grant (first case: an rw context mount) may be declared. The local file stops a repo author but not the in-jail agent, and a trust record stops the agent but prompts on cloned repos, so: refused in the committed file, trust-gated in the local file, free in user config. Pairs with [`OQ-AS3`](../research/agent-safehouse.md#OQ-AS3); owns [context-mounts.md §2.2](../design/context-mounts.md#22-where-an-rw-mount-may-be-declared-deferred)'s trust predicate. | **DESIGN** 2026-09-25 — iceboxed candidate, not ruled; [`OQ-WT1`](../design/workspace-config-trust.md#OQ-WT1) (whether to build it) and [`OQ-WT5`](../design/workspace-config-trust.md#OQ-WT5) (how a grant is approved) open, the other seven decided as implementation choices 2026-09-30 |
| [../reference/docs-website.md](../reference/docs-website.md) | The user guide as a site, copied from Vantage's setup: a `userguide/` tree in Vantage's four-part organization, `scripts/build-site.sh` running a pinned `vantage build`, a static-assets Worker, and Cloudflare Workers Builds as the only deployer. Adds one rule of its own: the guide is a closed tree. | **GRADUATED 2026-10-01** from `docs-website.md` to [../reference/docs-website.md](../reference/docs-website.md), now the authority, with [`OQ-DW1`](../reference/docs-website.md#oq-dw1), [`OQ-DW2`](../reference/docs-website.md#oq-dw2), [DW-D1](../reference/docs-website.md#dw-d1) and [DW-D2](../reference/docs-website.md#dw-d2) in its why-appendix. The stub at [../design/docs-website.md](../design/docs-website.md) holds one question, [`OQ-DW3`](../design/docs-website.md#OQ-DW3), filed that day: whether runtime messages name the site's URL instead of a repository path. The sketch is a pointer. What a page's address on the live site does is measured beside that question, not here. |
| [../design/host-tool-provisioning.md](../design/host-tool-provisioning.md) · [plan](../design/host-tool-provisioning-plan.md) | A host agent floor: the selected packs' program binaries, installed with no prompt (selecting the pack is the consent) into a yolo-owned host prefix that no jail mounts, kept current by the jail's launcher shape, and run from there by path, so a `yolo host` launch needs no particular ambient PATH. Answers the maintainer's review comment on [tool managers at the host](../reference/host-agent-environment.md#the-launch-path-and-which-copy-of-a-program-runs). | **DESIGN, 2026-09-25** — nothing built; every question ruled 2026-09-29, [`OQ-HP1`](../design/host-tool-provisioning.md#OQ-HP1)–[`OQ-HP7`](../design/host-tool-provisioning.md#OQ-HP7) and last [HP-DIR4](../design/host-tool-provisioning.md#HP-DIR4), with the consent step withdrawn by [HP-D3](../design/host-tool-provisioning.md#HP-D3); the plan is a SKETCH. |
| [../design/agent-directory-map.md](../design/agent-directory-map.md) · [plan](../design/agent-directory-map-plan.md) | Each agent pack declares its agent's directory whole: every path is state, cache or yours, and yolo's own writes (composed files, links it lays, names it deletes) are derived from the declarations that already cause them. One read-only evaluator, run where the agent runs, names broken links, shadowing files and unexplained entries, and answers "can I wipe it?". It never deletes or refuses anything. Pi first, with a measured map, then the other six agents. | **DESIGN, 2026-09-28** — the map is not built; pieces that rest on none of its questions are, as the design's status line and Appendix B record; [`OQ-AM1`](../design/agent-directory-map.md#OQ-AM1)–[`OQ-AM10`](../design/agent-directory-map.md#OQ-AM10) open, prioritized in [roadmap.md](roadmap.md), and the plan is a SKETCH. |
| [../design/pi-git-extension-caching.md](../design/pi-git-extension-caching.md) · [plan](../design/pi-git-extension-caching-plan.md) | pi's git extensions shared across jails as immutable per-commit trees in a machine store, each workspace pointing at the commit its own config resolves to and pi loading it as a local package it never updates. Replaces the first build's one shared checkout per repository, withdrawn by the no-leakage and no-winner rulings. | **DESIGN** 2026-09-26 — the redesign (git and npm, [`OQ-5`](../design/pi-git-extension-caching.md#OQ-5)) is built, and was green, on the local branch `held/pi-extension-store` (`d44cb88b9`; the `wip/` branches are deleted), held off main by [`OQ-6`](../design/pi-git-extension-caching.md#OQ-6) (the post-merge rewrite hook against [`OQ-LT2`](../reference/pack-system.md#oq-lt2)); `c402dd43`'s shared hook is reverted, its `due_on_change` trigger kept. |
| [../design/pack-pi-resources.md](../design/pack-pi-resources.md) · [plan](../design/pack-pi-resources-plan.md) | How a content pack delivers pi extensions, themes and prompts without naming pi's paths or listing files: pi's pack declares a slot outside pi's auto-discovery, and core registers each landed tree as a local pi package, appended beside the user's `packages`. Opens with a table of what pi loads, from where, and in what form. | **DESIGN** 2026-09-25 — nothing built; [`OQ-PR1`](../design/pack-pi-resources.md#OQ-PR1) (adopt the route) open. |
| [../design/workspace-mcp-sources.md](../design/workspace-mcp-sources.md) | Which workspace MCP files reach an in-jail agent, and the one thing left open. States the position — a repo's or the user's own MCP config reaching the agent is **desired** — records the measured per-agent project-scope file sets (Copilot reads three, Claude a fourth), and keeps the record of the `.vscode/mcp.json` shadow removed 2026-09-22. | **DESIGN 2026-09-22** — one question open ([`OQ-WM1`](../design/workspace-mcp-sources.md#OQ-WM1): what yolo says when a workspace file and `mcp_servers` name one server), prioritized in [roadmap.md](roadmap.md). The removal itself is BUILT; the anti-re-proposal record is [`retired-decisions.md`](retired-decisions.md). |
| [../reference/pack-system.md](../reference/pack-system.md#adding-entries-to-an-array-config-list) | Why a personal pack must copy another pack's whole config array to add one entry (arrays replace under JSON Merge Patch), and a narrow explicit `config-list` contribution that appends de-duplicated entries after ordinary overlays without changing merge-patch semantics anywhere else. | **GRADUATED 2026-09-25** — the design and its build plan were folded into [../reference/pack-system.md](../reference/pack-system.md#adding-entries-to-an-array-config-list), which is now the authority: the fold, per-entry capture, [what it does not do](../reference/pack-system.md#config-list-limits), [OQ-AL1](../reference/pack-system.md#oq-al1) and [OQ-AL2](../reference/pack-system.md#oq-al2). MEASURED in CI on rootless podman (run 36093253387); UNMEASURED against a real `pi install`. Both files were deleted. |
| [../reference/pack-system.md](../reference/pack-system.md#briefing) | Why a pack's root `AGENTS.md` shipped to the wrong readers: it is also the repository's own agent instructions, declaring one narrow briefing silently switched its delivery off, and a manifest could not spell broadcast at all. Shipped prose now lives in a `briefing/` directory, silence means broadcast in a manifest, and every declaration is additive and per-file. | **GRADUATED 2026-09-23** to [../reference/pack-system.md](../reference/pack-system.md#briefing), which is now the authority — the `briefing/` convention, P1–P6 (briefing defaults), per-file governance, the lint listing, the local-pack move, and [OQ-PB1](../reference/pack-system.md#oq-pb1)–[OQ-PB5](../reference/pack-system.md#oq-pb5). The design and its companion sketch were deleted in the same commit; the prior-art appendix was not carried. |
| [../reference/pack-system.md](../reference/pack-system.md#retiring-a-dropped-packs-host-output) | What `yolo host apply` does with the output of a pack dropped from `packs`: each kind's own retire pass cannot see a pack that left, so a separate pass, keyed on the packs the config names rather than the ones that resolved, archives a dropped pack's briefing, skills and files and removes its `config-overlay` keys, behind one prompt that briefings skip. | **GRADUATED 2026-09-26** from `host-pack-drop-cleanup.md` to [../reference/pack-system.md](../reference/pack-system.md#retiring-a-dropped-packs-host-output), now the authority, with its rulings as [R1–R4 (pack drop)](../reference/pack-system.md#pd-r1) and [the `retired:` provenance label](../reference/pack-system.md#the-retired-provenance-label). UNMEASURED: no real `yolo host apply` over a dropped pack is recorded; unit tests drive it into throwaway homes. The plan was deleted. |
| [../reference/composed-file-permissions.md](../reference/composed-file-permissions.md#how-a-host_files-entry-becomes-a-surface) | How a `host_files` entry becomes a surface: every file entry lowers to one surface owned by the synthetic agent `user` and named by the entry's slug, rendered through the same writers as an agent's settings; the codec comes from the extension unless the entry names one, a directory entry is a recursive copy, and refresh follows the mode. | **GRADUATED 2026-09-26** from `host-file-staging.md` to [../reference/composed-file-permissions.md](../reference/composed-file-permissions.md#how-a-host_files-entry-becomes-a-surface), now the authority. The plan was deleted. |
| [../reference/image-retention.md](../reference/image-retention.md) | **Image and GC-root retention, as built.** The image reaper asks liveness and gets it from the runtime; the nix GC-root reaper holds the root of any image a container is running on, also asked of the runtime, and ages out every other root after a week. Image retention is one current-image pointer per workspace — no global count, no undo buffer — and an unreachable authority declines the sweep. | **GRADUATED 2026-09-18** from `docs/design/the-load-sentinel-is-not-a-liveness-oracle.md`, and that redirect stub is **deleted (2026-09-22)** — every prose citation it existed to protect now points at [`image-retention.md#why-its-this-way`](../reference/image-retention.md#why-its-this-way) or at the reference's own [load-sentinel section](../reference/image-retention.md#the-load-sentinel-and-what-it-may-be-cited-for). ⚠ **Two of them hid from every `<basename>\.md` scan by line-wrapping mid-path across two comment lines** in `internal/image/` — a scan that reports zero inbound for a path is not evidence until it has been run against the wrapped spellings too. `P1`–`P3` and every [OQ-LS](../reference/image-retention.md#why-its-this-way) ruling resolve in the reference's why-appendix. The `imageroots.go`/`autoload.go` comment contradiction the design recorded is **fixed**. |
| [../reference/image-staging-vs-baking.md](../reference/image-staging-vs-baking.md#delivering-into-the-runtime) | Layer-aware delivery (C9): nix2container + `skopeo copy` replaced `streamLayeredImage` + `podman load`, so a rebuild ships the layers that changed instead of all 3.47 GB. Measured: 81.0s of a 96.1s load was the stream; the customisation layer is 0.78% of the image. | **GRADUATED 2026-09-23** to [../reference/image-staging-vs-baking.md](../reference/image-staging-vs-baking.md#delivering-into-the-runtime), which is now the authority — the layer plan, the copy and its namespace, archive destinations, the image-copy lock, and its rulings — [`OQ-LI1`](../reference/image-staging-vs-baking.md#why-its-this-way) through LI7, C9 and R8 — at its why-appendix (R3 folded into [`OQ-LI5`](../reference/image-staging-vs-baking.md#why-its-this-way)). The design was deleted; its unbuilt guards and unmeasured macOS archive paths are in [roadmap.md](roadmap.md). |

## Provider & profile machinery

Docs written 2026-08-29 → 2026-09-09 around one question: how does a provider (a z.ai key, a
Bedrock role) reach every selected agent. One parent design (graduated 2026-09-23; its counter-design was retired with it), the
first consumer, two review docs that measured what actually shipped — and, added 2026-09-09, the
docs in this family that are still live: Bedrock's native arm and the switching problem it
split off, joined on 2026-09-25 by the four docs the Bedrock design split into. The two implementation plans that carried the build order are **deleted** (2026-09-09):
both shipped whole on 2026-09-02, and a plan that outlives its work is a trap for the next reader,
so their surviving traps went into [providers.md](../reference/providers.md) as warnings and the
rest is in `git log`. The live questions are routed in [roadmap.md](roadmap.md) by id; this
section does not repeat its row numbers. Rows re-checked against the refreshed docs 2026-09-24.

| Doc | What it is | Status |
|---|---|---|
| [../reference/providers.md](../reference/providers.md#profiles-and-options) | The parent design. `kind: "profile"` began as a named variant of one pack's own declarations and is now `{name, provider}`, with every other effect a contribution gated on a profile name; `providers` stays a config key with a stricter schema. | **GRADUATED 2026-09-23** to [../reference/providers.md](../reference/providers.md#profiles-and-options), which is now the authority; the design's bare `OQ-N` ids and `D8` resolve at [the profile-variant rulings](../reference/providers.md#the-profile-variant-rulings). The design was deleted, and so was the counter-design it answered (`pack-profiles.md`, deprecated, nothing built from it; its credential architecture was already in providers.md). |
| [../reference/zai-plumbing.md](../reference/zai-plumbing.md) | The first real consumer: both routes to "one provider, every agent" — name the protocol and fill the values ([§3](../reference/zai-plumbing.md#the-resolution-table)), or ship a layered zai pack the user drops a key into ([§4](../reference/zai-plumbing.md#what-a-user-actually-does)) — and the endpoint-by-protocol resolution behind `-p zai` ([§5](../reference/zai-plumbing.md#what-a-user-actually-does)). | **DECIDED 2026-09-01** (ledger, [§8](../reference/zai-plumbing.md#why-its-this-way)); **and now BUILT** (corrected 2026-09-09): `packs/zai` ships the provider + profile, `internal/cli/run/providerpreflight.go` is in the tree, and pi/opencode selection landed in `6d1d7c54`. [§5](../reference/zai-plumbing.md#what-a-user-actually-does)'s resolution table now speaks the canonical vocabulary (`db6aff96`), and its follow-up note hands the codex dialect question to [providers.md §3](../reference/providers.md). |
| [providers.md](../reference/providers.md) (was design/docs/reference/providers.md, distilled 2026-09-03) | The defect report on that shipped machinery: **eleven** defects, D1–D11, of which three share one cause — a value validated against a set yolo owns and handed verbatim to consumers that own different sets. D1 is the only one that puts a wrong value in a file an agent reads (`wire_api`, the protocol field, was four borrowed spellings naming three protocols). D9 outgrew the doc to [`trust-paths.md`](../design/trust-paths.md)'s census. | **DECIDED 2026-09-01** (ledger, [§11](../reference/providers.md)) and **built** — D1's three-name vocabulary + per-agent dialect maps (`0f04632d`), D2's composer refusal (`5d8bd1fe`), D4 (`868b610f`), D5's `--timing` split (`886a9191`) and D6's census (`67f87f36`) are in the tree, `integration/providers_test.go` pins D1 (`cee9c1fc`), and D10/D11 — filed 2026-09-02 while paying §[3.0a](../reference/providers.md)'s verification debt — landed the same day (`7fa624ba`). |
| [providers.md](../reference/providers.md) (was design/docs/reference/providers.md, distilled into the same reference) | Splits the knot the two above left tangled: **catalog** (the agent's directory of providers it *could* use) and **selection** (which one it *does* use) are two features, and one table drives both — measured in a live jail, `-p zai` changes the behaviour of one agent in four. Also dissolves disable-without-deleting. | **DECIDED 2026-09-01** (ledger, [§10](../reference/providers.md); a tenth question was withdrawn as never having been a design question) and **built** 2026-09-02 — [§3](../reference/providers.md)'s empty pi row was filled from source (`070a3574`), which is what unblocked pi's and opencode's selection keys (`6d1d7c54`), and selection landed for all four agents. [§8](../reference/providers.md)'s own order has one residue: step 4, option C's explicit disable, is still unbuilt. |
| [../design/bedrock-plumbing.md](../design/bedrock-plumbing.md) | How each shipped agent reaches Bedrock's one shipped endpoint family, `bedrock-runtime`: through its own native Bedrock client (codex, opencode, pi, and claude for Anthropic models) or through yolo's wire bridge (claude for every other model, copilot, oh-omp), what a user types to get either, and the shape of the one `bedrock` provider. [Where the split ended up](../design/bedrock-plumbing.md#where-the-split-ended-up) says it in plain words. Split on 2026-09-25; [its table](../design/bedrock-plumbing.md#where-the-rest-went--moved-on-2026-09-25) maps every moved id to its new home. | **DESIGN, 2026-09-25, native arm unbuilt.** `bedrock-runtime` only ([DIR-BR3](../design/bedrock-plumbing.md#DIR-BR3)); [`OQ-BR5`](../design/bedrock-plumbing.md#OQ-BR5) and [`OQ-BR6`](../design/bedrock-plumbing.md#OQ-BR6) ruled 2026-09-25; [`OQ-BR6`](../design/bedrock-plumbing.md#OQ-BR6)'s no-region refusal built 2026-09-29, keyed on the provider's `platform`, with one re-ask for the host notch. Two live questions, [`OQ-BR9`](../design/bedrock-plumbing.md#OQ-BR9) and [`OQ-BR1`](../design/bedrock-plumbing.md#OQ-BR1), ruled together. |
| [../design/providers-and-profiles-redesign.md](../design/providers-and-profiles-redesign.md) | Split out of bedrock-plumbing 2026-09-25 as the seed of a redesign: what `-p <name>` should name, the marker a derive recognizes a provider by, whether a gate keys on the profile name or the provider, where the transport (native client or wire bridge) lives, and what a bare `-p X` does for an agent that cannot reach X. Explains today's provider/profile model in plain words first. | **DESIGN, 2026-09-25.** [`OQ-BR2`](../design/providers-and-profiles-redesign.md#OQ-BR2), [`OQ-BR8`](../design/providers-and-profiles-redesign.md#OQ-BR8) and [PP-D1](../design/providers-and-profiles-redesign.md#PP-D1) ruled and built 2026-09-29. Three live questions: [`OQ-PP1`](../design/providers-and-profiles-redesign.md#OQ-PP1)–[`OQ-PP3`](../design/providers-and-profiles-redesign.md#OQ-PP3). |
| [../design/model-lists-and-pickers.md](../design/model-lists-and-pickers.md) | Split out of bedrock-plumbing and provider-switching 2026-09-25: where yolo must pick model ids itself, the built-in picks pack that carries them, the `models` kind a company pack shapes a list with, how each agent's picker renders it, tier aliases, and currency without a catalog. | **DESIGN, 2026-09-25, partly BUILT** (re-checked 2026-09-30): the `openai-codex` and Bedrock lists in data, the `models` kind, the enforcement switch and each agent's rendering under an `only` (2026-09-27 to 09-29); `yolo check`'s currency warning and claude's conventional tier aliases (2026-09-30). Also 2026-09-30, each after its measurement: pi's refusing wrapper, on `openai-codex` too once a second measurement followed the subscription login through it, and codex's prelaunch menu on `openai-codex`. On 2026-10-01, [MM-D5](../design/model-lists-and-pickers.md#MM-D5)'s `yolo check` line naming each provider `"enforce_models": false` leaves opencode's menu unnarrowed on ([MM-D29](../design/model-lists-and-pickers.md#MM-D29)). copilot's `providers.json` stopped on its measurement, which filed [`OQ-MM4`](../design/model-lists-and-pickers.md#OQ-MM4). codex's menu at `yolo host` designed, not built, which filed [`OQ-MM5`](../design/model-lists-and-pickers.md#OQ-MM5). Live questions: [`OQ-MM1`](../design/model-lists-and-pickers.md#OQ-MM1) first, then [`OQ-MM3`](../design/model-lists-and-pickers.md#OQ-MM3), then [`OQ-MM4`](../design/model-lists-and-pickers.md#OQ-MM4), then [`OQ-MM5`](../design/model-lists-and-pickers.md#OQ-MM5). |
| [../design/wire-bridge-gateway.md](../design/wire-bridge-gateway.md) | Split out of bedrock-plumbing 2026-09-25: the wire bridge as the jail's model gateway — SigV4 signing, routing claude's everything profile by model id, a sign-only chat-completions route, the subscription arm with opt-in failover, and the new all-traffic mode that enforces a model allowlist and routes per agent. | **DECIDED, 2026-09-25**, no ruling owed — the SigV4 signer, [`OQ-WG6`](../design/wire-bridge-gateway.md#OQ-WG6)'s `via` profile field, [`OQ-WG7`](../design/wire-bridge-gateway.md#OQ-WG7)'s multi-route daemon and the sign-only route are BUILT ([§4.1](../design/wire-bridge-gateway.md#41-how-it-is-built)), and so are codex's Responses via route ([`WG-I20`](../design/wire-bridge-gateway.md#WG-I20)), Part 2's routing by model id, the region-composed upstream and the platform re-key ([`WG-I37`](../design/wire-bridge-gateway.md#WG-I37)–[`WG-I39`](../design/wire-bridge-gateway.md#WG-I39)), and Part 5's allowlist ([`WG-I40`](../design/wire-bridge-gateway.md#WG-I40)–[`WG-I43`](../design/wire-bridge-gateway.md#WG-I43)); no agent has sent a request through the via route. Not built: Part 4, which waits on a measurement, and pi's Converse route, decided as [`WG-I36`](../design/wire-bridge-gateway.md#WG-I36) and waiting on [`pi-codex-provider-shadowing.md` OQ-3](../design/pi-codex-provider-shadowing.md#OQ-3). |
| [../design/bedrock-web-search.md](../design/bedrock-web-search.md) | Split out of bedrock-plumbing 2026-09-25: web search on every Bedrock profile as an MCP tool served by an AgentCore gateway the company creates once, reached through an in-jail signing proxy and shipped by the Bedrock pack. | **DESIGN, 2026-09-25, nothing built.** The direction is ruled ([DIR-BR4](../design/bedrock-web-search.md#DIR-BR4)); five live questions, [`OQ-BR19`](../design/bedrock-web-search.md#OQ-BR19)–[`OQ-BR23`](../design/bedrock-web-search.md#OQ-BR23). |
| [../reference/providers.md](../reference/providers.md#deselection-clear-what-yolo-wrote-keep-what-the-user-wrote) (was design/provider-switching.md) | Split out of bedrock-plumbing: a deselected profile left codex, pi and opencode asking a new endpoint for the old provider's model, and the fix is one selection rule — clear the id yolo wrote, keep the one the user wrote. Its alias and shipped-id questions moved to model-lists-and-pickers 2026-09-25. | **GRADUATED 2026-09-26** to [../reference/providers.md](../reference/providers.md#deselection-clear-what-yolo-wrote-keep-what-the-user-wrote), now the authority. [`OQ-PSW2`](../reference/providers.md#oq-psw2) (the deselect clear), [`OQ-PSW4`](../reference/providers.md#oq-psw4) (recorded in `boot.log`, silent on the terminal) and [`OQ-SW1`](../reference/providers.md#oq-sw1) (`-p` outranks a host-config value) are built and resolve in its appendix, and [the clear holds on an adopting boot too](../reference/providers.md#a-clear-holds-on-an-adopting-boot-too). The design file is deleted; every link now points at the reference. |
| [../reference/providers.md](../reference/providers.md#the-credential-gate) | Split out of provider-switching on 2026-09-22: which provider **credentials** and profile-gated env reach an agent, and how a value reaches only the agent that selected it. Before the gate every hydrated `env_sources` key and every satisfied pack `env` gate reached every agent. | **GRADUATED 2026-10-01** from `provider-credential-scope.md` into [`providers.md`'s credential gate](../reference/providers.md#the-credential-gate), now the authority, with [`OQ-BR4`](../reference/providers.md#oq-br4) and [`OQ-CN1`](../reference/providers.md#oq-cn1) to [`OQ-CN9`](../reference/providers.md#oq-cn9) in [its why-appendix](../reference/providers.md#the-credential-gate-rulings). The stub at [../design/provider-credential-scope.md](../design/provider-credential-scope.md) keeps the build record: the done-condition tests and the `CN-D1` to `CN-D25` ledger. MEASURED BY TESTS ONLY, save one nested podman boot; UNMEASURED whether the per-agent env file reaches every way an agent is started. |
| [../design/pi-codex-provider-shadowing.md](../design/pi-codex-provider-shadowing.md) · [plan](../design/pi-codex-provider-shadowing-plan.md) | Filed 2026-09-25 from another checkout: pi's catalog derive wrote an `openai-codex` row into `models.json` that shadowed pi's built-in ChatGPT subscription client, which then sent the workspace's ambient `OPENAI_API_KEY` and got 401s. | **DESIGN, 2026-10-05 — [`OQ-3`](../design/pi-codex-provider-shadowing.md#OQ-3) ruled the broad reading** (yolo writes no model entry over any provider an agent has built in; each agent pack declares the names; not built, the [plan's §2.1](../design/pi-codex-provider-shadowing-plan.md#21-the-broad-reading-the-build)), and [`OQ-4`](../design/pi-codex-provider-shadowing.md#OQ-4) is open (whether that reaches a list a pack declares for such a provider). The `openai-codex` exclusion is BUILT, 2026-09-26: by name for pi (`92c20cc6`) and omp (`4ed48212`); codex already did; the other derives needed nothing. [`OQ-1`](../design/pi-codex-provider-shadowing.md#OQ-1) and [`OQ-2`](../design/pi-codex-provider-shadowing.md#OQ-2) ruled. Measured by the catalog tests only, which compose each agent's `needs` closure since 2026-09-27. Not a graduation candidate while [`OQ-4`](../design/pi-codex-provider-shadowing.md#OQ-4) is open and the broad reading unbuilt. |
| [../design/attach-skew-and-contract-guardrails.md](../design/attach-skew-and-contract-guardrails.md) · [plan](../design/attach-skew-and-contract-guardrails-plan.md) | Filed 2026-09-26 from another checkout: an attach after a host update handed a running jail pack contracts its older binaries could not read (agy's `unknown command "footer"`, and a boot refused on claude's list-form `api_key_env_name`). | **DESIGN, 2026-09-26 — the attach gate is BUILT** (named contract tags; a terminal restart prompt, a refusal elsewhere, `YOLO_ALLOW_ATTACH_SKEW` as the one acknowledgment), and pack-contract skew is closed by [`OQ-PK2`](../reference/pack-system.md#oq-pk2)'s per-launch trees. [`OQ-SK4`](../design/attach-skew-and-contract-guardrails.md#OQ-SK4) was decided as SK-D15 and is built (2026-09-30): an acknowledged attach names the skew in the briefing it refreshes. Measured on Linux podman; no attach to a jail a real older yolo launched. |
| [../design/gateway-provider-packs.md](../design/gateway-provider-packs.md) | Gateway providers (Kilo and its kind) shipped as provider packs rather than as agent-derive special cases. | **DESIGN, 2026-09-26** — [`OQ-GP4`](../design/gateway-provider-packs.md#OQ-GP4) (the Kilo special-casing in two derives) is open and holds its graduation; everything else BUILT 2026-09-15. MEASURED: the manifests and per-agent projections, by unit test. UNMEASURED: no live OpenRouter or Kilo run — no agent session through either gateway is recorded. |
| [../reference/agent-credentials.md](../reference/agent-credentials.md#the-openai-subscription-credential-service) · [plan](../design/openai-auth-broker-plan.md) | The OpenAI subscription broker: one writer of the token and a view per agent (Codex, Pi, opencode), with host-only import and logout (`yolo openai-auth`). | **GRADUATED 2026-10-01** from `openai-auth-broker.md` into [`agent-credentials.md`'s OpenAI section](../reference/agent-credentials.md#the-openai-subscription-credential-service), now the authority, with [`OQ-OA1`](../reference/agent-credentials.md#oq-oa1) to [`OQ-OA7`](../reference/agent-credentials.md#oq-oa7) and [`OA-D1`](../reference/agent-credentials.md#oa-d1) in its why-appendix. The design is a pointer. The [plan](../design/openai-auth-broker-plan.md) stays: it owns the checks a ChatGPT account needs, a browser login end to end and a real expiry crossing, which are UNMEASURED. |
| [../design/sso-backed-bedrock.md](../design/sso-backed-bedrock.md) → [../reference/agent-credentials.md](../reference/agent-credentials.md#sso-backed-bedrock-credentials-aws-auth) | Bedrock from a host `aws sso login`, served into the jail by `packs/aws-auth` over the container-credentials endpoint. [`OQ-SSO7`](../design/sso-backed-bedrock.md#13-decision-ledger) rules three Bedrock credentials supported, SSO primary. | **GRADUATED 2026-09-29** into [`agent-credentials.md`'s SSO-backed Bedrock section](../reference/agent-credentials.md#sso-backed-bedrock-credentials-aws-auth), now the authority. The design stays whole as the argument and its [Decision Ledger](../design/sso-backed-bedrock.md#13-decision-ledger): all ten questions ruled, and [SSO-D1](../design/sso-backed-bedrock.md#SSO-D1), the launch warning for a missing or lapsed session, built the same day. Every build step is done and the implementation plan is deleted. In daily use against a live `aws sso login` on a Linux host since the 2026-09-29 try-out; a running jail picking up a re-login after a portal session ends, and `macos-user`, are unmeasured. |
| [../design/agent-footer.md](../design/agent-footer.md) | Which provider a session is on, in every agent's footer: one core renderer, `yolo internal footer`, prints the billing route in plain words and the notch (jail, guest or host), and each agent pack wires it into its agent's own footer hook; codex has none. | **BUILT, 2026-09-30** — [`OQ-FT15`](../design/agent-footer.md#OQ-FT15) (how a one-launch `yolo host -p` reaches the host footer) was decided as [FT-D2](../design/agent-footer.md#FT-D2) and built 2026-09-30; everything else BUILT 2026-09-25. UNMEASURED: no footer has been watched under a live agent. |
| [../design/credential-sources-separation.md](../design/credential-sources-separation.md) · [plan](../design/credential-sources-separation-plan.md) | Filed 2026-09-27: whether provider credentials should leave `env_sources` for a key of their own, after a claimed key was withheld from `yolo host -- bash`. Measured against the built gate, the host remedy (`yolo host -p <profile> -- <cmd>`) already exists and the split would revisit [`OQ-CN1`](../reference/providers.md#oq-cn1). | **DESIGN, 2026-09-27 — ES-D1 to ES-D23 built at the host**, [`OQ-ES5`](../design/credential-sources-separation.md#OQ-ES5)'s host half ruled and built (`yolo host --with-credentials`); [`OQ-ES1`](../design/credential-sources-separation.md#OQ-ES1), [`OQ-ES5`](../design/credential-sources-separation.md#OQ-ES5)'s jail half, [`OQ-ES6`](../design/credential-sources-separation.md#OQ-ES6) and [`OQ-ES7`](../design/credential-sources-separation.md#OQ-ES7) open. |

## The 2026-09 sprint — five designs, all built

The sprint that closed on 2026-09-12 built five accepted designs, and **none of them had a row
here**. That is this index's own rule failing one level up: a row that disagrees with its doc is
wrong and gets fixed, but a doc with no row cannot be caught disagreeing with anything. Each status
below is the doc's own header, read 2026-09-12.

Whether each has earned a `system-doc` graduation was assessed the same day, per doc, walked against
the code rather than against the status line, in a triage record since retired (`doc-triage.md`,
recoverable with `git log --follow`). **One graduated at once**; the other four were held, each for a
different named reason, and the sequencing is [OQ-DT1](#oq-dt1).

| Doc | What it is | Status |
|---|---|---|
| [../reference/report-tiers.md](../reference/report-tiers.md) | `yolo host apply` stated every fact and never its **result**, so the reader was left adding the lines up. One **verdict line** per run, and a **report tier** on every line beneath it — notch facts, run facts, losses and blockers — where the tier, not the emitter, decides whether a line prints by default, once per run, or only under `--verbose`. A declared dependency that is missing becomes a blocker rather than a line in the middle. | **GRADUATED 2026-09-13** to [../reference/report-tiers.md](../reference/report-tiers.md), which is now the authority — the tiers, P1–P8, the verdict contract, the remedy contract, the report vocabulary, the launch stream and every `OQ-RO` ruling. `AGENTS.md` cites its [`OQ-RO3`](../reference/report-tiers.md#why-its-this-way) as the standing *a launch has no quiet mode* rule and now resolves into the reference tree. The file left at the old path is a **redirect stub**, kept only until two [`OQ-RO3`](../reference/report-tiers.md#why-its-this-way) citations in a concurrently-held file are repointed; its implementation sketch was archived in the same commit. |
| [../design/config-ownership-and-promotion.md](../design/config-ownership-and-promotion.md) | Who owns an agent's config file. yolo infers it from the confinement notch, and adopting `yolo host apply` is precisely the act of leaving the world that inference is sound in — so ownership becomes one declared user-scope key (`host_management: none \| assert \| own`), `own` makes the host render like a jail, and `yolo config promote` lifts a captured in-jail edit into the conventional local pack instead of discarding it. | **DESIGN again since 2026-09-20** — built 2026-09-12, then reopened: retiring `host_management: "assert"` is ruled and unbuilt, [`CO13`](../design/config-ownership-and-promotion.md#13-decision-ledger) is decided and unbuilt, and [`OQ-CO14`](../design/config-ownership-and-promotion.md#oq-co14) is open ([roadmap.md](roadmap.md)), which holds its graduation. The 2026-09-12 build: all eight of [§10](../design/config-ownership-and-promotion.md#10-what-i-would-build-in-order)'s steps landed, step 8's **one-time adoption archive** last: `entrypoint.archiveAdoption`, one call from the stateful writer both notches share ([§6.3.3](../design/config-ownership-and-promotion.md#633-what-survives-as-a-guard), [OQ-CO7](../design/config-ownership-and-promotion.md#13-decision-ledger)). Every question the design opened is ruled, and so is the one the BUILD opened — [OQ-CO12](../design/config-ownership-and-promotion.md#13-decision-ledger) settled the `assert` → `own` switch as **keys-and-values invariant, not byte-invariant** (2026-09-12), leaving the two key deletions it measured as bugs rather than as conformance — **both fixed the same day**. ⚠ It **reverses four rulings** recorded in [environment-manager-plan.md](environment-manager-plan.md#open-questions-to-resolve-before-their-phase). |
| [../reference/pack-system.md](../reference/pack-system.md#oq-lt1) | Removed the `config.lua` transform slot between the merge and the managed-enforce step: no user, its one worked example served by the declarative `autonomy` kind, its determinism requirement stated in a doc and enforced by nothing. The cut ran along the seam between the transform and the shipped packs' `derive.lua` path, and carried the managed floor (`Enforce`) into `internal/agentcfg`. | **GRADUATED 2026-09-23** — folded into [../reference/pack-system.md](../reference/pack-system.md#oq-lt1), which is now the authority: [the derive slot](../reference/pack-system.md#the-derive-slot), [the managed floor](../reference/pack-system.md#the-managed-floor), [OQ-LT1](../reference/pack-system.md#oq-lt1) and [OQ-LT2](../reference/pack-system.md#oq-lt2). A removal doc had no system of its own to describe, so it was folded rather than given a reference, and deleted. |
| [../reference/macos-user-home-tiers.md](../reference/macos-user-home-tiers.md) | macos-user has one sandbox home, so the machine, workspace and session tiers were the same directory — which content delivery turned from a static leak between workspaces into a **write-write race on the briefing an agent reads as instructions**. `HOME` stays where it is; every directory the container backends bind from `<workspace>/.yolo/home/` becomes a symlink into that same sidecar, and every pack-declared machine-scope directory is mirrored so the credential hook's relative link keeps resolving. | **GRADUATED 2026-09-21** to [../reference/macos-user-home-tiers.md](../reference/macos-user-home-tiers.md), which is now the authority — the tier table, the layout and its six ordering rules, the mirror and the relative link it exists for, the no-migration refusal, and [`OQ-HT1`](../reference/macos-user-home-tiers.md#oq-ht1)–[`OQ-HT4`](../reference/macos-user-home-tiers.md#oq-ht4) under [Why it is this way](../reference/macos-user-home-tiers.md#why-it-is-this-way). Built 2026-09-12 with no migration step, as ruled, and **measured** by the 2026-09-12 hardware session plus the nightly job since. ⚠ Its [measurement table](../reference/macos-user-home-tiers.md#what-is-measured-and-by-what) names what is still an argument, and one defect is open and deliberately un-automated. The design doc left at the old path is a **superseded stub**. |
| [../reference/macos-user-provisioning.md](../reference/macos-user-provisioning.md) | The other half of the same gap: a container jail gets its tools from an image **floor** and an imperative **stage**, and macos-user had neither, so `mise_tools`, `lsp_servers` and `mcp_presets` rendered config and installed nothing. Two separable halves in order — a core set for the noncontainer nix profile, then the container's own stage body run as a new Seatbelt-confined step between the bootstrap and the agent. | **GRADUATED 2026-09-21** to [../reference/macos-user-provisioning.md](../reference/macos-user-provisioning.md), which is now the authority — the floor and its two-kinds-of-exclusion fatal, the stage and its failure policy, and [`OQ-P1`](../reference/macos-user-provisioning.md#oq-p1)–[`OQ-P5`](../reference/macos-user-provisioning.md#oq-p5) under [Why it is this way](../reference/macos-user-provisioning.md#why-it-is-this-way). Both halves were built 2026-09-12 and are **measured**: a hardware session on 2026-09-12 and the nightly [`macos-user.yml`](../../.github/workflows/macos-user.yml) job since 2026-09-13. ⚠ **One claim is still unmeasured and is named as such** — [what a first stage costs](../reference/macos-user-provisioning.md#the-one-unmeasured-claim--what-a-first-stage-costs), which is why that job is not wired to PRs. The design doc left at the old path is a **superseded stub**. |

**Neither macOS row is held back any more, and what released them was a measurement rather than an
edit.** Every doc in [`../reference/`](../reference) carries a `verified_commit`, and a doc about a
backend nobody has run cannot honestly carry one. The 2026-09-12 hardware session and the nightly
[`macos-user.yml`](../../.github/workflows/macos-user.yml) job supplied what was missing, and each
graduated reference names the claims they still do not cover rather than rounding them up.

## Track M verification runbooks

[`runbooks/`](runbooks) holds the Mac hardware verification procedures — they
are the revival plan's Track M gates, not user-facing reference, which is why they
left the user guide's old directory. See the [sequencing-2026-07](sequencing-2026-07.md#runbooks)
for their status and that move:

| Doc | What it is | Status |
|---|---|---|
| [runbooks/mac-macos-user-e2e.md](runbooks/mac-macos-user-e2e.md) | You-drive macos-user acceptance-bar test (the M1 anchor). | **Passed** (2026-07-21); M1 gate green; kept as repeatable procedure. |
| [runbooks/mac-ac-container-builder.md](runbooks/mac-ac-container-builder.md) | Zero-sudo Apple Container builder proof; Track-M/J3-adjacent. | **Passed** (2026-07-17) — kept as the repeatable procedure. |
| [runbooks/mac-go-port-verification.md](runbooks/mac-go-port-verification.md) | Go-vs-Python diff verification of the port. | **Stale** — recommended for `git rm` (its diff-against-Python method is dead post-wipe). |

Related live tracker: [`../research/macos-support-matrix.md`](../research/macos-support-matrix.md)
is the authoritative state-of-the-macOS-backend matrix.
