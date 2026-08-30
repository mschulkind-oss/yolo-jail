# Unblocking the deferred environment-manager work

**Status:** HANDOVER — still live, re-checked **2026-08-23**. All four items below remain unbuilt,
and the reason is unchanged for each: three need hardware or a terminal this jail does not have, and
the fourth is in-jail work nobody has picked up. **One correction to the sentence below:** Phase 9
(agent autonomy as a notch policy) has shipped too, so the plan's built set is Phases **0–6, 8 and
9**, and **Phase 7 — the `guest` notch, items 1 and 2 here — is the only unbuilt phase**.
Item 3's half-state is worth knowing before you start it: `yolo check-deps` exists and is
deliberately the *probe* half — *"It NEVER installs anything … the offer-to-run belongs to `apply` at
a lower notch"* (`internal/cli/checkdeps.go:9-12`) — so what is missing is the offer, not the
detection.

The environment-manager plan shipped Phases 0–6 and 8. Four things were deferred.
This doc is the **handover**: for each one, what it is, *why* it couldn't be finished
here, and exactly what **you** need to do — on your own hardware — to unblock it.

Your assumption ("mostly just testing on my Mac") is right for the biggest item, but
not the whole story. Of the four:

| # | Deferred item | What unblocks it | Needs your Mac? |
|---|---|---|---|
| 1 | **macOS `guest` notch** (Phase 7.1) | run + verify on a real Mac | **Yes** — only you have one |
| 2 | **Linux `guest` notch** (Phase 7.2) | a real Linux host (Landlock + userns) | No — a Linux box, not the jail |
| 3 | **`yolo host apply` offer-to-run installs** (Phase 6.4) | an interactive terminal on any real machine | Either OS |
| 4 | **No-exec jail provision** (Phase 3) | nothing from you — it's pure in-jail dev | **No** — not gated on you at all |

Item 4 is not waiting on you at all — flagged here so it doesn't get lost behind the
Mac items. The **per-notch briefing body** (Phase 8.2) isn't in this table because it
has no work of its own: it's one paragraph that writes itself once items 1–2 give it a
real guest/host boot to describe.

The full design and acceptance criteria live in
[environment-manager-plan.md](environment-manager-plan.md) (Phases 3, 6, 7, 8) and
[../design/yolo-as-environment-manager.md](../design/yolo-as-environment-manager.md) §4.
This doc is only the "what I, the human, must provide" layer.

---

## Why any of this needs *you*

Everything else in the plan was verifiable **inside a nested jail** — the standard
in-jail fast loop (`yolo -- bash` rebuilds the flake and runs the new image). The
deferred items are exactly the ones that step **outside** that loop:

- A **nested Linux jail cannot exercise an LSM sandbox.** Landlock and `bwrap` need
  capabilities and an unprivileged-user-namespace posture the doubly-nested container
  doesn't have (`--userns=host`, `--cgroups=disabled`; see AGENTS.md "Podman-in-podman").
  So the `guest` backends compile and unit-test here, but a real confinement boundary
  can only be *proven* on an un-nested host.
- **macOS does not exist in this jail at all.** Seatbelt (`sandbox-exec`), the
  `_yolojail` service user, and native `aarch64-darwin` nix packages are darwin-only.
- **A `sudo` password prompt cannot be shown through** a non-interactive test harness.
  The offer-to-run design deliberately shows the OS's own prompt (never captures it),
  which needs a real TTY on a real machine.

So the gate is honest, not incidental: these need hardware and an interactive session.

---

## Item 1 — macOS `guest` notch (Phase 7.1) — **your Mac**

**What it is.** `confinement: guest` on macOS: the agent runs as the hidden `_yolojail`
service user under Apple Seatbelt, in a **real home on the real filesystem, no VM, no
image** — with your packs' full portable surface set rendered into it. This is the
existing `macos-user` backend, now driven by the confinement dial and rendering surfaces
(the zero-surfaces bug was fixed in Phase 1.4). See
[../guides/macos.md](../guides/macos.md) for the backend today.

**Why it's blocked here.** No macOS, no Seatbelt, no `_yolojail`.

**What you need to do:**

1. On your Mac, from a yolo checkout, set `confinement: guest` in a scratch config and
   run a non-agent probe:
   ```console
   $ yolo describe                 # should print: confinement guest, composed primitives
   $ yolo -- true                  # provision the guest home + exit (no agent, no API call)
   ```
2. Confirm the guest home is a **real** dir under `_yolojail` (not a container overlay),
   that your packs' `config`/`skills`/`briefing`/`env` surfaces rendered into it, and
   that Seatbelt is actually confining it (a write outside the allowed paths is denied).
3. Confirm `describe` names the composed primitives (`separate-user` + `Seatbelt`), per
   Phase 7's "done when."

**Prerequisites on the Mac:** Nix installed and your user **trusted** (`yolo check`
flags it if not); the `macos-user` backend selectable (`YOLO_RUNTIME=macos-user` or the
`runtime` key). macos.md §Prerequisites is the authority.

