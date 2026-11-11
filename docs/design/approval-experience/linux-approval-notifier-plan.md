---
status: accepted
stage: BUILT
next: "Root reviews and integrates the bounded candidate, then runs combined gates and native capture"
depends-on:
  - linux-approval-notifier.md
tags: [implementation-plan, approvals, linux]
---

# Build the opt-in Linux approval hook, not a new authority

**Status:** 2026-11-09. Written against `62b8bbda375acda7caa3f675a4844aa020e8b060`.
**Design:** [linux-approval-notifier.md](linux-approval-notifier.md). Supervisor approved the
per-request staged scope after the singleton/current-CLI conflict was escalated.

Precedence: the design wins on behavior, the tree wins on fact, this plan is advice
except for marked constraints. A non-cosmetic disagreement is a stop-and-report, not a
reason to broaden approval authority. No implementer starts from an unsettled product premise.

## Bounded implementation evidence

Implemented from main `017ea6df9`. The initial test-first command failed with missing
vendored godbus; after adding the pinned dependency, it failed with undefined `Run`,
`Dependencies` and `Notification`, before production code existed. The focused consumer,
adapter and race tests then passed. The CLI/store cases prove Allow once, Deny, exits
3/5/6, exact-use grants, absence of working entries on refusal, and the real CLI audit.
The over-ceiling case files a narrower two-minute policy and clicks the offered 15m action;
a normal GitHub PR duration can succeed and is not asserted to be refused.

- [`notifier_test.go`](../../../internal/approvalnotify/notifier_test.go): authenticated actions,
  typed reason-2 Deny, owner changes, expiry, cleanup ordering, refusal feedback and failures.
- [`bus_test.go`](../../../internal/approvalnotify/bus_test.go): actual godbus call boundary,
  explicit address, no-auto-start flags, typed signals queued during Notify, wire signatures.
- [`main_test.go`](../../../internal/approvalnotify/main_test.go): production entry SIGINT/SIGTERM
  and the unchanged hook runner's decided/expired/jail-ended withdrawal, with event readiness.
- [`internalapprovalnotify_test.go`](../../../internal/cli/internalapprovalnotify_test.go):
  hidden dispatch and the real host approval subprocess/store boundary.

Native integrated capture, nested-jail verification, combined completion census and landing
remain root-owned. Tests read no source files outside their package, but import the existing
brokerhook/store and CLI authority packages; root regenerates the combined census once.
No SIGKILL cleanup, singleton lifecycle, Mac rendering or P-5 through P-9 wording is claimed.

## Grounding and reuse

- `internal/brokerhook/brokerhook.go`: `Runner.fire` supplies a schema-1
  `brokerstore.EventView` on stdin; `Env` supplies absolute `YOLO_BIN`. The hook runs in
  the host user's home. `Runner.event` cancels decided/expired/refused requests; `fire`
  stops their whole process group with SIGINT, then SIGKILL after `StopGrace` (2 seconds).
  Do not modify this runner. Use `exec` in the opt-in example so the notifier receives signals.
- `internal/brokerstore/watch.go`: `EventView`, `RequestView` have stable JSON names;
  `RequestView.Expires`, `Headline`, `Description`, and the event's `Summary` are already
  produced by the classifier/store. Consume this view, never reclassify commands.
- `internal/cli/approve.go`: `approveExitAlreadyDecided=3`, `approveExitPolicy=5`,
  `approveExitNotConfirmed=6`. `--add`/`--remember` are removed (usage 2); current GitHub
  resource-duration decisions can be policy-refused. Pass refusals through unchanged.
- `internal/cli/internal.go`: `runInternal` is the hidden-command dispatch seam, before
  normal run rewriting; mirror its thin entry points, no dispatch registry/public verb.
- `internal/cli/hostdepstub_test.go`: `TestMain` routes `testAsYoloArg` through `Main`.
  `internal/cli/onrequestapprove_test.go` builds the host fixture and self-exec yolo
  wrapper. Reuse the fixture construction, not its sleeps or the private owner's script.
- `internal/termsafe`: `Visible` / `VisibleLines` neutralize control/bidi characters.
  `brokerstore.MarkupEscape` escapes notification XML. Apply once to plain stored text;
  do not escape an already `_MARKUP` environment string again.
- `go.mod` / `vendor/` contain TOML/Lua/x/sys/text/YAML but no D-Bus library.
  Public `github.com/godbus/dbus/v5@v5.2.2` is already in the shared module cache;
  its module requires x/sys v0.27.0, below this repo's v0.49.0. Inspect its BSD-2-Clause
  license and APIs, add that public version and run `go mod vendor`.
  `dbus.Connect(address)` uses an explicit address; never `SessionBus*` or
  `ConnectSessionBus`. `dbus.FlagNoAutoStart` must accompany server method calls.
