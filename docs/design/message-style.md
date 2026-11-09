---
title: "How a yolo message reads: fifteen real messages, rewritten in one style"
date: 2026-11-09
status: draft
stage: DESIGN
next: "The owner marks up the fifteen samples and rules OQ-MS1 to OQ-MS5 in Vantage; the marked-up rule list then moves into plain-language-principle.md and drives the language sweep"
tags: [design, messages, cli, ux, writing, style]
summary: "The plain-language principle says what a message must not contain. It does not say how one is shaped: who the subject is, how a refusal is ordered, where a command goes, how a path is shown, how a line starts and ends. This doc picks fifteen real messages from the tree, one per style question, rewrites each in one proposed style, and lists the rules those rewrites imply. The owner marks up the samples; the result is the rule an agent applies across the codebase."
---

# How a yolo message reads: fifteen real messages, rewritten in one style

**Status:** 2026-11-09. Nothing built. Every "Before" was checked with `rg` against `b36be7d53`; line
numbers move, so each one is the place to start looking.

> **In short.** The message audit already says what a message must not contain. What is still open
> is shape: one voice, one order for a refusal, one place for a command, one way to show a path.
> Fifteen real messages, each rewritten in one style, are cheaper to rule on than a list of
> abstract rules — mark up the samples, and the rules fall out.

**Why it matters.** About 1,100 messages are waiting on the
[language sweep](../research/message-rewrites/README.md). An agent doing that sweep from the
[plain-language principle](../reference/plain-language-principle.md) alone makes these choices once per
message, and today's code shows the result: "yolo did not", "Not started:", "opened the", "pack %s",
`~/…` and absolute paths, all in one launch.

