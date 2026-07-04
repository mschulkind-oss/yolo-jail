# Handoff: reply to open GitHub PRs (host-side, needs gh credentials)

Audience: an agent (or Matt) running **on the host** with `gh` authenticated.
The in-jail agent has no GitHub write access; everything below is already
done in-tree — your job is only the PR communication. Verify the referenced
commits are **pushed to main** before replying (replies cite hashes).

## PR #23 — dependabot: actions/checkout 5 → 7

Superseded by commit `6be6290`, which applies the identical bump plus one
pin dependabot's branch predates (`.github/workflows/update-flake-lock.yml`).

Action: after push, dependabot usually notices main already has the bump
and closes the PR itself within a day. If it doesn't:

```sh
gh pr close 23 --comment "Superseded by 6be6290, which applies the same bump plus the update-flake-lock workflow added after this PR was opened. Thanks, dependabot."
```

## PR #21 — kurt-hs: podman VM memory warnings + editable-install hardening

**Do not merge.** The branch targets `src/cli.py`, the pre-package-split
monolith — it cannot rebase onto the current layout. All three changes were
instead ported by hand to `PORT_COMMIT` with
`Co-authored-by: Kurt Galiatsatos <kurt.galiatsatos@hyperscience.com>` so
authorship lands in the history:

| PR piece | Where it lives now |
|---|---|
| `_podman_machine_memory` / floor / resize hint | `src/cli/runtime.py` (shared helpers) |
| `_check_podman_machine_resources` + `yolo check` wiring | `src/cli/check_cmd.py` (hooked after the "Podman Machine: available" probe) |
| `_maybe_warn_about_oom_killer` (exit-137 hint) | `src/cli/run_cmd.py`, wired at all three container-exit paths (attach, race-attach, main teardown) |
| Justfile editable-install hardening (`env -u VIRTUAL_ENV` python pinning) | `Justfile` — applied essentially verbatim |
| Tests | rewritten against the package layout in `tests/test_podman_machine_advisory.py` (18 tests) |

Behavior was preserved intentionally, including the advisory-only posture,
the 4096 MB floor, the hedged "often means" phrasing on exit 137, and the
VM-restart caveat in the resize hint.

Action — comment and close (adapt tone as you like, keep the substance):

```
Thanks for this — the diagnosis and the advisory-only design were both
spot-on, and all three pieces are now on main as PORT_COMMIT with your
co-authorship: the machine-memory check in `yolo check`, the exit-137
OOM hint (at all three container-exit paths), and the Justfile
editable-install hardening, plus your test matrix rewritten against the
current module layout.

Closing rather than merging only because the repo has been restructured
since May (src/cli.py split into a package, plus a large mount/
provisioning rework this week), so the branch has no rebase path.
Sorry it sat so long — much appreciated.
```

```sh
gh pr close 21
```

## Cleanup

Delete this file after both PRs are handled.