- No `dbus-daemon` executable was present during grounding. Mandatory runner/adapter
  conformance tests must use fakes and not silently skip. Native rendering is a separate gate.

## Map (one worker owns these paths)

| Path | Change |
| :--- | :--- |
| `internal/approvalnotify/` (new) | Linux D-Bus adapter, per-request lifecycle, CLI subprocess, entry, source-named tests |
| `internal/cli/internal.go` | Hidden `approval-notify` case and hidden usage list |
| `internal/cli/internalapprovalnotify_test.go` (new) | Production entry dispatch, host-only and invalid-input boundary tests |
| `go.mod`, `go.sum`, `vendor/` | Public godbus v5.2.2, vendored for hermetic Nix |
| These two docs | Stamp implementation evidence and remaining staged scope, no full acceptance claim |
| [`native-notification-gallery.md`](native-notification-gallery.md) | Only P-1/P-2/P-4 implementation status, linked evidence; preserve baseline and NG-D5 |

No new binary: the existing shipped `yolo` reaches the command on host and jail;
host-only checking refuses jail invocation. Hence flake/source-bundle binary lists do
not change. Production source must remain inside the flake's existing `internal/` fileset.

## Task 1 — conformance tests first, then one implementation commit

**Constraint:** single writer, serial after the planning writer stops. Clean tree required.
Do not run whole-tree gates or slow integration; root owns one review and combined landing.
Commit `feat(approvals): add opt-in Linux D-Bus notifier` with code, tests, dependency and
bounded status docs. Root may request a separate checkpoint commit if interrupted.

**Consumer boundary:** hidden command with no arguments, schema-1 request-filed JSON
on stdin, environment's `DBUS_SESSION_BUS_ADDRESS` and `YOLO_BIN`. Refuse jail, non-Linux,
missing/malformed request, wrong event/schema, nonpending or already-expired input, absent
absolute executable path. Errors name the existing host `yolo approve` next step and go
only to hook stderr. Limit input/output accumulation, never log full environment.
No stdout protocol. Returned hook output remains diagnostics, never consent.

**Advice:** split an effectful `Main`/entry from a `Run(ctx, event, deps)` loop. Small private
interfaces suffice: bus owner/capabilities/notify/close/signals/connection-close; decision
runner returning exit and bounded combined output; expiry timer factory. Fake these exact
interfaces in mandatory tests. This is testability for existing effects, not a new API.
Cheap choices: file names, struct names, bounded text cap, maps vs switches.

**Tests first:** create `internal/approvalnotify/notifier_test.go`. Add this first test
with the test-local `newNotifierFixture` helper specified below; run the red command
before creating production code. The helper belongs in the test, not a new runtime seam.

```go
func TestForgedActionCannotGrant(t *testing.T) {
    f := newNotifierFixture(t)
    f.start() // starts Run; returns only after fake Notify reports id 41
    f.signal(":1.99", "ActionInvoked", uint32(41), "once")
    f.signal(":1.7", "ActionInvoked", uint32(99), "once")
    f.signal(":1.7", "ActionInvoked", uint32(41), "default")
    f.signal(":1.7", "ActionInvoked", uint32(41), "deny")
    f.waitDone()
    f.assertDecisions([][]string{{"approve", "r-0123456789abcdef0123456789abcdef", "--deny"}})
}
```

The fixture supplies owner `:1.7`, capability `actions`, Notify id 41, schema-1 pending
request with the id above and expiry one hour after an injected time; captures every
method and decision argv, provides buffered ordered signals at the exact Notifications
path/interface, and offers channels for Notify, completion and timer firing. `waitDone`
has only a generous failure guard. Do not add sleeps, elapsed-time assertions, or negative
assertions before a subsequent event/termination establishes that processing completed.
The red command is `go test ./internal/approvalnotify -run TestForgedActionCannotGrant`;
it must fail before the runner exists. Record the actual red result, not this prediction.

**Mandatory conformance cases, through that same consumer loop:**

1. Posted Notify has app identity yolo, urgency byte 2, resident true, timeout int32(0),
   replaces_id 0 for a new hook, exactly once/15m/deny key-label pairs, no default.
   Preserve current Headline/Description presentation (no P-5..P-9 rewrite); XML/control/bidi
   attacks stay text. Existing classifier summary may supply the alternative CLI id.
2. Genuine offered once/15m/deny invokes exactly the argv in the design, one time.
   Unknown key, wrong id, forged sender, wrong path/interface, malformed body are ignored.
   Duplicate signals cannot run two decisions. Signal arrival during Notify must not be
   lost: install the subscription before posting; process queued signals after id is known.
3. Owner replaced while a toast is live: old-owner signals rejected; new owner's id 41
   is not the old notification. Never close that id at the new owner. Loss terminates with
   no decision. Requery ownership at decision time, not only once at subscription.