**The shape.** [Fifteen samples](#the-samples), each a style question; [the rules](#the-rules-the-samples-imply)
they imply; [five questions](#open-questions) where the choice is the owner's taste.

**Start at [the samples](#the-samples)** — mark up an After you would word differently; the rule
under it changes with it.

**Needs your ruling:** [OQ-MS1](#OQ-MS1), [OQ-MS2](#OQ-MS2), [OQ-MS3](#OQ-MS3), [OQ-MS4](#OQ-MS4),
[OQ-MS5](#OQ-MS5).

**Reads with:** [`plain-language-principle.md`](../reference/plain-language-principle.md) (rules 1–8
on content, which this doc does not repeat), [`happy-path-principle.md`](../reference/happy-path-principle.md)
(every stop names the next step), [the message audit](../research/message-rewrites/README.md) (the
~1,100 messages this style will be applied to).

---

## What this decides, and what it does not

This doc decides the **shape** of a message: its subject, its order, its punctuation, and how it shows
a command, a path, a value or a check finding. The plain-language principle already decides the
**content** — no internal words, no decision ids, one idea per sentence, say what to do. Where a
sample breaks a content rule, the After fixes that too, and the rule column names it.

It does not cover:

- rewriting the other ~1,085 messages — that is the language sweep, done after the rules are ruled;
- `--help` and usage-text layout, the briefing and skills, the changelog — same principle, different
  shapes, out of scope here;
- color: markup is stripped from every quote below, and no rule changes it.

## How to read a sample

- **Before** is the source string, verbatim, with its Go format verbs (`%s`, `%q`). A value the code
  concatenates in is written `<name>`.
- **After** follows every leaning in [Open questions](#open-questions). If a leaning loses, the Afters
  that show it change with it.
- **Rule** names the style rule the sample was chosen to show, from [the rule list](#the-rules-the-samples-imply).

## The samples

| # | Kind | Source | Style question it settles |
| :--- | :--- | :--- | :--- |
| 1 | Refusal, yolo bug | `internal/cli/run/launchinput.go:288` | A stop only a yolo bug causes |
| 2 | Refusal, user's packs | `internal/cli/run/packfiles.go:405` | Whose next step it is |
| 3 | Refusal with a command | `internal/cli/packadd.go:66` | A command on its own line |
| 4 | Launch line | `internal/cli/run/keeperspawn.go:634` | Internal jargon ("keeper") |
| 5 | Disclosure | `internal/cli/run/macosuserdoorways.go:172` | A long multi-clause sentence |
| 6 | Launch line | `internal/cli/run/packservices.go:319` | A decision id in printed text |
| 7 | Warning | `internal/cli/run/hostloopback.go:430` | A repository path in printed text |
| 8 | Warning | `internal/cli/run/homeskeleton.go:298` | Voice: who acted |
| 9 | Progress line | `internal/hostfloor/ensure.go:146` | Capitalization; jargon ("floor") |
| 10 | `yolo check` skip | `internal/cli/check/sections_loopholes.go:31` | Headline as a sentence |
| 11 | `yolo check` failure | `internal/cli/check/check.go:385` | Headline and fix line |
| 12 | `yolo check` fix line | `internal/cli/check/packs.go:154` | Path display; "then: yolo check" |
| 13 | Malformed user file | `internal/config/validate.go:537` | Echoing a bad value |
| 14 | Returned error | `internal/agentcfg/compose.go:406` | Go package prefix and `%T` |
| 15 | Verb refusal | `internal/cli/apply.go:146` | Command prefix; "notch" |

### 1. A refusal only a yolo bug can cause

Before:

```text
Host service '<name>' has no verified private launch-input destination and complete jail mount argv; it was not started. Wire the core producer and full assembled mount topology before retrying.
```

After:

```text
yolo did not start the host service '<name>', because it could not prepare that service's settings. This is a bug in yolo.
Launch again. If it happens again, report it, with the output of `yolo --version`, at:
    https://github.com/mschulkind-oss/yolo-jail/issues
```

**Rule:** [S11](#s11) — a bug-only stop says it is a bug and names who can act.

### 2. A refusal the user's own packs cause

Before:

```text
pack %s and pack %s both deliver different content for ~/%s — give them different file names, or drop one
```

After:

```text
yolo cannot start this jail, because packs %s and %s both put a different file at ~/%s. Remove one of them:
    yolo pack remove <name>
Or ask the pack's author to rename the file.
```

**Rule:** [S3](#s3) — what yolo did not do, why, then the next step for *this* reader (not the pack's author).

### 3. A refusal that hands over a command

Before:

```text
the pack list is the user config's, which only the host edits; run: yolo host-run -C %s --name pack-%s -- %s
```

After:

```text
yolo pack %s: only the host can change your pack list. Paste this line into a host terminal:
    yolo host-run -C %s --name pack-%s -- %s
```

**Rule:** [S4](#s4) — a command to run goes alone on its own indented line.

### 4. A launch line built on a project word

Before:

```text
The keeper of %s (pid %s) has not finished after %s. Its log: %s. Its last line: %q. `kill %s` ends it in order.
```

After:

```text
The yolo process shutting down %s (pid %s) is still running after %s. Its last log line was %q. Full log: %s
To let it finish shutting down, run:
    kill %s
```

**Rule:** [S7](#s7) — name a thing by what it does for the reader.

### 5. A disclosure written as one long sentence

Before:

```text
Opened the %q doorway (pack %q, pid %d) on %s for this launch, outside the sandbox: it answers only this launch's caller token, forwards to the host's %q service, and stops when the command exits. Its log: %s
```

After:

```text
yolo started a connection to the host's %q service on %s (pack %q, pid %d). It runs outside the sandbox and accepts only this launch's token. It stops when the command exits. Log: %s
```

**Rule:** [S10](#s10) and [S12](#s12) — one idea per sentence; a disclosure keeps every fact it discloses.

### 6. A launch line that prints a decision id

Before:

```text
Not started: the <daemon> jail daemon, because <why>, which it serves; select one (`-p <agent>=<profile>`) to start it (providers.md OQ-CN7).
```

After:

```text
yolo did not start the <daemon> service, because <why>. To start it, select a profile it serves:
    yolo -p <agent>=<profile>
```

**Rule:** [S8](#s8) — no decision id or design-doc name in printed text.

### 7. A warning that points at a repository file

Before:

```text
Warning: YOLO_NO_HOST_LOOPBACK is set — NOT requesting host-loopback forwarding (<args>).
  Jail-facing services (Claude OAuth broker, yolo-ps, yolo-journalctl) will be
  unreachable from inside this jail. Unset it to restore the fix.
  docs/reference/loopback-tls-reachability.md
```

After:

```text
Warning: YOLO_NO_HOST_LOOPBACK is set, so yolo did not connect this jail to services on the host. The Claude login broker, yolo-ps and yolo-journalctl will not answer from inside it. To fix this, unset YOLO_NO_HOST_LOOPBACK and launch again.
```

**Rule:** [S8](#s8) — no repository path; a user who installed a release has no checkout. Link the
published guide only when a page exists.

### 8. A warning with no subject

Before:

```text
Warning: home skeleton: cannot create the mountpoint %s: %w
```

After:

```text
Warning: yolo could not prepare %s inside the jail (%w). The jail starts, but anything your config puts at that path is missing.
```

**Rule:** [S1](#s1) — yolo is the subject when yolo acted or failed.

### 9. A progress line

Before:

```text
installing %s into yolo's floor (%s): %s
```

After:

```text
Installing %s for `yolo host` (%s): %s...
```

**Rule:** [S2](#s2) — sentence case; a progress line ends in `...`.

### 10. A `yolo check` headline written as a label

Before:

```text
[SKIP] Inside jail — loophole checks skipped
Loopholes are host daemons; run `yolo check` on the host to see whether they are healthy.
```

After:

```text
[SKIP] yolo check does not check host services from inside a jail.
  -> Run yolo check on the host to see whether they work.
```

**Rule:** [S13](#s13) — a check headline is a sentence that says what happened.

### 11. A `yolo check` failure and its fix line

Before:

```text
%s found but not working: exec failed
Run `<version command>` yourself to see why it does not answer, then: yolo check
```

After:

```text
[FAIL] podman is installed, but yolo could not run it.
  -> Run this to see why, then run yolo check again:
       podman --version
```

**Rule:** [S13](#s13) and [S4](#s4) — the headline says what is wrong; the `->` line says what to do.

### 12. A `yolo check` fix line that names a file

Before:

```text
Fix the `packs` list in <user config path> (`yolo pack --help` shows an entry's forms), then: yolo check
```

`<user config path>` prints absolute today (`paths.UserConfigDisplayPath`), while sample 2 prints `~/`.

After:

```text
Fix the packs list in ~/.config/yolo-jail/config.toml, then run yolo check again. To see the forms an entry can take, run yolo pack --help.
```

**Rule:** [S5](#s5) — a path under the reader's home is shown with `~`.

### 13. An error from a malformed user file

Before:

```text
config.kvm: expected a boolean (got <value>)
```

A user who wrote `kvm = "yes"` sees `(got 'yes')` — Python's spelling, from `pyReprValue`.

After:

```text
kvm must be true or false, but your config sets it to "yes".
```

**Rule:** [S9](#s9) — echo a value in the config file's own syntax, and say what is expected in the
file's own words.

### 14. A returned error with a Go package prefix

Before:

```text
agentcfg: surface %s/%s: host config is not %s (got %T)
```

`%T` prints Go types such as `[]interface {}`.

After:

```text
yolo cannot use <file> as %s's settings, because it holds a list where yolo expects a table. Fix the file, then run the command again.
```

**Rule:** [S6](#s6) — no Go package name, Go type or internal noun ("surface") in a message.

### 15. A verb's refusal that explains the design

Before:

```text
yolo apply: --format json is the HOST notch's dry run (`yolo apply --setup host-self`, or `yolo host apply`). No other notch has a document to emit: at jail this verb is a launch, at guest on macOS it points at launch, and guest has no backend on Linux.
```

After:

```text
yolo apply: --format json works only for the host. To see what yolo would change on the host, run:
    yolo host apply --format json
```

**Rule:** [S6](#s6) and [S10](#s10) — a verb's error starts with its command name; the reader gets the
fix, not the design.

## The rules the samples imply

Each rule is a short imperative. *(The S-numbers are coined here, as stable ids for markup; when the
rules move into the plain-language principle they are renumbered there.)*

1. <a id="s1"></a>**S1. Make yolo the subject** when yolo acted, failed or refused: "yolo did not start
   X", not "Not started: X" and not "I did not start X" ([OQ-MS1](#OQ-MS1)).
2. <a id="s2"></a>**S2. Write every line as a sentence:** a capital letter, a verb, and a closing
   period. A progress line ends in `...` instead. "yolo", and a command or program name such as
   `podman`, keeps its lowercase even at the start of a line ([OQ-MS2](#OQ-MS2)).
3. <a id="s3"></a>**S3. Order a refusal in three parts:** what yolo did not do, the reason in one
   clause, then the next step as its own sentence — a step for the person reading, not for a pack
   author or a yolo developer.
4. <a id="s4"></a>**S4. Put a command the reader should run alone on an indented line,** with no
   backticks and nothing after it. A command only mentioned in passing stays inline
   ([OQ-MS3](#OQ-MS3)).
5. <a id="s5"></a>**S5. Show a path under the reader's home with `~`;** show any other path in full.
   Never print a path relative to yolo's repository ([OQ-MS4](#OQ-MS4)).
6. <a id="s6"></a>**S6. Start a verb's error with `yolo <verb>:`,** then a sentence. Never start any
   message with a Go package name, and never print a Go type ([OQ-MS5](#OQ-MS5)).
7. <a id="s7"></a>**S7. Name a thing by what it does for the reader** — "the yolo process shutting
   down the jail", "a connection to the host's service" — never by a project word such as keeper,
   doorway, floor, notch, surface or contribution.
8. <a id="s8"></a>**S8. Print no decision id, design-doc name or repository path.** Link the published
   user guide when the reader needs more and a page exists; otherwise say nothing more.
9. <a id="s9"></a>**S9. Echo a bad value as the reader wrote it,** in the config file's own syntax, and
   name what was expected in that file's words: "true or false", "a table".
10. <a id="s10"></a>**S10. End a sentence where its idea ends.** No em dash, semicolon or colon chains
    joining two ideas.
11. <a id="s11"></a>**S11. Say so when only a yolo bug can cause the stop,** then name who can act:
    launch again, and report it at the issues page with `yolo --version`.
12. <a id="s12"></a>**S12. Keep every fact a disclosure discloses** — pid, address, log path — and say
    in present tense what the thing does for the reader.
13. <a id="s13"></a>**S13. In `yolo check`, the headline is a sentence saying what is wrong, and the
    `->` line says what to do,** ending "then run yolo check again".

## What done looks like

1. The owner's markup is folded into the samples and the rules, and the five questions are ruled.
2. The rule list moves into [`plain-language-principle.md`](../reference/plain-language-principle.md)
   as its message-shape section, and this doc is deleted.
3. The language sweep applies those rules across the parts of
   [the message audit](../research/message-rewrites/README.md); each part's Afters are rewritten to the
   ruled style before they are applied.

## Open questions

1. 💬 🤷 **OQ-MS1: Should a message say "yolo did not…", "I did not…", or neither?**

   Priya launches a jail and yolo skips a service. Today one launch prints "yolo did not start…",
   "Not started: the … daemon" and "Opened the … doorway" — three voices. If an agent relays the line
   to her, a message that says "I" reads as the agent speaking.

   - **A — "yolo did not start X."** Clear who acted, whoever relays it. Slightly longer.
   - **B — "I did not start X."** Friendlier; ambiguous once an agent repeats it.
   - **C — "Not started: X."** Shortest; no subject, which rule 3 of the principle forbids.

   <!-- vantage: question id=OQ-MS1 leaning="A — it is already the commonest form in the code, and it stays clear when an agent relays the line." -->

   _Leaning:_ A — it is already the commonest form in the code, and it stays clear when an agent relays the line.

   **Answer:**
   > _(empty — fill in when decided)_

2. 💬 🤷 **OQ-MS2: Do short lines — check headlines, progress lines — get a capital and a period?**

   Priya runs `yolo check` and reads `[FAIL] podman found but not working: exec failed`: lowercase,
   no period, no verb. Messages elsewhere are full sentences. The headline sits beside the badge, so
   it could stay a label.

   - **A — Every line is a sentence.** `[FAIL] podman is installed, but yolo could not run it.` Progress
     lines end in `...`. One rule, no exceptions to remember.
   - **B — Headlines and progress lines are capitalized fragments with no period.**
     `[FAIL] Podman installed but not runnable`. Shorter; reads like a status board, and a
     program name at the start gets capitalized.

   <!-- vantage: question id=OQ-MS2 leaning="A — one rule with no exceptions, and the principle already says headings are sentences." -->

   _Leaning:_ A — one rule with no exceptions, and the principle already says headings are sentences.

   **Answer:**
   > _(empty — fill in when decided)_

3. 💬 🤷 **OQ-MS3: Does a command the reader should run go on its own line, or inline?**

   Priya's launch refuses and tells her to run `yolo pack remove zai`. Today some messages put the
   command mid-sentence in backticks; the `host-run` ones run it on after "run:". She copies it with
   the mouse, and an agent copies it as text.

   - **A — Its own indented line, no backticks.** Copies exactly; costs a line per message.
   - **B — Inline in backticks, own line only when it is long.** Shorter; the reader must trim the
     backticks and decide what "long" means.

   <!-- vantage: question id=OQ-MS3 leaning="A — the command is the next step, and a line of its own copies exactly for a person and an agent alike." -->

   _Leaning:_ A — the command is the next step, and a line of its own copies exactly for a person and an agent alike.

   **Answer:**
   > _(empty — fill in when decided)_

4. 💬 🤷 **OQ-MS4: Should a path under the home folder be shown as `~/…` or in full?**

   Priya runs `yolo check` and is told to fix `/home/priya/.config/yolo-jail/config.toml`; one launch
   later a pack conflict names `~/.claude/settings.json`. Both forms print today. Inside a jail `~` is
   the jail's home, not hers, so an agent reading a host path as `~/…` can open the wrong file.

   - **A — `~/…` for paths under home, in full otherwise.** Shorter and familiar; a message read
     inside a jail about a host file names it in full.
   - **B — Always in full.** Never ambiguous; long, and it prints the user's name in every log.

   <!-- vantage: question id=OQ-MS4 leaning="A — shorter and familiar, with the in-jail host-path case named in full." -->

   _Leaning:_ A — shorter and familiar, with the in-jail host-path case named in full.

   **Answer:**
   > _(empty — fill in when decided)_

5. 💬 🤷 **OQ-MS5: Should a command's error start with `yolo <verb>:`?**

   Priya's agent runs `yolo apply --format json` inside a script and the transcript shows one red
   line. Today `yolo apply`'s errors start `yolo apply:`, while `yolo pack add`'s host-run message
   starts mid-sentence and a launch line has no prefix at all.

   - **A — A verb's errors start `yolo <verb>:`; launch lines do not.** The transcript says which
     command spoke, as `git` and `go` do.
   - **B — No prefix; the sentence starts with yolo.** One voice everywhere; a log line loses which
     command printed it.

   <!-- vantage: question id=OQ-MS5 leaning="A — in a log or an agent transcript, the prefix is the only thing naming which command spoke." -->

   _Leaning:_ A — in a log or an agent transcript, the prefix is the only thing naming which command spoke.

   **Answer:**
   > _(empty — fill in when decided)_
