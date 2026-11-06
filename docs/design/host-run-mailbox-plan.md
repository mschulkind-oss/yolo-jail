---
title: "Plan: host-run's first build slice"
date: 2026-10-23
status: accepted
stage: BUILT
next: "Every task is built; retire this plan when host-run-mailbox.md graduates"
tags: [plan, host, mailbox, stopgap]
---

# Plan: host-run's first build slice

**Status:** 2026-10-23. Completed against the tree, written against `64ac4af1b`.
**Design:** [`host-run-mailbox.md`](host-run-mailbox.md), every question ruled
([decision ledger](host-run-mailbox.md#14-decision-ledger)).

**Precedence.** The design wins on behavior. The tree wins on fact, for cosmetic drift only (a
moved line, a renamed helper with the same role): follow it and say so in the report. This plan
is advice and the first thing to be wrong; never twist the code to match it. **Stop and report,
do not commit,** when a test does not fail the way a task predicts, a named file is dirty with
someone else's work, a named symbol is gone, the fix needs a file outside the task's fence, or a
gate fails outside the task's scope.

## Map

| Path | Change |
| :--- | :--- |
| `internal/hostrun/line.go` | new — `Line`, `ParseHostArgs`, `CheckWords`, `CheckLabel`, `FormatLine` |
| `internal/hostrun/records.go` | new — run id mint/parse, the `Request`, `Refusal` and `Result` JSON shapes, file names |
| `internal/hostrun/ghcred.go` | new — the `gh` credential-printing forms |
| `internal/hostrun/resolve.go` | new — `PATH` resolution and refusal, the `.yolo`-ancestor test, the agent-writable scan, the `env:` list |
| `internal/hostrun/mailbox.go` | new — open `.yolo`, make and hold `host-run`, build a run directory, exclusive create, deliver a refusal, prune |
| `internal/hostrun/host.go` | new — `RunHost`: steps 7 to 10, disclosure, run, spool, signals, delivery, audit, last line |
| `internal/hostrun/wait.go` | new — `Wait`, `Show`, the state table, framed printing |
| `internal/cli/hostrun.go` | new — the verb: help, in-jail ready line, steps 2 to 6, dispatch to `wait`/`show`/`RunHost` |
| `internal/cli/configtarget.go` | `resolveWorkspaceRoot` (~`:276`) split into `resolveWorkspaceRootFrom(dir string) (string, bool)` plus the cwd wrapper |
| `internal/cli/cli.go` | early route beside `gh` (~`:84`) |
| `internal/cli/dispatch.go` | `"host-run": runHostRunVerb` beside `"gh"` (~`:61`) |
| `internal/cli/help.go` | one row after `gh`/`audit` (~`:63`) |
| `internal/cli/hintcommands_test.go` | `"host-run"` in `verbDispatchers` (~`:95`) |
| `internal/ghbroker/daemon.go` | exit-77 message prints the ready line (~`:502-506`) |
| `internal/ghbroker/daemon_test.go` | `TestServeAnswersEveryWrite77AndRunsNothing` (~`:73`) rewritten to the new text |
| `packs/github/briefing/gh.md` | the write bullet (`:9-11`) |
| `internal/cli/run/githubbriefing_test.go` | the pinned "Ask the user to run it on the host." (~`:30`) rewritten |
| `internal/jailcontent/briefing.go` | new section after "## Limitations" (~`:905-916`); `lockedConfigRequest` (`:1001-1008`) gains one sentence |
| `internal/jailcontent/briefing_test.go` | the section's presence in a jail briefing, absence at the host notch |
| `internal/jailcontent/builtinskills/configuring-the-jail/SKILL.md` | the `yolo loopholes enable` hand-off (`:55-58`) |
| `integration/hostrun_test.go` | new — one end-to-end test |

## Reuse before you write

- **Workspace walk:** `resolveWorkspaceRoot` and `workspaceMarked` in
  `internal/cli/configtarget.go`. Split the loop out to take a start directory; keep the cwd
  wrapper's behavior byte-identical for its callers. **Never** call `workspaceRoot()`: its
  bare-cwd fallback is exactly what [HX-D36](host-run-mailbox.md#HX-D36) forbids.
- **Roots:** `paths.OpenStateDirRoot` (`.yolo`), `paths.OpenStateSubdirRoot` (`host-run`, the
  run directory; it creates nothing), `paths.ReadRegularFileBeneath` (every jail-side read),
  `os.Root.Mkdir`, `Rename`, `RemoveAll`, `OpenFile`.
- **Scope:** `paths.WorkspaceScopeBreach` is already inside the walk; nothing extra to call.
- **Paths:** `paths.WorkspaceStateDir(ws)` for `<ws>/.yolo`. (There was a `PATH` refusal that
  named `paths.GlobalStorage()` and `filepath.Dir(paths.UserConfigPath())` as "yolo's state
  directories"; it is deleted, [HX-D32](host-run-mailbox.md#HX-D32).)
- **Display:** `termsafe.HasUnsafe` and `termsafe.Unsafe` (rune check), `termsafe.VisibleLines`
  (printed streams), `shquote.Join` (the ready line), `shquote.JoinDisplay` (`runs:`).
- **TTY:** `tty.IsTerminalFile` (`internal/tty/tty.go:23`).
- **In jail:** `config.InJail()`.
- **Audit:** `brokeraudit.Open(paths.BrokerAuditLog(), warn)` then `Append`; the jail spelling is
  `paths.HostServicesDirName(paths.JailShortHash(runtime.FromWorkspace(ws)))` (import
  `github.com/mschulkind-oss/yolo-jail/internal/runtime`, as `internal/prune/probes.go` does).
- **CLI shape:** mirror `internal/cli/gh.go` (`runGHVerb`, `runGH`) and its tests
  (`runMainCaptured` in `internal/cli/gh_test.go:14`, `TestMainRoutesGHBeforeTheFrontDoor`).
  `answerHelp("host-run", args, os.Stdout)` (`internal/cli/subhelp.go:166`) for `--help`.
- **Pty in tests:** copy `openPty` from `internal/provision/provision_tty_linux_test.go:25` into a
  `_linux_test.go` file; it skips where `/dev/ptmx` is missing.
- **Integration:** `writeProject`, `runYolo`, `yoloBin`, `requireJail` in `integration/`.

## Contracts (shapes other code must match)

```go
package hostrun

type Line struct {
	Dir   string   // -C, "" when absent
	Label string   // --name, "" when absent
	Argv  []string // after --, non-empty
}

func ParseHostArgs(args []string) (Line, error) // args after "host-run"; usage errors name the fix
func CheckWords(words []string) error           // *RuneError{Index int, Rune rune}; Error() never prints the word
func CheckLabel(label string) error             // ^[a-z0-9][a-z0-9._-]{0,39}$
func FormatLine(l Line) string                  // "yolo host-run [-C …] [--name …] -- argv", shquote.Join, leading = or % single-quoted
func NewRunID(now time.Time, rnd io.Reader) (string, error) // YYYYMMDDTHHMMSSZ-xxxxxx
func ParseRunID(name string) (time.Time, bool)

func RunHost(env HostEnv, l Line, startDir, workspace string) int
func Wait(o JailOpts) int
func Show(o JailOpts) int
```

JSON files (`snake_case` keys): `request.json` {`id`, `label`, `argv`, `program`, `cwd`,
`workspace`, `started`}; `refused.json` {`id`, `label`, `argv`, `cwd`, `workspace`, `step`,
`reason`, `next`, `time`}; `result.json` {`id`, `label`, `argv`, `program`, `cwd`, `workspace`,
`exit` (int or null), `signal` (string or empty), `started`, `ended`, `elapsed_ms`,
`stdout_bytes`, `stderr_bytes`, `stdout_truncated`, `stderr_truncated`, `yolo_version`}. Times are
RFC 3339 UTC. Field order and Go type names are cheap and yours.

## Traps

- **`hintcommands_test.go` reads every `yolo …` in a string literal** and fails a hint naming a
  verb or flag the CLI does not parse. `yolo host-run wait --name …` in the briefing, the ready
  line and the gh message only pass once `host-run` is in `verbDispatchers` with `wait` and
  `show` as string cases of its dispatcher's switch, and `-C`, `--name` and `--timeout` appear as
  long-flag literals in the handler. Add the dispatcher entry in Task 4 and land Task 5 after it.
- **`TestEveryRegisteredCommandAnswersHelp`** (`internal/cli/subhelp_test.go:48`) and
  **`TestUsageListsEveryParsedFlag`** (`:207`) apply to the new verb: `yolo host-run --help`
  answers help and the usage names every parsed flag. A `--help` **after** `--` is argv, as in
  `TestGHHelpAfterTheSeparatorIsForwarded`.
- **The dev jail's own `PATH` must not be inherited by a test**, because what it holds is not
  knowable from here: a `bin/` inside the workspace or in yolo's state fails the `PATH` check.
  Unit and integration tests set `PATH` explicitly (a temp `bin/` with a fake program, or the
  resolved directory of `true`), never inherit it. (When this was written the check also refused
  any entry under a `.yolo`-holding directory, so a home carrying a marker failed it; that clause
  is deleted, see [HX-D32](host-run-mailbox.md#HX-D32).)
- **The host form runs nothing with `YOLO_VERSION` set**, and the jail exports it. `InJail` tests
  for a non-empty value, so unit tests use `t.Setenv("YOLO_VERSION", "")`; the integration
  subprocess drops the name from its `Env`.
- **`YOLO_ALLOW_MISSING_PROGRAMS=1` is exported in the dev jail** and fails some
  `internal/cli` tests unrelated to this change; run the gate with it unset.
- **darwin path class:** `t.TempDir()` is under a symlink on macOS and every path here goes
  through `EvalSymlinks`; fixtures hand out `filepath.EvalSymlinks(t.TempDir())`.
- **`os.Root` held across a rename keeps working** (it holds a descriptor); open the run
  directory's root on `.<id>.tmp` before the rename and write streams through it after.
- **`OpenStateSubdirRoot` creates nothing**: `Mkdir` first, treat `fs.ErrExist` as fine for
  `host-run` and as a collision for `.<id>.tmp`.
- **Heartbeat by mtime:** rewrite a fixed-width RFC 3339 timestamp with `WriteAt(…, 0)` through
  the creating descriptor; `wait` reads the file's mtime by `Lstat`, never its content.
- **Do not set `Setpgid` or `Setsid`** on the child: it must stay in the terminal's foreground
  process group ([HX-D8](host-run-mailbox.md#HX-D8)).

## Tasks

### Task 1 — grammar and checks (independent)

- **Settled, do not re-open:** the line is `yolo host-run [-C DIR] [--name LABEL] -- ARGV`;
  `-C` and `--name` at most once each, before `--`; the argv is non-empty and is not itself
  `yolo host-run …`; the label grammar is `^[a-z0-9][a-z0-9._-]{0,39}$`; the rune check refuses
  any rune `termsafe.HasUnsafe` flags (invalid UTF-8 included) and any rune in Unicode Cf, Zl or
  Zp, naming the word's position and the code point, never the word; the run id is
  `^[0-9]{8}T[0-9]{6}Z-[a-z2-7]{6}$` (UTC second, 30 random bits); `FormatLine` uses
  `shquote.Join` and single-quotes a word beginning `=` or `%`.
- **Tests first** — `internal/hostrun/line_test.go`:

  ```go
  func TestParseHostArgs(t *testing.T) {
  	l, err := ParseHostArgs([]string{"-C", "/w", "--name", "m-1", "--", "gh", "pr", "merge", "42"})
  	if err != nil || l.Dir != "/w" || l.Label != "m-1" || strings.Join(l.Argv, " ") != "gh pr merge 42" {
  		t.Fatalf("%+v %v", l, err)
  	}
  	for _, bad := range [][]string{
  		{"--"}, {"gh"}, {"-C", "/a", "-C", "/b", "--", "x"}, {"--name", "Bad", "--", "x"},
  		{"--", "yolo", "host-run", "--", "x"}, {"--bogus", "--", "x"},
  	} {
  		if _, err := ParseHostArgs(bad); err == nil {
  			t.Errorf("accepted %q", bad)
  		}
  	}
  }

  func TestCheckWordsNamesPositionAndCodePoint(t *testing.T) {
  	for _, w := range []string{"a\nb", "a‮b", "a​b", "a­b", "a b", "a\xffb"} {
  		err := CheckWords([]string{"ok", w})
  		var re *RuneError
  		if !errors.As(err, &re) || re.Index != 1 {
  			t.Fatalf("%q: %v", w, err)
  		}
  		if strings.Contains(err.Error(), w) {
  			t.Errorf("the refusal prints the raw word %q", w)
  		}
  	}
  	if err := CheckWords([]string{"café", `\d+`, "it's"}); err != nil {
  		t.Fatal(err)
  	}
  }

  func TestFormatLineQuotesLeadingEqualsAndPercent(t *testing.T) {
  	got := FormatLine(Line{Dir: "/home/you/w", Label: "t1", Argv: []string{"echo", "=x", "%self", "it's"}})
  	want := `yolo host-run -C /home/you/w --name t1 -- echo '=x' '%self' 'it'"'"'s'`
  	if got != want {
  		t.Fatalf("got  %s\nwant %s", got, want)
  	}
  }

  func TestRunIDRoundTrip(t *testing.T) {
  	at := time.Date(2026, 10, 23, 14, 33, 7, 0, time.UTC)
  	id, err := NewRunID(at, bytes.NewReader(make([]byte, 8)))
  	if err != nil || !strings.HasPrefix(id, "20261023T143307Z-") || len(id) != 23 {
  		t.Fatalf("%q %v", id, err)
  	}
  	if got, ok := ParseRunID(id); !ok || !got.Equal(at) {
  		t.Fatalf("%v %v", got, ok)
  	}
  	for _, bad := range []string{".20261023T143307Z-aaaaaa.tmp", "20261023T143307Z-AAAAAA", "x", "../x"} {
  		if _, ok := ParseRunID(bad); ok {
  			t.Errorf("parsed %q", bad)
  		}
  	}
  }
  ```

  Red: `go test ./internal/hostrun/` (package does not exist).
- **Change:** `line.go`, `records.go`, `ghcred.go` (forms: `gh auth token`; `gh auth status`
  with `-t` or `--show-token`; `gh config get oauth_token`; matched on the argv with `argv[0]`'s
  base name `gh`, flags anywhere after the subcommand). Add a `ghcred_test.go` table for those
  four plus `gh auth status` and `gh pr view` passing.
- **Gates:** `go test ./internal/hostrun/`; `go vet ./internal/hostrun/`.
- **Fences:** nothing outside `internal/hostrun/`.
- **Commit:** `feat(hostrun): line grammar, rune check and run ids`.
- **Report:** the red run, the green run.

### Task 2 — the host form (after 1)

- **Settled, do not re-open:** steps 7 to 10 and everything after them in
  [design §4.3](host-run-mailbox.md#43-what-the-host-does): `.yolo` absent refuses naming a
  launch; `host-run` made 0700 with `Root.Mkdir`, opened link-refusing; the `gh` credential form
  (step 8) delivers `refused.json` in its own run directory; step 9 only resolves the program and
  refuses nothing (the `PATH` audit it used to carry is deleted — [HX-D32](host-run-mailbox.md#HX-D32));
  the run directory is built
  as `.<id>.tmp` holding `request.json` and `heartbeat`, then renamed to `<id>`; then prune
  (entries matching the id grammar or its `.<id>.tmp` form, id time over 24 h, `RemoveAll`
  beneath the held root, failure is one warning); disclosure exactly as the design's block,
  `env:` line only when one of the listed variables is set; the child execs in the start
  directory, stdin the terminal, stdout and stderr pipes teed to the terminal and spooled to
  16 MiB each with the truncation marker; heartbeat every 10 s; INT ignored by the host form,
  TERM and HUP forwarded; after exit `stdout`, `stderr`, then `result.json` by temp name and
  rename; one audit line (`service: host-run`, `request: <id>`, `set: human-run`/`refused`,
  `outcome: ran`/`refused`); exit with the child's status or 128 + signal; every create
  `O_CREATE|O_EXCL|O_NOFOLLOW|O_NONBLOCK`, mode 0600.
- **Tests first** (`internal/hostrun/host_test.go`, driving `RunHost` with `HostEnv` holding temp
  files for the streams, a fixed `Now`, a fake `PATH` with a script `bin/say` that prints its
  args, and an audit log in a temp dir):
  - happy path: `say hi` delivers `stdout` = `hi\n`, `result.json` exit 0, last stderr line
    contains `Nothing to paste back.`, one audit line with `Set == "human-run"`;
  - `request.json` exists and `stdout` does not while the child runs (`bin/slow` that sleeps
    until a file appears; assert, then create the file);
  - `.yolo` absent refuses 64 naming `launch`; `.yolo` a symlink refuses; `.yolo/host-run` a
    symlink refuses; nothing created outside the workspace (walk the temp tree before and after);
  - a FIFO and a hardlink planted at `host-run/.<id>.tmp/stdout` are impossible to plant ahead
    (the id is random), so plant them by injecting a fixed `rnd`: the create fails, the run is
    refused before exec (step 10's message), and the planted file is unchanged;
  - `PATH` with `""`, `.`, `<ws>/bin` or `$HOME/.yolo-holder/bin` refuses 64 and delivers
    `refused.json` with `step: 9`; `gh auth token` refuses with `step: 8`;
  - a start failure (`PATH` holds no such program) delivers `result.json` with `exit` null and
    the error in stderr;
  - SIGINT to the child (send to its pid) records `signal: "interrupt"` and exit 130;
  - truncation: a spool cap injected at 8 bytes writes the marker and `stdout_truncated: true`;
  - prune: a run directory named 25 h before `Now` is removed, one named 23 h before stays, a
    non-matching name stays.
  Red: `go test ./internal/hostrun/ -run Host`.
- **Change:** `resolve.go`, `mailbox.go`, `host.go`. `HostEnv` carries `Stdin`, `Stdout`,
  `Stderr`, `Getenv`, `Now`, `Rand`, `Audit`, `Version`, `SpoolCap` (default 16 MiB); test seams
  are cheap and yours.
- **Gates:** `go test -race ./internal/hostrun/`.
- **Fences:** `internal/hostrun/` only.
- **Commit:** `feat(hostrun): the host form runs, spools and delivers into the mailbox`.
- **Report:** red and green runs; whether `Root.Rename` of a directory needed anything beyond the
  plain call.

### Task 3 — `wait` and `show` (after 1; independent of 2)

- **Settled, do not re-open:** [design §4.4](host-run-mailbox.md#44-how-the-result-arrives).
  Poll every 1 s; consider only names matching the id grammar; an unnamed `wait` snapshots the
  names at start and takes the first new one; a named `wait` takes the newest run whose
  `request.json` or `refused.json` carries the label, present or new; neither consumes; a missing
  mailbox is "nothing yet"; the state table's messages and exits (64, 69, 75, 0); default
  timeout 100 min; heartbeat stale after 60 s by mtime; a permission error reading any file is
  69 with the copy-back fallback; `show` with no argument takes the newest name, with an id that
  run, with `--name` the newest carrying it, no run at all is 64; printing as the design's framed
  block, 16 KiB head and tail per stream through `termsafe.VisibleLines`, exit status in the body.
- **Tests first** (`internal/hostrun/wait_test.go`, writing fixture run directories by hand, a
  fake clock and an injected poll interval of 1 ms):
  - unnamed `wait` ignores a pre-existing finished run and returns 0 on one created after start;
  - named `wait --name t1` returns at once on a pre-existing finished `t1` run and ignores a newer
    `t2`;
  - `refused.json` → 69 printing its reason and next step;
  - stale heartbeat → 69; fresh heartbeat at the timeout → 75 naming "running on the host";
  - nothing at the timeout → 75 naming "not run yet";
  - a result whose command exited 75 → `wait` exits 0, body says `exit:  75`;
  - a `result.json` with mode 0000 (skip as root) → 69 naming the copy-back fallback;
  - a symlink at the run directory or at `stdout` is not followed (read refused, state 69);
  - `show` with no runs → 64; with three runs → the newest by name.
  Red: `go test ./internal/hostrun/ -run 'Wait|Show'`.
- **Change:** `wait.go`. `JailOpts` carries `Workspace`, `Label`, `ID`, `Timeout`, `Out`, `Err`,
  `Now`, `Poll`.
- **Gates:** `go test -race ./internal/hostrun/`.
- **Fences:** `wait.go` and `wait_test.go` only, so it lands beside Task 2 without overlap.
- **Commit:** `feat(hostrun): wait and show read the mailbox`.
- **Report:** red and green runs.

### Task 4 — the verb (after 2 and 3)

- **Settled, do not re-open:** `host-run` is routed in `Main` beside `gh`, before the banner,
  update notice and global flags; `wait` and `show` are verbs; anything else is the host form.
  Host form order: (1) `config.InJail()` → print the ready line and the matching `wait` (the
  design's block in [§4.1](host-run-mailbox.md#41-writing-the-line)), create nothing, exit 64; fill `-C` from `YOLO_HOST_DIR` only when
  absent; with it unset, print the line without `-C` and say to run it from a terminal inside the
  workspace; run `CheckWords` first and print no line that fails it; (2) all three streams
  terminals; (3) parse; (4) rune check; (5) start directory = `-C` or cwd through `EvalSymlinks`,
  must be a directory, a `-C` beginning `/workspace` adds the `$YOLO_HOST_DIR` sentence; (6)
  `resolveWorkspaceRootFrom(start)`, none → the design's refusal text naming `-C`,
  `$YOLO_HOST_DIR` and running bare; then `hostrun.RunHost`. `wait`/`show` find the workspace by
  the same walk from `-C` or the cwd and refuse 64 with none.
- **Tests first** (`internal/cli/hostrun_test.go`, using `runMainCaptured`):

  ```go
  func TestMainRoutesHostRunBeforeTheFrontDoor(t *testing.T) {
  	t.Setenv("YOLO_VERSION", "test")
  	t.Setenv("YOLO_HOST_DIR", "/home/you/code/widgets")
  	rc, out, errOut := runMainCaptured(t, "host-run", "--name", "t1", "--", "echo", "hi")
  	if rc != 64 {
  		t.Fatalf("rc %d out %q err %q", rc, out, errOut)
  	}
  	if want := "yolo host-run -C /home/you/code/widgets --name t1 -- echo hi"; !strings.Contains(errOut, want) {
  		t.Fatalf("no ready line %q in:\n%s", want, errOut)
  	}
  	if !strings.Contains(errOut, "yolo host-run wait --name t1") {
  		t.Fatalf("missing wait line:\n%s", errOut)
  	}
  }
  ```

  Plus: no workspace above a temp start dir → 64 naming `-C` and `$YOLO_HOST_DIR`, nothing
  created (host form, `YOLO_VERSION` unset, streams on a pty from `openPty`); non-terminal stdout
  → 64 naming "no redirect or pipe"; `resolveWorkspaceRootFrom` stops at a
  `WorkspaceScopeBreach` (start dir = a fake home with a `yolo-jail.jsonc`, `HOME` pointed at
  it); `yolo host-run --help` exits 0; the help listing contains `host-run`; `wait` with no
  workspace → 64. Red: `go test ./internal/cli/ -run HostRun`.
- **Change:** `internal/cli/hostrun.go` (new), `configtarget.go` (split the walk), `cli.go`
  (route), `dispatch.go` (row), `help.go` (row: `{"host-run", "Run one command on the host for
  this workspace's agent, and deliver its output to the jail ('wait', 'show' in the jail)"}`),
  `hintcommands_test.go` (`"host-run": {"internal/cli", "hostRunMain", "args[0]", ""}`, matching
  whatever the dispatcher function and switch variable are actually named).
- **Gates:** `go test ./internal/cli/ -run 'HostRun|Hint|Help|Usage|Dispatch'`, then
  `go test ./internal/cli/` with `YOLO_ALLOW_MISSING_PROGRAMS` unset.
- **Fences:** no `internal/cli/run/` file; no change to `workspaceRoot()`'s behavior.
- **Commit:** `feat(cli): yolo host-run`.
- **Report:** red and green runs; the dispatcher's real name as entered in `verbDispatchers`.

### Task 5 — the texts agents read (after 4)

- **Settled, do not re-open:** each text names `yolo host-run` and keeps the old instruction as
  the fallback; the gh message prints `yolo host-run -C <workspace> -- gh <argv>` from
  `b.workspace` and `d.Argv` through `hostrun.FormatLine`, and prints no line when the request
  sent stdin (it names `--body-file <a file in the workspace>`) or when `hostrun.CheckWords` fails
  (it names moving that text to a file) ([HX-D48](host-run-mailbox.md#HX-D48)).
- **Exact texts:**
  - gh exit 77, replacing "To make the change, ask the user to run it on the host;": "To make
    the change, give the user this line to run in a terminal on the host; its output comes back
    to this workspace (`yolo host-run wait` waits for it): `yolo host-run -C … -- gh …`. If their
    yolo has no `host-run`, ask them to run the `gh` command there and paste the output;" then the
    existing `yolo audit` clause.
  - `gh.md` write bullet, after "need an approval step that is not built yet.": "The refusal
    prints a `yolo host-run` line for the user to run on the host, which sends the output back
    here; if their yolo has no `host-run`, ask the user to run it on the host."
  - Jail briefing, a new section after "## Limitations":

    ```text
    ## Running a command on the host

    When you need something only the host can do, write ONE line for the human to run in a host
    terminal, and the output comes back to you; nothing needs to run here first:

        yolo host-run -C <value of $YOLO_HOST_DIR> --name <fresh-label> -- <command>

    Then start `yolo host-run wait --name <fresh-label>` as a background task (run it again if it
    exits 75), or ask the human to say "done" and run `yolo host-run show --name <fresh-label>`.
    Start the line with bare `yolo host-run`; no `;`, `&&`, `|`, `>` or `$(`; single-quote words
    with shell characters; keep it under 300 characters (long text goes in a workspace file the
    command reads). Typing the line here prints it ready to paste. If the host's yolo has no
    `host-run`, ask the human to run the command and paste the output.
    ```

  - `lockedConfigRequest`, appended: "`yolo host-run -C <value of $YOLO_HOST_DIR> -- yolo check`
    sends that check's output back here."
  - `configuring-the-jail` `SKILL.md`, the `brokered` loophole paragraph: the human enables it on
    the host; give them `yolo host-run -C <value of $YOLO_HOST_DIR> -- yolo loopholes enable
    <name>` so the output comes back.
- **Tests first:** rewrite `TestServeAnswersEveryWrite77AndRunsNothing`'s pinned phrases to the
  new text and add a case with `Argv: {"pr", "merge", "42"}` and no stdin that asserts
  `yolo host-run -C ` + the fixture workspace + ` -- gh pr merge 42`; keep the stdin case asserting
  `--body-file` and no `yolo host-run -C`. Rewrite `githubbriefing_test.go`'s
  "Ask the user to run it on the host." to the new sentence. In `briefing_test.go`, assert a jail
  briefing contains `## Running a command on the host` and `yolo host-run wait --name`, and the
  host-notch base does not. Red: `go test ./internal/ghbroker/ ./internal/cli/run/ ./internal/jailcontent/`.
- **Gates:** the same three packages, then `go test ./internal/cli/ -run Hint`.
- **Fences:** no file under `internal/hostrun/` beyond calling its exported functions.
- **Commit:** `feat: point the host hand-offs at yolo host-run`.
- **Report:** red and green runs; any other test that pinned an old phrase (rewrite it to the new
  behavior; never loosen it).

### Task 6 — end to end (after 4 and 5)

- **Tests first** — `integration/hostrun_test.go`, `TestHostRunDeliversAHostCommandToTheJail`:
  1. `dir := writeProject(t, "{}")`; `runYolo(t, dir, "yolo host-run --name it-1 -- printf hi")`
     exits 64, contains `-C ` + `dir`, and the launch leaves `dir/.yolo`.
  2. On the test process (the host here): `exec.Command(yoloBin, "host-run", "--name", "it-1",
     "--", "printf", "hi")`, `Dir: dir`, `Env` = `os.Environ()` without `YOLO_VERSION` and with
     `PATH` = the directory of `exec.LookPath("printf")` (skip if that directory has a `.yolo`
     ancestor), all three streams on a pty slave; it exits 0, and the master's output contains
     `Nothing to paste back.`
  3. `runYolo(t, dir, "yolo host-run show --name it-1")` exits 0 and its output contains `hi`
     and `exit:  0`.
  Red: `go test -count=1 -run TestHostRunDeliversAHostCommandToTheJail ./integration`.
- **Gates:** that test; `just check-ci`; the nested-jail check (below).
- **Commit:** `test(integration): host-run end to end`.
- **Report:** the run, and `podman info --format '{{.Host.Security.Rootless}}'` beside it.

## Ships with

- **Unit tests:** as listed per task. The call-site pins are
  `TestMainRoutesHostRunBeforeTheFrontDoor` (fails if the early route goes), the help-listing and
  `verbDispatchers` checks (fail if the rows go), the gh daemon case asserting the printed line
  (fails if the call in `daemon.go` goes), the briefing section test (fails if the section goes),
  and the host test's audit assertion (fails if the `Append` goes).
- **Integration:** `TestHostRunDeliversAHostCommandToTheJail`, the one test that catches the
  jail failing to read what the host wrote.
- **Tests rewritten to the new behavior, not relaxed:** `TestServeAnswersEveryWrite77AndRunsNothing`,
  the github briefing test's pinned sentence.
- **Docs:** the design doc stays the authority until graduation; no `CHANGELOG.md` entry
  (written at release time). The user guide's loopholes page is not touched in this slice.
- **Help text:** the `host-run` usage (`answerHelp`) states that the output is piped (no color, no
  pager, no TUI) and that the line must start with bare `yolo host-run`.
- **Nested-jail check (AGENTS.md, Workflow step 2):** `just build-dev-bundle`; from a throwaway
  workspace, launch with the emitted launcher, type `yolo host-run -- printf hi` inside (ready
  line printed), then on the outer jail run the emitted launcher's `host-run` with
  `env -u YOLO_VERSION PATH=<printf's dir>` from that workspace, and `show` inside the nested
  jail.
- **Measurements owed before the stage moves to `BUILT`** (none blocks landing): one real paste
  on a rootless-podman host; one from an Apple Container jail and one from a macos-user session
  ([design §7](host-run-mailbox.md#7-backends-and-notches)); the 80-column copy test in bash, zsh and
  fish ([design §11](host-run-mailbox.md#11-what-done-looks-like)).
- **Cheap and yours:** Go type and field names inside `internal/hostrun`, the poll and heartbeat
  helpers' shape, refusal wording beyond the quoted phrases, test helper names.

## Don't

- Don't read any file beneath `.yolo` on the host side, `request.json` included: the host writes
  it and never reads it back ([HX-P3](host-run-mailbox.md#HX-P3)).
- Don't use `paths.WriteWorkspaceStateFile` or `os.WriteFile` for mailbox files: both truncate an
  existing inode ([HX-D7](host-run-mailbox.md#HX-D7)).
- Don't add a token, digest, expiry or single-use claim, and don't add an `ask` verb: each was
  dropped on purpose ([HX-D40](host-run-mailbox.md#HX-D40)).
- Don't call `workspaceRoot()`; don't guess a workspace from running containers
  ([HX-D37](host-run-mailbox.md#HX-D37)).
- Don't add a `brokeraudit` field ([HX-D45](host-run-mailbox.md#HX-D45)).
- Don't add a pty for the child: pipes are deliberate ([HX-D9](host-run-mailbox.md#HX-D9)).
- Don't let `wait` or `show` delete anything ([HX-D43](host-run-mailbox.md#HX-D43)).