4. Close reason 2 runs `--deny`; 1/3/4 run no decision. Append a genuine offered action
   after an ignored close to establish event ordering. Explicit programmatic close and
   feedback toast ids cannot loop back into Deny.
5. Context cancellation and injected expiry close the issued id before closing connection;
   neither decides. Already-expired input posts nothing. Connection/owner loss never grants.
6. Missing address: no dial. Missing owner/no actions: no Notify/no CLI/no terminal fallback.
   Dial, capabilities, Notify and CLI-start failures return diagnosable hook failures.
7. CLI exit 0: withdraw and complete. Exits 3/5/6: withdraw original and post nonactionable
   feedback with escaped bounded CLI message/next step, no default or other actions;
   retain exit/message on hook stderr. Feedback failure cannot turn refusal into success.
   Other nonzero exits are diagnostic failures, never treated as consent or retried as once.

**Adapter tests:** fake the actual godbus method-call boundary used by the production
adapter (not a separate unused transport). Assert method interface/path/destination,
`FlagNoAutoStart`, wire signatures, checked owner, and signal registration ordering.
Exercise `Connect` with an explicit private test address/connection factory; prove
missing address doesn't invoke it. The same production adapter must receive typed
`dbus.Signal` values in tests, not fabricated decision booleans. Tests must fail if its
owner/capabilities/Notify call sites or no-auto-start flags are deleted. No source-comment
assertions. A private real-bus protocol test may be added when a bus runner is available,
but cannot replace these required non-skipping checks.

**CLI boundary tests:** exercise the new `runInternal` case (not merely the callee), and
build a stand-in executable with `testAsYoloArg` as the existing on_request test does.
Run the notifier's real subprocess decision runner through current `yolo approve`
against a temp brokerstore/live scope: once succeeds, denial succeeds; already decided
is 3, over-ceiling duration is 5, removed scope is 6. Assert actual store/grant/audit
results and captured refusal text; transport success is never asserted as authorization.
Use the notifier fixture's fake bus to drive the loop around this real CLI boundary.
No real credentials, remote APIs, or agent launches.

**Existing hook lifecycle proof:** run a test-local notifier-shaped process under the
unchanged `brokerhook.Runner`; readiness is a pipe/channel event. Decide the real request
elsewhere, advance the store's fake expiry, and end its jail. Observe the SIGINT-driven
close event, not a sleep. Also test production Main SIGTERM/SIGINT signal-to-context
wiring with the subprocess entry; fake transport readiness and close capture must be
explicit. Never claim SIGKILL is caught. If subprocess fake transport needs additional
plumbing, keep it test-local; no test-only hidden production CLI verbs.

## Focused gates and reporting

```console
gofmt -w internal/approvalnotify internal/cli/internalapprovalnotify_test.go internal/cli/internal.go
go test ./internal/approvalnotify
go test -race ./internal/approvalnotify
go test ./internal/cli -run 'TestInternalApprovalNotify|TestApprovalNotify|TestApproveStableExitCodes|TestApproveGitHubWithoutTheEntriesHookRefusesFor'
go test ./internal/brokerhook -run 'TestAHookIsKilledWhenItsRequestLeavesPending|TestAHookStillRunningAtExpiryRecordsNoFailure|TestTheHookDecidesNothingByItself'
CGO_ENABLED=0 go build -o <durable-scratch>/yolo ./cmd/yolo
git diff --check
git diff --cached --name-only
```

Retain original HOME/shared Go caches for builds. Scope HOME/XDG changes only to fixture
subprocesses (existing CLI tests set them per test). Build output belongs in durable scratch
with `go build -o <scratch>/yolo ./cmd/yolo`, not an untracked repository executable.
If new tests read files outside their package, rerun completion-census and report its diff
to root before editing outside this fence. Root owns whole-tree/lint/landing and runtime
capture. Nested-jail verification required by AGENTS remains a root integration gate;
unit results alone do not satisfy it.

**Fence:** no approval umbrella, history, browser boundary, roadmap, capture scripts,
Linux capture doc, personal config/hook, Mac renderer, grant/auth changes, config activation,
P-5..P-9 wording, fixture/capture regeneration, or unrelated census edits. A needed outside
edit or changed premise is a blocking supervisor decision. Ordinary focused failures are repaired.

**Report:** commit SHA/stat, actual red and green commands/results, changed-files,
no-staged-files, optional entrypoint spelling, skipped/unavailable tests, residual SIGKILL/
singleton/native-capture/Mac gaps. P-1/P-2/P-4 status may say bounded implementation tested,
never native proof/full P-2. NG-D5 remains open until integrated candidate recapture; the
capture lead owns that evidence and the root owns the one high-effort review.
