# Nothing reaches your host because it happened to be there — loophole activation

**Status:** RULED 2026-08-15, nothing built. The rulings are the maintainer's, taken during the
`host-processes` conversion; this doc records them and works out what they cost.

**The short version.** A loophole is active today because it was *present* and something it named
happened to exist on the host — bundled, plus a `requires` predicate that sniffs `PATH`. That is
being replaced end to end: **presence stops implying activation.** A pack declares a loophole's
default state, that declaration defaults to **disabled**, config overrides it at either scope, and
the `requires.command_on_path` sniff is **deleted** rather than fixed — it is the mechanism, not a
bug in the mechanism. The principle behind it, in the maintainer's words: *"we don't give host
access by default."*

**Reads with:** [`broker-as-a-pack.md`](broker-as-a-pack.md) (the sprint this came out of; §5.5 is
the connection preamble, §12 the `host-processes` conversion),
[`loophole-packaging-overview.md`](loophole-packaging-overview.md) (§5 "Defaults, and what stays
bundled" — this supersedes its activation story),
[`gate-placement-principle.md`](gate-placement-principle.md) (why a second gate over the same act is
worse than none).

---

## 1. What activates a loophole today

Three layers already exist and are well-named (`internal/loopholes/discover.go:643-680`):

| | today | meaning |
|---|---|---|
| `Enabled` | defaults **true** from the manifest; config may override | *"the user's switch"* |
| `Active` | `Enabled` **and** `requires`/`platforms` are satisfied | *"the machine can run it"* |
| `Honored` | `Active` **and** the origin gate admits it | *"the pack it came from may touch the host"* |

The layering is right. What is wrong is what feeds the first one: `enabled` defaults to **true** in
the manifest (`discover.go:50`), and every bundled manifest sets it. So a loophole is on the moment
it is *present*, and presence was never a decision the user made.

### 1.1 The sniff, and the bug it is causing right now

`requires.command_on_path` is `exec.LookPath` **on the host** (`internal/loopholes/loopholes.go:176`).
Two manifests use it:

- `claude-oauth-broker` requires `claude` on the host's PATH, meaning *"only run the broker if Claude
  Code is installed on the host."*
- `host-processes` requires `ps`, which its own manifest comment admits is a POSIX staple and a
  formality.

**The broker's use is a live bug.** yolo-jail exists to run agents *inside jails*, and agent CLIs
install lazily in the jail (`~/.yolo-launchers/`). A user who only ever runs `claude` in a jail has
no host `claude`, so **the broker never activates** — silently — and that user gets exactly the
concurrent single-use-refresh-token race the broker exists to prevent
([`agent-credentials.md`](agent-credentials.md) §2.5). It works on the maintainer's machine because
that host has claude installed. A predicate that is true for the author and false for the product's
core use case is the worst shape a default can have.

**The dependency it was approximating is structural, not observational.** "Is there anything to
refresh for" is really "is the claude pack selected" — which the pack system can express directly,
and which no `PATH` lookup can answer correctly.

---

## 2. The rulings

**R1. Presence never activates.** A loophole is active only if something said so.

**R2. A pack declares its loophole's default state, and that declaration defaults to disabled.**
`default_enabled`, on the loophole manifest. Absent means off. This is what lets a pack "do the
right thing by default" without yolo guessing on its behalf.

**R3. `requires.command_on_path` is deleted from the schema.** Not corrected — deleted. It is the
sniffing mechanism itself, and both of its uses are the argument against it: one is wrong for the
product's main case, the other is a formality on a POSIX staple. A loophole whose program is missing
should fail loudly at spawn, not vanish silently from a list.

**R4. Host access is never on by default.** `audio` ships `default_enabled: false`. Being useful is
not a reason to be automatic.

**R5. Install is user-scope; enable is either scope.** Already true and kept: `packs` is read from
the user config only (`internal/config/packs.go`), install-shaped keys are refused in workspace
scope, and `loopholes.<name>.enabled` is honored from both. So a workspace may switch on only what
the user already installed — the weak, agent-editable scope is bounded by the strong one, which is
what makes per-workspace enablement safe to offer at all.

**R6. The broker's loophole moves inside `packs/claude/`.** It exists only to serve claude, so
selecting the claude pack is the dependency — and R3's deletion is then free rather than a
regression, because the sniff was standing in for exactly this.

---

## 3. What this does NOT license

- **Not** a second gate over host execution. `default_enabled` feeds `Enabled`; a fetched pack's
  host crossing still needs `Active` and `Honored`, so declaring yourself default-on cannot buy
  host access without the origin gate's approval. Adding an origin restriction *specifically* to
  `default_enabled` would be the halfway-measure shape [OQ-LP14 was criticized for](loophole-packaging-overview.md).
- **Not** a change to `requires.file_exists`, which stays. It answers "can this machine run it",
  which is a real question — `audio` uses it — and it does not decide activation on its own.
- **Not** pack-level dependencies. R6 avoids needing them; nothing here introduces a pack that
  depends on another pack.
- **Not** a change to the three-layer model. `Enabled`/`Active`/`Honored` are right; only what feeds
  `Enabled` changes.

---

## 4. What it costs

**Every currently-active loophole goes dark on upgrade** unless its pack declares
`default_enabled: true` or the user enables it. That is the ruling working as intended, but it is a
silent behaviour change for anyone already relying on `yolo-ps` or `audio`, and silence is the part
worth fixing: a one-time launch notice naming what *was* active and the exact line to restore it
costs little and turns a mystery into a decision. **Open — see OQ-A2.**

**The broker gains a way to be silently off.** Off by default with no host claude was already the
status quo (§1.1) — the difference is that after R6 it is off because you did not select the claude
pack, which is at least legible. If it ends up default-disabled *inside* the claude pack (OQ-A1),
the failure mode is a burned refresh token, and `requires` is gone as a place to warn from.

**`Active` gets thinner.** With `command_on_path` deleted, `requires` is just `file_exists`. That is
a simplification, not a loss — but the "loophole silently inactive" reports it used to produce were
at least *diagnosable*, and a missing program now surfaces as a daemon that fails to spawn. Worth
checking that failure reads well before shipping.

---

## Open Questions

1. **OQ-A1 — is the broker `default_enabled: true` inside the claude pack, or off like everything
   else?**

   R4 says host access is never on by default; the maintainer also observed that *"the oauth broker
   is host incidental only really — you could run the broker in a container too if you wanted"*,
   which says its host-ness is not its defining property. Those two pull in opposite directions and
   this is where they meet. What it decides: whether installing the claude pack gives you working
   token serialization, or whether that is a second step you can forget.

   _Leaning:_ **`default_enabled: true`, inside the claude pack.** The thing being switched on is
   not "host access" in any sense the user chose it for — it is *part of running claude correctly*,
   and its absence is not a missing feature but a silently corrupted credential shared across every
   jail. R4's principle is about not reaching onto a host the user never pointed us at; selecting
   the claude pack is pointing us at it. If that reads as an exception to R4, the honest framing is
   that R4 governs *reaching for a host resource* and this governs *doing the job the pack exists to
   do*.

   **Answer:**
   > _(empty — fill in when decided)_

2. **OQ-A2 — does the upgrade say anything, or is it a silent clean break?**

   Everyone's active loopholes go dark. Options: silent; a one-time launch notice naming them with
   the enable line; or a migration that writes the current set into user config as explicit
   `enabled: true` entries (preserving behaviour, at the cost of the ruling not actually applying to
   existing users).

   _Leaning:_ **the one-time notice.** A migration that re-enables everything makes the ruling a
   no-op for exactly the people who already have host daemons running, which is backwards. Silence
   is cheap for us and expensive for them.

   **Answer:**
   > _(empty — fill in when decided)_

3. **OQ-A3 — does `default_enabled: true` stay available to *fetched* packs?**

   §3 argues no extra gate is needed, because the origin gate already stands between a fetched pack
   and the host. But "a pack I fetched can declare itself on" is a sentence worth reading twice
   before it is true.

   _Leaning:_ **yes, unrestricted.** The origin gate is the gate; a second rule keyed on the same
   act is the thing this repo keeps deleting. Note the practical bound: a fetched pack still cannot
   be *selected* without a user editing their user-scope config, so declaring yourself default-on
   changes nothing until someone installs you deliberately.

   **Answer:**
   > _(empty — fill in when decided)_
