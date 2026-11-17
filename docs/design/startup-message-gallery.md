---
title: "A storybook for startup messages: see each line, where it comes from, and how it reads"
date: 2026-11-17
status: draft
stage: DESIGN
next: "Review the Stage 1 design without treating OQ-SMG1 as a prerequisite; planning and implementation require separate authorization. The maintenance-rule choice gates only Stage 4."
tags: [design, startup, messages, terminal, gallery, storybook, dev-tooling]
summary: "Proposed local startup-message gallery: package-owned Go caller captures, committed JSON without line numbers, and ignored static HTML with source-bound comparison panes. No message rewrite, implementation authorization, public deployment or quiet launch."
---

# A storybook for startup messages: see each line, where it comes from, and how it reads

**Status:** 2026-11-17. Design only; nothing is built or authorized for implementation. The [source grounding](../research/startup-message-gallery-grounding.md) retains the bounded survey at `a2b6554ce4439fb695ccdf85ab9a9649a0f9ad2c`; the cited capture, renderer and scope-gate seams are unchanged at `7883c182dd61aaf53d0b3e0c934f39ddfc569bff` and were checked for publication. New package, file and recipe names are proposals. [Unverified assumptions](#unverified-assumptions) remain engineering checks, not owner decisions.

> **In short.** Review startup output through captures of real Go callers in synthetic worlds, not a second layer of message wording. A local static page makes bytes, origins and before/after differences visible without claiming to reproduce every terminal.

**Why it matters.** Startup output can be noisy enough to go unread. Existing caller assertions help prevent regressions, but do not offer a browsable view of cumulative output and source ownership.

**The shape.** [What exists and why it is not enough](#what-already-exists-and-why-it-is-not-enough); [the architecture](#the-architecture-in-one-picture); [terms](#terms-of-art); nine decisions (D-SMG1 to D-SMG9); [stages](#design-stages); [acceptance scenarios](#acceptance-scenarios); [one owner question](#open-questions).

**Needs your ruling:** [OQ-SMG1](#OQ-SMG1). It gates Stage 4 only.

**Reads with:** [`report-tiers.md`](../reference/report-tiers.md) ([OQ-RO3](../reference/report-tiers.md#why-its-this-way): no quiet launch; which details are required), [`happy-path-principle.md`](../reference/happy-path-principle.md) (every stop names the next step), [`plain-language-principle.md`](../reference/plain-language-principle.md), [`message-style.md`](message-style.md) (draft style rules, not settled), [the message audit](../research/message-rewrites/README.md) (breadth; its line citations are a historical snapshot), and the [approval gallery](approval-experience/gallery/index.html) whose machinery this design copies.

---

## What this decides, and what it does not

It decides how startup messages become visible, traceable and reviewable:

- who owns scenario data;
- how evidence is captured;
- how source mappings stay honest;
- what the preview shows;
- what goes red when a caller is deleted;
- how generation stays free of side effects.

It does not:

- change any message wording, or fix the misleading `.git/config` attribution described in [Stage 1](#design-stages) — the gallery makes that visible, and a later change fixes it;
- add a quiet mode, a density switch, or any way to drop a required disclosure ([OQ-RO3](../reference/report-tiers.md#why-its-this-way) and the pack read/exec trust disclosures hold);
- require converting every emitter to a new logging framework or event bus;
- authorize building the gallery now, or reordering the broker/MCP priorities;
- settle the open taste questions in [`message-style.md`](message-style.md);
- publish anything. The gallery is a local development tool: the page is generated into an ignored directory and served on loopback. Putting it on the public docs site is separate future work with its own design;
- choose broker permission semantics. The [scoped broker design](scoped-broker-policy.md) owns that separate contract. The gallery records what the code does.

## What already exists, and why it is not enough

The [bounded grounding survey](../research/startup-message-gallery-grounding.md) compared renderer fixtures, caller captures and existing event wiring. Its representative domains are not an exhaustive inventory.

| Existing machinery | What it does | Why it is not the message gallery | What this design reuses |
| :--- | :--- | :--- | :--- |
| `internal/approvalfixtures` (`gallery.go::Gallery`, `fixture.go`, `manifest.go::Fold`, `Encode`) | Static `html/template` gallery of approval requests and notifications, with evidence classes and sanitized-name validation | Its subject is request objects and notification renderings, not terminal streams. It has no stream, width, call-stack or source mapping. | The whole pattern: fixture schema with supported/synthetic and capture/preview labels, one fold route, canonical JSON, static HTML, validation of sanitized spellings |
| `Justfile` `dev-storybook` | Refreshes the approval gallery through focused tests and serves it on `127.0.0.1:8765` | Serves one committed directory about approvals | The recipe shape: focused `go test`, then Python's HTTP server bound to loopback |
| `internal/cli/approvalcapture_test.go::TestApprovalCapture` and `YOLO_UPDATE_APPROVAL_FIXTURES=1` | Drives production constructors against fake worlds and compares with committed fixtures | Captures request fields, not what the launch prints | The capture-test idiom: compare by default, rewrite only under an explicit update variable |
| Caller tests in `internal/cli/run` (`configapproval_test.go`, `brokeredscope_test.go`, `launchflagdisclosure_test.go`, `patchedtreescallsite_test.go`) | Drive `changePrompter`, the gate and `Run` with fakes, and assert on output and records | Assertions are substring checks inside tests. Nobody can look at the output. | Their fakes and entry points are where capture scenarios attach |
| `internal/tty/inventory_test.go` | AST inventory of ANSI emitters and terminal probes | Inventories escapes, not human messages | The AST-inventory technique, for the emit-site census |
| `internal/richtext`, `internal/tty`, `internal/termsafe`, `internal/progress` | Markup to ANSI, color and width gates, visible-control safety, clocked progress | Renderers and gates. None is a catalogue or a source mapper. | Production code under capture; `progress.Config` Now/Tick/Width seams for deterministic frames |
| `internal/approvalqueue/web` | Plain embedded HTML/JS/CSS with no build step | It has real approval function and is not a harmless preview | Precedent only: hand-written script, no bundler |
| `config.Sources`, `Fixture.SupportNote` | Locates user config values; a prose note naming a code path | Locates the user's config, not the Go producer. The note is not compiler-checked. | Kept as a distinct "config origin" column, never merged with emitter |

None of the inspected machinery combines caller-written bytes, stream and physical emit-call identity into a browsable capture. That combination is the proposed new mechanism; the surrounding fixture and static-page patterns already exist.

## The architecture in one picture

```
 scenario (Go, in the owning package's _test.go)
   │  drives the production ENTRY with a fake world, once per variant
   ▼
 recorder (io.Writer wrappers handed to production code)
   │  each Write → segment {stream, transient, bytes, call stack}
   ▼
 capture  internal/messagegallery/captures/<domain>/<id>.json   ← committed, no line numbers
   │  compared on every `go test`; rewritten only under the update variable
   ▼
 generator (Go, html/template)  +  source index (go/parser over the working tree)
   │  resolves symbols to file:line now, matches wording to string literals
   ▼
 static page  .yolo/durable/message-gallery/index.html           ← not committed
   │
   ▼
 just dev-messages   (regenerate, then serve on 127.0.0.1)
```

## Terms of art

These gallery-specific meanings are **coined here**. They describe proposed evidence records, not runtime message APIs. Standard interfaces such as [`io.Writer`](https://pkg.go.dev/io#Writer), Go's [abstract syntax tree (AST)](https://pkg.go.dev/go/ast), [`runtime.Callers`](https://pkg.go.dev/runtime#Callers) and [`html/template`](https://pkg.go.dev/html/template) keep their Go meanings.

| Term | Meaning here |
| :--- | :--- |
| **Scenario** | One named situation: a fake world plus one production entry call. It has a stable ID, a title, a domain and a story. Example: `scope/declaration-only-change`. |
| **Story** | One or two plain sentences saying why a user would see this. Authored in the scenario. |
| **Entry** | The production function the scenario calls. The claim "the real caller emits this" is a claim about the entry. |
| **Variant** | One run of a scenario under a named terminal environment: width, TTY or not, color or `NO_COLOR`, platform. Each variant is a real run, never a transformation of another. |
| **Segment** | One `Write` call as recorded: stream, bytes, whether it was transient, and its call stack. |
| **Stream** | Where bytes went: `stderr`, `stdout`, `log-only`, `boot-log`. Kept apart because production keeps them apart. |
| **Transient** | Bytes written through `progress.TransientWriter.WriteTransient`: live redraws that never reach a persistent log. |
| **Emitter** | The innermost production function on a segment's stack that is not rendering plumbing or the standard library. Found automatically. |
| **Emit site** | One call expression inside the emitter that writes output. Its identity is the enclosing top-level declaration plus its ordinal among that declaration's emit calls in source order, for example `run.(*changePrompter).PromptReport#3`. Two prints in one function are two sites. |
| **Attribution** | How a segment is tied to the entry: `sync`, `driven` or `async`. See [D-SMG2](#d-smg2-evidence-is-captured-from-production-callers-in-three-labelled-classes). |
| **Mark** | A named event a fake records through the harness when production calls it, with its stack. Used as the "before effect" anchor for required facts and as the witness for non-synchronous segments. Not an authored claim that an effect happened. |
| **Witness** | An earlier mark whose entry-backed production stack and exclusive writer handoff tie a later non-synchronous segment to the entry. Not a manually declared origin. |
| **Wording site** | The function holding the string literal the text came from. Often differs from the emitter: `scopeBlock` words a block that `changePrompter.PromptReport` prints. |
| **Capture** | The committed JSON record of a scenario's variants and segments. |
| **Evidence class** | How much a capture proves: `caller`, `helper`, or `native`. See [D-SMG2](#d-smg2-evidence-is-captured-from-production-callers-in-three-labelled-classes). |
| **Narrative** | A capture of one entry that prints a whole startup or a long run of it, shown in order with every line's domain marked. |
| **Required fact** | A detail the scenario asserts must appear on a terminal-visible stream, optionally before a named effect. Used for trust disclosures and next steps. |
| **Census** | An AST list of emit sites in the startup packages, limited to a published list of call forms. Used to say honestly what the gallery does not cover. |

## The decisions

### D-SMG1. Scenarios live with the code they drive; captures live in one place

A scenario is Go, in a `_test.go` file of the package that owns the entry. It has to be there: entries such as `changePrompter` are unexported, and the fakes already exist in those test files.

A scenario declares:

- ID, title, domain (the [thirteen representative producer domains](../research/startup-message-gallery-grounding.md#5-representative-producer-domains)), and story;
- the entry symbol and the evidence class;
- `supported` or `synthetic`, in the approval-fixture sense;
- the variants to run;
- required facts, and for a refusal the expected next step;
- effect expectations: files that may change, commands the fake exec seam may see.

A small shared package, proposed as `internal/messagegallery`, owns the schema, the recorder, validation, canonical encoding and the generator. Captures are written to one directory, proposed as `internal/messagegallery/captures/<domain>/<id>.json`, so the generator reads one tree.

A central scenario file was rejected. It could not reach unexported entries, and it would become the second place every message change has to be made.

Each capture names its owning package and test function. A gate in `internal/messagegallery` checks both directions with `go/parser`: every capture ID appears as a literal in a scenario registration in the named package, and every registration has a capture. An orphaned capture or an uncaptured scenario is red.

Unsupported or missing cases are data, not silence. A scenario may be registered with `status: uncovered` and a reason: "emitter writes to `os.Stderr` directly; no writer seam", or "needs a container". It then appears in the gallery as a labelled empty tile. This follows the approval gallery's "missing" evidence state.

### D-SMG2. Evidence is captured from production callers, in three labelled classes

| Class | What ran | What it proves | What it does not prove |
| :--- | :--- | :--- | :--- |
| `caller` | The production entry, with fakes for effects | This entry, in this situation, wrote these bytes to these streams in this order | Anything about a real terminal or a real launch outside the faked seams |
| `helper` | A pure function such as `approvalPresentationLines` or `PatchedForkLine`, called directly | How the wording behaves across widths and awkward values | That any caller prints it. The tile says so. |
| `native` | A real terminal, captured by a human-run script. Later stage; optional. | What one terminal actually drew | Anything about other terminals |

The recorder enforces the difference between `caller` and `helper` per segment. Every segment gets one attribution, computed from its stack and never declared.

| Attribution | Stack shape | What it proves alone |
| :--- | :--- | :--- |
| `sync` | The declared entry is on the stack, and every frame between the entry and the emitter is production code | The entry caused this write |
| `driven` | The frame directly above the production code is the harness's own clock driver, which advances the fake clock. The entry is not on the stack. | Nothing alone: a test advanced time |
| `async` | A production goroutine. No test frame anywhere on the stack, and no entry. | Nothing alone: some production goroutine wrote |

Any other stack shape is red. That includes a test that calls the printer itself, a test that starts the pump goroutine itself, and test code other than the clock driver sitting above production frames.

A `driven` or `async` segment is accepted in a `caller` scenario only with a **witness**. The scenario names a mark, and the recorder checks three things:

- the mark was recorded earlier in the same variant;
- the mark's own stack has the entry on it, with only production frames between the entry and the fake that recorded it;
- the segment arrived on a writer the scenario handed to the entry and to nothing else.

The mark is the proof that the entry started the work: the fake subprocess was launched, or the step body was run, by production code under the entry. The segment is then shown as "caller, asynchronous, witnessed by `<mark>`". Its emitter is the pump or redraw function, labelled as such.

A scenario whose segments are all `driven` or `async` with no witness cannot claim `caller`. It is `helper`, and the tile says "pump only". This stops a standalone pump or progress test from standing in as caller proof.

Stage 1 accepts `sync` segments only; any other attribution is red there. Witnessed segments arrive with the progress scenarios in Stage 2. Neither needs an event bus or any change to emitters.

A `helper` scenario must name the `caller` scenario it supplements, or carry `caller: uncovered`. The gallery shows that as a badge.

Captures are compared on every ordinary `go test` run. They are rewritten only under one update variable, proposed as `YOLO_UPDATE_MESSAGE_CAPTURES=1`. The update variable never touches required facts or effect expectations, because those are Go source.

An existing substring assertion in a caller test does not have to be converted. Capture scenarios are added beside those tests, and reuse their fakes.

### D-SMG3. Source mappings come from the call stack and the syntax tree, never from hand-written line numbers

A gallery line shows four kinds of origin, kept separate:

| Mapping | Where it comes from | How it stays honest |
| :--- | :--- | :--- |
| **Emitter** and **caller chain** | `runtime.Callers` inside the recorder's `Write`. Frames from the rendering plumbing (`internal/richtext`, `internal/progress`, the console printer wrappers, the recorder, the tee writers) and the standard library are skipped; the first remaining frame is the emitter. The chain runs up to the entry, or to the goroutine root for a non-synchronous segment. | Automatic. Recomputed on every test run. The chain is stored as declarations and files, without line numbers. |
| **Emit site** | The emitter frame's file and line, taken at capture time, are mapped through `go/parser` to a call expression, and then to an ordinal. See "How an emit site is identified" below. | Stored as declaration plus ordinal, never as a line. Moving code does not change it; adding or removing an emit call in that declaration does. |
| **Wording site** | A source index built at generation time. `go/parser` walks non-test files under `internal/`, splits each string literal at its format verbs, and matches the static fragments against the captured plain text. The longest match wins; ties are all shown. | Each capture is matched only against the source version it belongs to ([D-SMG5](#d-smg5-the-page-is-static-html-generated-locally-and-not-committed)). A scenario may pin a wording symbol where the match is ambiguous. A working-tree pin that no longer matches is red. |
| **Line number** | The generator resolves the emit site's ordinal and the wording symbol to `file:line` by parsing the source version the capture belongs to | Never committed, so never stale. The line shown for an emit site is the line of that call expression, not the line where its function starts. |
| **Config origin** | Where the scenario's fixture config declares the value, from `config.Sources` where the entry carries it | Shown in its own column as "the setting that caused this", never as the source of the line |

#### How an emit site is identified

A function-level symbol cannot tell two prints in one function apart, so the capture stores a finer identity.

1. At capture time the recorder has the emitter frame's file and line. The capture test is compiled from the working tree, so that file is the source that ran.
2. The harness parses that one file and finds the enclosing top-level declaration. Closures belong to the declaration that contains them, so the compiler's `func1` names are not used.
3. Inside that declaration it lists the emit calls in source order. An emit call is a call expression in one of the census's listed forms: a `richtext.Printer` method, the launch console's `print`/`printf` wrappers, `fmt.Fprint`, `fmt.Fprintf` or `fmt.Fprintln`, `io.WriteString`, or a direct `Write` on a writer. The planner fixes the list.
4. The frame's line selects the innermost listed call whose source span contains it. The site is that call's ordinal, written `pkg.Decl#N`, with the call form beside it.

Three outcomes are not hidden:

- **Ambiguous.** Two listed calls share the selected line, for example two prints on one line. The segment stores both ordinals and is marked ambiguous. Neither site counts as covered.
- **Census miss.** The line selects no listed call. The capture test is red. The remedy is to add the call form to the census list, which keeps the census honest against what actually runs.
- **Ordinal shift.** Adding or deleting an emit call renumbers the later sites in that declaration. Captures that touch those sites change and must be updated. The diff shows the renumbering.

Only the files of observed emitter frames are parsed during capture.

Three limits are stated in the page, not hidden:

- A segment written from a goroutine that pumps a subprocess pipe, or from a clock tick, has a stack that does not reach the entry. Its attribution is `async` or `driven`, and it counts as caller evidence only with a witness mark ([D-SMG2](#d-smg2-evidence-is-captured-from-production-callers-in-three-labelled-classes)). Its emit site is the pump's or redraw's own call, labelled "asynchronous". The page never presents it as the site that worded the message.
- An emitter that writes to the process's `os.Stderr` directly cannot be recorded. The harness does not redirect file descriptors, because that loses the stack and hides effects. Such sites are `uncovered` until they take a writer.
- Generated shell bytes printed inside the jail are not Go printer output. They are a separate later domain with their own evidence.

### D-SMG4. The preview shows the bytes; it does not pretend to be a terminal

A capture stores raw bytes per segment. [ANSI control sequences](https://en.wikipedia.org/wiki/ANSI_escape_code) include SGR (Select Graphic Rendition, styling) and OSC (Operating System Command, terminal operations); the preview interprets only the explicitly supported subset. The page shows each variant in up to five views.

| View | Content | Honest claim |
| :--- | :--- | :--- |
| **Styled** | Bytes converted by a small Go converter that understands only the SGR codes `richtext` emits, carriage return, and erase-line | "What these bytes mean under the subset yolo emits" |
| **Escapes visible** | Every control byte shown as a visible token, in the manner of `termsafe.Visible` | "Exactly what was written" |
| **Plain** | The no-TTY variant's own bytes | "What a pipe or log receives". It is a real capture, not the styled view with color stripped, because production chooses differently when there is no terminal. |
| **Final frame** | For progress: the screen state after applying carriage returns and erases at each clock step | "What remained on the line at each tick" |
| **Persistent log** | Non-transient segments only | "What the launch log keeps" |

Rules for the preview:

- **Width.** Each variant is drawn in a box exactly N columns wide with wrapping off. Text past column N is drawn in a marked overflow zone labelled "a terminal would wrap or cut here". The browser never rewraps. Where production wraps for itself (`wrapApprovalLine`), the wrapping seen is production's.
- **Unicode.** The page states each line's byte length and rune count. Lines containing anything beyond ASCII are flagged "column count approximate". Scope-table misalignment from byte-length padding is shown as it is, not corrected in CSS.
- **Unknown sequences.** Any escape the converter does not know, including every OSC sequence, is never interpreted. It is drawn as a visible token, and the validator rejects it unless the scenario declares `hostile-input` to exercise `termsafe`.
- **Links.** `richtext` has no hyperlink facility, so there is no terminal link to preview. Every clickable link on the page is produced by the generator from the source index. Captured bytes never become a link target.
- **Streams.** `stderr`, `stdout`, `log-only` and `boot-log` are separate lanes with a merged-order toggle. Log-only lines are marked "not on screen".
- **Clock.** Progress scenarios drive `progress.Config` Now and Tick. Each segment carries its fake-clock offset, so warm silence, the two-second grace, and the fifteen-second heartbeat are visible as time and not guessed.

The variant matrix for the first slices is: width 0 (no terminal), 60, 80 and 120; color on and `NO_COLOR`; platform where the entry branches on it.

One production seam is missing. `approvalPresentationWidth` combines an injected terminal predicate with a real `os.Stderr` probe, so an 80-column approval variant cannot be produced today. The gallery needs a small injected width source there. Until it exists, approval variants are labelled `width: not probed`. A fake 80-column claim is not allowed.

### D-SMG5. The page is static HTML, generated locally and not committed

The generator is Go with [`html/template`](https://pkg.go.dev/html/template), as in `approvalfixtures`. There is no React, no npm, no bundler and no terminal emulator dependency. Unlike the approval gallery, the page carries one small hand-written script, embedded with no build step. It does search, filtering and pane selection only. With scripts off, every tile is still visible in full. A content security policy in the page forbids network loads.

The committed artifact is the capture JSON. The HTML is written to an ignored directory, proposed as `.yolo/durable/message-gallery/`.

A committed HTML page was rejected for two reasons. It would carry line numbers and so go stale on every unrelated edit. It would also add a large generated diff to every wording change. The JSON diff is the reviewable record: a pull request that changes a message shows the changed bytes.

A new recipe, proposed as `just dev-messages`, runs the capture tests in compare mode, generates the page, and serves it on loopback beside `dev-storybook`. It starts nothing else.

The generator is a library inside `internal/messagegallery`. It has no `main` package and is not a `yolo` subcommand. It runs through one operator-only test, proposed as `TestWriteGallery`, which does nothing unless an output directory is named in the environment. The recipe is the only caller. This is the same route the approval gallery uses: generation through a focused `go test`. The import gate in [D-SMG8](#d-smg8-generation-has-no-side-effects-and-the-harness-checks-rather-than-trusts) therefore has no exception to make.

If the compare step fails because source was edited and captures were not updated, the recipe stops and prints the update command for the failing package. It does not serve a page built from stale captures.

The page offers:

- **Search** over wording, scenario title, domain, emitter and entry.
- **Filters** by domain, evidence class, stream, outcome (proceeds, asks, refuses), and "has findings".
- **Source navigation.** Each segment shows `path:line` and symbol as copyable text, for emitter and wording site. It is also an editor link built from a template, with a VS Code style default and an environment override. Absolute paths are safe because the page is local and uncommitted.
- **Side-by-side comparison** in three modes:
  - two variants of one scenario, for example 60 against 120 columns;
  - two scenarios, for example declaration-only change against genuine root addition;
  - working tree against a baseline commit, as set out in "Baseline comparison" below.
- **Findings**, computed and not hand-written: lines past the variant width, repeated near-identical lines, a refusal whose last screen line is not the next step, a required fact present only on a log-only stream.

#### Baseline comparison

The recipe takes a baseline ref. The usual value during an edit is `HEAD`: the committed state against the uncommitted edit.

**Each pane is bound to one source version.** Working-tree captures resolve against working-tree source. Baseline captures resolve only against source read from the baseline commit. The generator never looks up an old capture's site or wording in current source.

**Bounded Git reads.** The generator's baseline reader supports four read-only operations, plus the bounded history read below, and nothing else:

- resolve the ref to a full commit;
- list the capture directory at that commit;
- read a capture file at that commit;
- read a source file at that commit.

The source files read are only those a baseline capture names: the files of its emit sites, its caller chain, and its pins. There is no repository snapshot and no hash manifest. The commit is the version.

**What the baseline pane shows.**

| Item | Baseline pane |
| :--- | :--- |
| Emit site | `pkg.Decl#N`, resolved to `path:line at <short commit>` from the baseline file. Plain copyable text with the matching `git show` command. Never an editor link. |
| Wording site | Matched only within the baseline versions of the files the capture names. No match shows "wording site not resolved at `<commit>`". |
| Pin | Checked against the baseline file. A failure shows "pin unresolved at `<commit>`". It never fails generation. |
| Fate of the site | One of: "same site in working tree", "declaration present, site renumbered", "declaration gone". Computed by looking up the identity in the working tree and shown as a note, not a link. |

**Failures on the baseline side never block the working side.** Only the working tree can turn the gallery red.

| Situation | Behavior |
| :--- | :--- |
| The ref does not resolve | The recipe stops before generating, names the ref, and says how to run without a baseline |
| The commit has no capture directory, or no capture for a scenario | The pane reads "no capture at `<commit>`"; the scenario is marked new |
| A scenario exists at the baseline and not now | Listed as removed, with its baseline pane |
| The baseline capture has a schema version the generator cannot read | The pane reads "capture schema N at `<commit>` is not readable by this generator" |
| A named source file is missing at the commit | That item reads "not resolved at `<commit>`" |
| The working tree has uncommitted changes | Normal. The working pane is headed "working tree: `<HEAD commit>` plus uncommitted changes". |
| The baseline ref is `HEAD` and the tree is clean | Every scenario is "unchanged", and the page says so |

Each capture carries a schema version number for this purpose.

Each tile also shows "capture last changed at `<commit>`" where Git history is available, from one bounded log read of that capture file. Captures are committed together with the source that produced them, so that commit is the exact source the recorded behavior belongs to.

### D-SMG6. Editing happens in Go; the loop is edit, regenerate, look, commit

There is no in-browser editing and no wording override layer. A staged wording that never passed through the caller is exactly the unfaithful preview this design exists to remove.

The loop:

1. Find the line in the gallery by searching its wording. Read its story and its emitter.
2. Follow the wording-site link and edit the Go source.
3. Run the update command for that package's capture tests. Required facts and effect expectations still run; a rewrite that drops a disclosure fails here.
4. Run `just dev-messages` with the baseline set to `HEAD`, or to the branch point for a longer change. Compare before and after at each width. The before pane resolves against the baseline commit's own source, so renaming or deleting the old emitter or its literal does not break the comparison.
5. Commit the source and the changed capture JSON together. The reviewer reads the byte diff.

Generated files have one owner each. Captures are written only by capture tests under the update variable. The page is written only by the generator. Nothing is hand-edited, and canonical encoding keeps diffs deterministic.

### D-SMG7. Deleting a caller turns a capture test red

A renderer golden stays green when the production call is deleted. A `caller` capture does not:

- The capture is recomputed through the entry on every test run. If the call is removed, its segments vanish and the comparison fails in the owning package.
- The entry-on-stack rule from [D-SMG2](#d-smg2-evidence-is-captured-from-production-callers-in-three-labelled-classes) stops a scenario from being "repaired" by calling the helper directly.
- Required facts fail independently of the byte comparison. Running the update variable cannot quietly accept a lost disclosure.
- Order is part of the capture. A required fact may be bound to "before effect X", where X is a mark, for example the fake spawn. A disclosure moved after the spawn is red even if the bytes are identical. This encodes the rule that disclosure precedes execution.
- Removing a required fact from a scenario is a source change a reviewer sees. The rule: it must cite the ruling that allows it.

Coverage is reported per emit site by the census. An AST pass lists the emit sites in the startup packages (starting with `internal/cli/run`, `internal/entrypoint`, and the message-producing parts of `internal/config`), using the same call forms and the same `pkg.Decl#N` identity as the recorder. The census and the captures therefore name sites the same way.

The census claims only its listed call forms. A function that builds a string and returns it has no emit site; it is found as a wording site. An emit form missing from the list cannot hide for long: the first capture that reaches it is a census miss and red ([D-SMG3](#d-smg3-source-mappings-come-from-the-call-stack-and-the-syntax-tree-never-from-hand-written-line-numbers)).

Each site has one status on the coverage page:

| Status | Meaning | Counts as covered |
| :--- | :--- | :--- |
| `covered` | Some `caller` scenario has a `sync` segment at this site | Yes |
| `witnessed` | Only `driven` or `async` segments reach it, each with a valid witness | Yes, shown separately |
| `helper-only` | Only `helper` scenarios reach it | No |
| `ambiguous` | Reached only through a line shared with another listed call | No |
| `uncovered` | No capture reaches it | No |

A captured site never covers its neighbor. If a function has two print calls and scenarios reach only the first, the second is `uncovered`, and deleting it turns nothing red; the coverage page showed it as unprotected beforehand. Deleting the first is red.

Whether coverage becomes a gate is [OQ-SMG1](#OQ-SMG1).

### D-SMG8. Generation has no side effects, and the harness checks rather than trusts

The capture harness is ordinary `go test` with three layers. It does not claim to be an operating-system sandbox.

1. **Entries with injected effects only.** A scenario may drive only an entry whose effects are already seams: fake exec, fake stores, an injected clock, injected writers. An entry that would start a container, an installer, a daemon or a network request is `uncovered`, not approximated.
2. **A scrubbed process environment, set inside the test.** The test sets its own `HOME` and config directories to a fixture tree under `t.TempDir()`, points `PATH` at an empty fixture directory, clears credential-bearing variables, and sets the flags that disable automatic capture, readiness probing and update checks. This is test-local. The developer's shell `HOME`, `TMPDIR`, `GOENV` and Go caches are untouched.
3. **Checks after the run.** The fixture tree is compared before and after, and only declared files may differ. This is also the evidence that an interactive "no" or end-of-input left the approval record unchanged. The fake exec log may contain only declared commands. Any undeclared effect fails the scenario.

Sanitization runs before a capture is written and again at generation:

- Paths must sit under the fixed fictitious roots, for example `/home/dev/code/app`, and names must come from the example set, as the approval fixtures require.
- A capture containing the real home directory, user name, host name or the temp root is rejected. Rewriting such a path after the fact would change widths, so the default is refusal. A scenario may declare `path-substituted`, and the variant then carries that label and makes no width claim.
- Token-shaped strings, non-example e-mail addresses and long opaque blobs are rejected by pattern.
- Scenario data is synthetic by rule. No real transcript, owner config, grant or log is ever an input.
- All text reaches the page through `html/template` escaping.
- `internal/messagegallery` holds the recorder, the validator and the generator as ordinary library files, so its own tests can exercise them. A gate checks two things: no non-test file outside `internal/messagegallery` imports the package, and the package is absent from the dependency list of the `yolo` binary. Scenario tests in other packages import it from `_test.go` files only. There is no generator `main` package to exempt.

### D-SMG9. A narrative shows a whole startup, with every line owned and counted

A narrative is one `caller` capture of an entry that prints many families in order. The first one is a `Run`-level scenario of the kind `patchedtreescallsite_test.go` already drives.

The page draws a narrative as one terminal column with a gutter. Each line carries its domain color, its scenario-family label and its emitter. Above it sits a tally: total screen lines, lines per domain, and lines that are required facts. A startup with 37 lines, 22 of them from forks and patches, is then a number on the page.

Filtering a narrative by domain dims the other lines and leaves them in place, so context is kept. Lines that satisfy a required fact carry a "required" badge linking to `report-tiers.md`. This is the gallery's only relationship to noise reduction: it shows hierarchy, grouping and wording candidates. It has no control that hides output from a real launch, and it proposes none.

Narratives are captured, not assembled. Stitching separate captures into an imagined launch would invent an order no code produces. Where a full launch cannot be faked yet, the narrative covers the part that can, and says where it stops.

## Design stages

Seed notes for the planner, not a plan. Each stage is useful alone, and none requires migrating other emitters.

| Stage | Contents | Why this order |
| :--- | :--- | :--- |
| **1. Scope-prompt trio** | Recorder, schema, validation, capture compare/update, generator, `dev-messages`, source index, emit-site identity, variant side-by-side, baseline comparison. `sync` segments only. Three `caller` scenarios through the approval gate that today's `changePrompter` serves: (a) a selected call broker whose capability is inactive or empty; (b) an active broker whose declaration metadata changed while its admitted roots did not, with workspace config unchanged; (c) a true root addition. Each has a control variant in which the identical state is already approved. Where a scenario asks, its variants are interactive no, end-of-input and unattended refusal. | The motivating confusion. It exercises streams, record preservation, the next step on refusal, and the emitter/wording split. |
| **2. Contrast set** | Config change with no/EOF/refusal; one before/after argv disclosure; fork and extension inventory with the attach contrast; one required-service failure; warm and cold progress with frames. `driven` and `async` segments with witness marks. Width seam for `approvalPresentationWidth`. First `helper` scenarios for pathological widths. | The packet's evaluation set. It adds transient output, the clock, and non-synchronous evidence. |
| **3. Whole startup** | First `Run`-level narrative, the tally, baseline comparison, computed findings, the census and coverage page | Needs Stage 1 and 2 families to exist before a narrative means anything |
| **4. Widening** | Per-domain expansion, entrypoint and boot-log lanes, the gate chosen under [OQ-SMG1](#OQ-SMG1), optional `native` captures, a shared index page with the approval gallery | Open-ended. Driven by which domains the owner wants to rewrite next. |

**The trio records behavior; it does not prescribe it.** Whether scenarios (a) and (b) prompt is the broker work's decision, not this design's. Their outcome (asks, does not ask, or refuses) is a recorded field of the capture, read from what the gate did. No required fact in (a) or (b) says a prompt must appear. The required facts in the trio are limited to what the existing caller tests already assert, for example that an interactive no leaves the approval record unchanged.

"Does not ask" is a first-class result. The tile shows the scenario, its story, the empty screen, and the unchanged record. It does not go blank or missing.

At the [source the packet inspected](../research/startup-message-gallery-grounding.md#2-the-scope-prompt-trio), unchanged in the named scope-gate seams at `7883c182`, a non-dormant source in scenario (b) asks. The header reads "Repository scope changed", the root counts are zero, and `scopeBlock` attributes the declared-root source to "this workspace's .git/config". If Stage 1 lands on that behavior, the capture records it as it is, and the capture commit pins the source version it belongs to. It does not fix the message or the gate.

If the broker work lands first, or later, and (a) or (b) stop prompting, the capture update shows the change. The baseline comparison then reads "asked at `<commit>`" against "does not ask" now. The gallery does not go red to demand the old prompt back, and it does not keep the old capture alive.

The planner captures whatever the tree does on the day Stage 1 is built. Which of the two states that is, is not a precondition.

## Decision Ledger

The architecture entries below are the draft's engineering choices, **not implementation authorization**.
Every gallery mechanism is unbuilt. The standing constraints are already authoritative; source links
below identify their authority rather than claiming the proposed harness enforces them today.

| ID | Ruling / Decision | Date | Settled in | Built |
| :--- | :--- | :--- | :--- | :--- |
| — | No quiet launch or suppression of required trust disclosures | Standing | [Report contract](../reference/report-tiers.md#why-its-this-way); [scope boundaries](#what-this-decides-and-what-it-does-not) | Existing rule; gallery unbuilt |
| — | Design only; no gallery code or priority change to broker/MCP work | 2026-11-17 | [Scope boundaries](#what-this-decides-and-what-it-does-not) | Publication only |
| D-SMG1 | Package-owned scenarios, centralized captures and two-way orphan gate | 2026-11-17 | [Scenario ownership](#d-smg1-scenarios-live-with-the-code-they-drive-captures-live-in-one-place) | — |
| D-SMG2 | Caller/helper/native evidence; computed sync/driven/async attribution; prior witness for non-synchronous segments; compare by default | 2026-11-17 | [Evidence](#d-smg2-evidence-is-captured-from-production-callers-in-three-labelled-classes) | — |
| D-SMG3 | Emitter from stack, physical emit-call identity as declaration plus ordinal, wording from source; no committed lines | 2026-11-17 | [Source mapping](#d-smg3-source-mappings-come-from-the-call-stack-and-the-syntax-tree-never-from-hand-written-line-numbers) | — |
| D-SMG4 | Byte-faithful views, marked overflow, no unknown-escape interpretation | 2026-11-17 | [Preview](#d-smg4-the-preview-shows-the-bytes-it-does-not-pretend-to-be-a-terminal) | — |
| D-SMG5 | Local Go static HTML; operator-only generator test; source-bound panes through bounded read-only Git | 2026-11-17 | [Local generation](#d-smg5-the-page-is-static-html-generated-locally-and-not-committed) | — |
| D-SMG6 | Edit in Go; no UI wording overrides | 2026-11-17 | [Edit loop](#d-smg6-editing-happens-in-go-the-loop-is-edit-regenerate-look-commit) | — |
| D-SMG7 | Captured-call deletion, lost disclosures and ordering regressions fail; per-site census with five statuses | 2026-11-17 | [Drift controls](#d-smg7-deleting-a-caller-turns-a-capture-test-red) | — |
| D-SMG8 | Injected effects, test-local environment and post-run checks; refuse-first sanitization and production-import gate | 2026-11-17 | [Safety](#d-smg8-generation-has-no-side-effects-and-the-harness-checks-rather-than-trusts) | — |
| D-SMG9 | Narratives captured, never fabricated by stitching; tallies/dimming without launch-output hiding | 2026-11-17 | [Narratives](#d-smg9-a-narrative-shows-a-whole-startup-with-every-line-owned-and-counted) | — |
| OQ-SMG2 | Withdrawn: public publication is outside scope; local-only, with any future publication separately designed | 2026-11-17 | [Local generation](#d-smg5-the-page-is-static-html-generated-locally-and-not-committed) | — |
| — | Scope trio records actual gate behavior, not prompt-policy assertions; broker authority stays separate | 2026-11-17 | [Stages](#design-stages) | — |

## Acceptance scenarios

Each is a statement a later implementation must make true. "Red" means a failing `go test`.

### Discovery and source navigation

| # | Scenario | Expected |
| :--- | :--- | :--- |
| A1 | Search the gallery for "Repository scope changed" | The scope scenarios that actually print it appear. The header's emitter is `changePrompter.PromptReport`; scope-block segments instead name `printScopeBlock`, with `PromptReport` in their caller chain and `scopeBlock` as wording site. |
| A2 | Open the active-metadata-change scenario while the tree still prompts for it | The story explains that a changed declaration, not a root change, caused the prompt. The block's wording site is `scopeBlock`. The config-origin column is distinct from both. |
| A3 | Move a wording function fifty lines down and regenerate | No capture changes. The page's line links point at the new lines. |
| A4 | Rename the emitting function | The owning package's capture comparison is red until captures are updated. The diff shows only the symbol. |
| A5 | A wording match is ambiguous | All candidates are listed. A scenario pin picks one. A pin that stops matching is red. |
| A6 | A segment is written from a pump goroutine, with a valid witness mark | It is labelled "caller, asynchronous, witnessed by `<mark>`". Its emit site is the pump's call, labelled asynchronous. |
| A7 | A function has two `Fprintln` calls; a scenario reaches only the first | The capture names site `#1`. The coverage page shows `#1` covered and `#2` uncovered. |
| A8 | In that function, delete the second, uncaptured call | No capture test is red. The census count drops by one. Nothing that was claimed as covered was lost. |
| A9 | In that function, delete the first, captured call | The owning package's capture comparison is red |
| A10 | Insert blank lines or a comment above either call | No capture changes. The page's line for each site moves to the call's new line. |
| A11 | Add a new emit call above a captured one in the same declaration, on a branch the scenario does not execute | Captures touching the later sites are red until updated. The diff shows only the renumbering. |
| A12 | Two listed emit calls sit on one source line | The segment is marked ambiguous with both ordinals. Neither site counts as covered. |
| A13 | A capture reaches a write whose call form is not on the census list | Red, as a census miss, naming the file and call |
| A14 | A site inside a closure | Its identity uses the enclosing top-level declaration, not a compiler-generated closure name |
| A15 | Open any emit-site link | It opens the line of the emitting call expression, not the first line of its function |

### Faithful capture and caller evidence

| # | Scenario | Expected |
| :--- | :--- | :--- |
| B1 | Delete the production call that prints the argv disclosure | The disclosure scenario is red in `internal/cli/run`, even though the formatter's own tests pass |
| B2 | Change a scenario to call the printer helper directly while claiming `caller` | Red: the entry is not on the stack |
| B3 | Move a pack host-access disclosure to after the fake spawn, with the same bytes | Red: the required fact is bound to "before spawn" |
| B4 | Reword a refusal so the remedy command is gone, then run the update variable | Still red: the next-step fact is Go source, not capture |
| B5 | Answer no, or give end-of-input, at the scope prompt | The fixture tree diff shows the approval record unchanged. The capture records the outcome line. |
| B6 | Unattended refusal variant | The output contains cause, outcome and next command. The last screen line is the next step, or a finding says it is not. |
| B7 | The control variant: an identical state already approved | The capture shows no prompt. The tile says "does not ask" rather than being absent. |
| B8 | A `helper` scenario with no caller scenario | The tile carries "no caller evidence". It is counted as uncovered in the census. |
| B9 | A capture file with no registered scenario, or the reverse | Red in `internal/messagegallery` |
| B10 | Run the capture tests twice with no source change | Byte-identical captures. No clock, temp-path or map-order noise. |
| B11 | A scenario's segments all come from a pump goroutine and it names no witness | It cannot claim `caller`. It is `helper`, and the tile says "pump only". |
| B12 | A test starts the pump goroutine itself and claims `caller` | Red: a test frame is on the asynchronous stack |
| B13 | A witness mark is named but was recorded without the entry on its stack, or after the segment | Red |
| B14 | A cold progress step redraws when the harness clock driver advances time, and a mark shows the step body ran under the entry | The segments are `driven`, accepted, and labelled as witnessed |
| B15 | Test code other than the harness clock driver calls production redraw code directly | Red |
| B16 | Any `driven` or `async` segment in a Stage 1 scenario | Red: Stage 1 is synchronous only |
| B17 | The broker work changes the active-metadata-change scenario so that it no longer prompts; captures are updated | The capture's outcome becomes "does not ask". No required fact fails. The tile renders the result in full. |
| B18 | The inactive-capability scenario, whatever it does at the commit where Stage 1 lands | The capture records the observed outcome. The scenario asserts none. |
| B19 | The root-addition scenario | Assertions are limited to those the existing caller tests already make. The gallery adds no new permission rule. |

### Terminal behavior

| # | Scenario | Expected |
| :--- | :--- | :--- |
| C1 | Color variant against the `NO_COLOR` variant | Both are real runs. Literal brackets such as `[y/N]` survive in both. |
| C2 | No-terminal variant | Shown as the plain view. It is not derived by stripping the styled one. |
| C3 | A line longer than the variant width | Drawn with a marked overflow zone and a finding. The browser does not rewrap it. |
| C4 | Approval text at width 60 before the width seam exists | The variant is labelled `width: not probed`. No 60-column claim is made. |
| C5 | Approval text at width 40 after the seam exists | Production's `wrapApprovalLine` output is shown: values whole, escape tokens unsplit |
| C6 | A scope table with a non-ASCII name | The misalignment is visible. The line is flagged "column count approximate". |
| C7 | A fixture value containing an OSC clipboard sequence, declared `hostile-input` | The styled view shows a visible token. Nothing is interpreted. No link is created from it. |
| C8 | The same value without the declaration | The validator rejects the capture |
| C9 | Warm step | The capture shows no screen output. The tile says "silent by design". |
| C10 | Cold step | Frames at the grace, heartbeat and result ticks are shown with fake-clock offsets. The persistent-log view omits the transient redraws. |
| C11 | A successful check recorded log-only | It appears in the log-only lane, marked "not on screen", and is not counted in the screen tally |
| C12 | A payload on stdout | It sits in its own lane. The merged view preserves recorded order. |

### No side effects and sanitization

| # | Scenario | Expected |
| :--- | :--- | :--- |
| D1 | A scenario's entry tries to run an undeclared command | Red: the fake exec log has an unexpected entry |
| D2 | A scenario writes a file it did not declare | Red: fixture tree diff |
| D3 | A capture contains the developer's home directory, user name or the temp root | Rejected before writing |
| D4 | A capture contains a token-shaped string or a non-example e-mail address | Rejected |
| D5 | Run `just dev-messages` | No agent, installer, container, daemon, provider or authenticated request is started. The server binds to loopback only. |
| D6 | Inspect imports | No non-test file outside `internal/messagegallery` imports it. It is absent from the `yolo` binary's dependency list. No generator `main` package exists. |
| D7 | Fixture text contains `<script>` | Escaped in the page. The content security policy would block it in any case. |
| D8 | After a capture run, inspect the developer's environment | Shell `HOME`, `TMPDIR`, `GOENV` and the Go caches are as they were |

### Review and comparison

| # | Scenario | Expected |
| :--- | :--- | :--- |
| E1 | Compare declaration-only change with genuine root addition | Two panes at the same width. Differing lines are marked. |
| E2 | Edit a message, update, and run with baseline `HEAD` | Before and after panes for every variant of the affected scenarios. Unchanged scenarios are collapsed. The working pane is headed with the commit plus "uncommitted changes". |
| E3 | Open the first narrative | Every line has a domain and an emitter. The tally shows lines per domain and how many are required. |
| E4 | Filter the narrative to forks and patches | Other lines dim and stay in place. Required lines keep their badge. |
| E5 | Look for a way to hide required disclosures from a real launch | There is none. The gallery changes no launch behavior. |
| E6 | Open the coverage page | Emit sites are listed by domain and by `pkg.Decl#N` as covered, witnessed, helper-only, ambiguous or uncovered. Uncovered tiles give their reason. |
| E7 | Disable scripts in the browser | All tiles and views are present. Only search and filter are missing. |
| E8 | Rename or delete the old emitter, update, and compare with baseline `HEAD` | Generation succeeds. The before pane shows `path:line at <commit>` from the baseline file, with no editor link, and the note "declaration gone". |
| E9 | Edit a pinned literal so that the baseline's pin no longer matches current source | Generation succeeds. The baseline pin is checked against the baseline file only. The working pin is checked against the working tree. |
| E10 | Make a working-tree pin stop matching | Red on the working side, with the pin named |
| E11 | Give a baseline ref that does not resolve | The recipe stops before generating, names the ref, and says how to run without a baseline |
| E12 | Baseline commit predates the gallery, or lacks one scenario | Panes read "no capture at `<commit>`". The scenario is marked new. Generation succeeds. |
| E13 | A scenario exists at the baseline and was since deleted | It is listed as removed, with its baseline pane |
| E14 | Baseline capture has an older schema version | The pane reads that the schema is not readable. Generation succeeds. |
| E15 | Edit source without updating captures, then run `just dev-messages` | The recipe stops and prints the update command for the failing package. No page is served from stale captures. |
| E16 | Inspect the Git commands the generator runs | Only: resolve a ref, list the capture directory at a commit, read a named file at a commit, and one log read per capture file. No write, no checkout, no fetch. |
| E17 | A prompt existed at the baseline and does not now | The comparison reads "asked at `<commit>`" against "does not ask". Nothing is red. |
| E18 | Look for a publish, deploy or site-build step | There is none. Output goes to an ignored directory, and the server binds to loopback. |

### Incremental adoption

| # | Scenario | Expected |
| :--- | :--- | :--- |
| F1 | Stage 1 lands with three scenarios | The rest of the tree builds and tests unchanged. No emitter outside the scope prompt is modified. No scope or broker behavior is changed by the gallery. |
| F2 | A new domain adds its first scenario | It needs only a test in its own package and a writer seam it already has |
| F3 | A domain whose emitter writes straight to `os.Stderr` | Registered as `uncovered` with that reason. Nothing is faked to cover it. |

## Open questions

1. 💬 **OQ-SMG1: Should a new or changed startup message eventually require a gallery scenario?**

   A contributor adds a startup line and runs the tests. The proposed coverage page shows any
   uncaptured emit calls, but reporting alone does not prevent new gaps. Should later maintenance
   make those gaps fail tests? This is a standing rule for everyone who changes a message,
   not a prerequisite for Stages 1–3.

   - **A — Report only.** Contributors see missing coverage but tests stay green.
   - **B — Ratchet by domain.** Once a domain is fully covered, new gaps there fail tests;
     other domains still report only.
   - **C — Gate everything after Stage 3.** Every uncaptured startup emit call fails tests.

   <!-- vantage: question id=OQ-SMG1 leaning="B — ratchet by domain: protect completed coverage without making the gallery a prerequisite for unrelated changes." -->

   _Leaning:_ B — ratchet by domain: protect completed coverage without making the gallery a prerequisite for unrelated changes.

   **Answer:**

   > _(empty — fill in when decided)_

The maintenance gate accepts only `covered` or `witnessed` sites in the chosen domain; the
[coverage contract](#d-smg7-deleting-a-caller-turns-a-capture-test-red) defines those states.
Stages 1–3 do not depend on this choice. [OQ-SMG2](#decision-ledger) is withdrawn, not an open decision.

## Unverified assumptions

These were not checked in source for this document. The planner confirms each before relying on it.

| # | Assumption | If false |
| :--- | :--- | :--- |
| U1 | The entries named for Stage 1 and 2 take their output writers by injection, so a recording writer can be handed in | That emitter is `uncovered` until it takes a writer; Stage 1 may need one small seam |
| U2 | Resolved for publication: the proposed local output is ignored; `git check-ignore` confirms it | Keep generated HTML ignored; never add it to the tracked design |
| U3 | Resolved for publication: `YOLO_NO_AUTO_CAPTURE`, `YOLO_NO_PROGRAM_READINESS` and `YOLO_NO_UPDATE_CHECK` exist; [grounding](../research/startup-message-gallery-grounding.md#4-bounded-safety-checks) links their source | Their existence is not proof a new scenario is effect-free; D-SMG8's third layer still checks effects |
| U4 | The `Run`-level fakes behind `patchedtreescallsite_test.go` can carry a narrative of useful length | The first narrative covers a shorter span and says where it stops |
| U5 | Every segment from the Stage 1 entries is synchronous: the entry is on the stack at the write | That scenario waits for Stage 2's witness rules; Stage 1 picks another variant |
| U6 | No existing helper computes terminal cell width | If one exists, use it for the column flag in place of the "approximate" label |
| U7 | Matching literal fragments finds the wording site for most lines | More pins are needed; the pin check keeps them honest |
| U8 | The line Go reports for a call that spans several source lines falls inside that call expression's span | The innermost-containing-span rule misses; such sites show as ambiguous or as a census miss until the mapping rule is adjusted |
| U9 | Frame file paths can be resolved to working-tree files from the module root, including when tests are built with path trimming | The harness needs the module path prefix stripped before lookup |
| U10 | The fakes behind the Stage 2 progress and subprocess entries are called by production code under the entry, so a mark recorded there has the entry on its stack | That scenario stays `helper` ("pump only") until a fake of that shape exists |