**Acceptance bar** (from memory `project_macos_dual_track`): the revival bar for the
native path is "native darwin nix `packages:` resolve and run." Exercise a `packages:`
entry in the guest home, not just the agent.

---

## Item 2 — Linux `guest` notch (Phase 7.2) — **a real Linux host**

**What it is.** `confinement: guest` on Linux: `bwrap` + Landlock giving a real home
with an LSM boundary — "a weaker container, no separate user, no image." This is the
fourth primitive composition the Phase 2 primitive model was built to express; it needs
no new concept, just a real kernel to enforce it.

**Why it's blocked here.** The nested jail can't create the namespaces/Landlock ruleset
(see "Why any of this needs you" above). **This is NOT a Mac task** — it's the one piece
your Mac can't help with. It needs a real Linux machine.

**What you need to do** (on a Linux host — bare metal, VM, or a *non-nested* jail host):

1. Verify the kernel prerequisites:
   ```console
   $ uname -r                                    # Landlock: ≥5.13; full ABI ≥6.7
   $ cat /proc/sys/kernel/unprivileged_userns_clone   # want 1 (or the sysctl absent)
   $ which bwrap
   ```
2. `confinement: guest`, then `yolo describe` (expect the `bwrap`+`Landlock` primitives)
   and `yolo -- true` to provision the confined home.
3. Prove the boundary: a write to a path outside the Landlock ruleset is **denied**, and
   a path inside it succeeds.

**Prerequisites:** a kernel with Landlock enabled, unprivileged user namespaces allowed,
`bwrap` on PATH. This can be the *same* physical host that runs your jails — just run the
`guest` probe from the **host**, not from inside a jail.

---

## Item 3 — `yolo host apply` offer-to-run installs (Phase 6.4) — **an interactive terminal**

**What it is.** Today `check-deps` *names* missing host tools and writes the manifest
(`~/.config/yolo/Brewfile` + apt/dnf/pacman kin); it never installs. The deferred piece
is the **confirm-gated offer-to-run**: after naming what's missing, offer to run the
remedies, **batched by elevation class** — one approval for all no-sudo installs, one for
all `sudo` ones, with `sudo` **first** so the OS password prompt (shown through, never
captured) appears once at the front. Never ambient; no-TTY falls back to print-only; the
manifest is always the floor.

**Why it's blocked here.** The batched-confirm UX and the pass-through `sudo` prompt need
a real interactive TTY and a real package manager to invoke — neither exists in the test
harness (and this jail's `DetectManager` returns `nix`, with no elevation prompt to
exercise). Automated tests must never actually install.

**What you need to do** (either OS, on a real machine with a real package manager):

1. Author or install a pack whose `program` declares `install_hints` for a tool you don't
   have, then:
   ```console
   $ yolo check-deps               # names the miss, writes ~/.config/yolo/<manifest>
   $ yolo host apply               # observe — should list the miss + the would-run remedy
   ```
2. When the offer-to-run lands, verify the **batching**: no-sudo remedies get one
   confirm; `sudo` remedies get one confirm **first**, and your OS's own password prompt
   comes up (yolo never echoes or stores it).
3. Verify the **no-TTY floor**: piped/non-interactive, it prints the manifest and runs
   nothing.

This is the one item that only needs a real machine and your hands — a Mac (brew) or a
Linux box (apt/dnf/pacman) both work. It does **not** need the guest backends.

---

## Item 4 — No-exec jail provision (Phase 3) — **no hardware; in-jail dev**

**What it is.** At the jail notch, `yolo apply` currently *reports* what a launch would
provision and directs you to `yolo -- true`. A dedicated provision-without-launch path
(build image, stage packs, render config, then exit — no exec) is the follow-up.

**Why it's deferred.** It's the existing run pipeline minus the exec; wiring a no-exec
mode through the lifecycle was scoped out of Phase 3, not blocked by hardware.

**What you need to do:** **nothing on your hardware.** This is ordinary in-jail work —
add the no-exec mode to the run pipeline and verify it with the standard nested-jail loop
(`./dist-go/linux-$(go env GOARCH)/yolo apply` provisions and exits without launching an
agent). Listed here only so it isn't mistaken for a Mac-gated item.

---

## Suggested order

1. **Item 4** first — no hardware, unblocks a cleaner `apply` story and can land any time.
2. **Item 1** (your Mac) and **Item 2** (a Linux host) — the guest notch, the headline
   deferral. Do them close together so **Phase 8.2** (the per-notch briefing body) can be
   verified against a real guest boot immediately after.
3. **Item 3** last — the install offer-to-run is independent and lower-risk; it slots in
   whenever you have a real machine with a package manager in front of you.

Once Items 1–2 land, the plan's Phase 7 "done when" and Phase 8 per-notch body are both
checkable, and the environment-manager plan is fully closed.
