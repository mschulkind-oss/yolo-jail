# Host↔jail state separation — the one open question

**Status:** GRADUATED, 2026-07-03 — the design shipped and its as-built account is
[`../reference/jail-state-separation-design.md`](../reference/jail-state-separation-design.md) —
read that for the split mise store, the neutral `/mise` path, per-side venv shadows, the
jail↔jail store residue and its gated prune, the migration, and the `SS-1`…`SS-5` rulings.

This file exists only to hold the one question that is still open, so that a reference doc does not
carry a live `💬`. Nothing else belongs here.

**Needs your ruling:** [SS-6](#ss-6) (file the upstream mise issue for the project-agnostic store key).

**SS-6, 2026-09-25:** mise's issues, pull requests and Discussions were searched, and no report
of this defect exists. A draft for you to file is under the question. Upstream's own behavior
moved in August 2026 (#11798), and that is recorded there too.

## Open Questions

1. <a id="ss-6"></a>💬 **SS-6: File the upstream mise issue for the project-agnostic store key.**
   The root defect is that mise's rust backend writes a *project-configured* value
   (`$CARGO_HOME/bin`) into a store entry keyed only by `(tool, version)`. The three layers that
   shipped — no-op for the common case, a host-gated prune, and a `mise.jail.toml` escape hatch —
   **heal** the symptom; nothing yet **fixes** it, so every future backend that records a
   side-specific path reintroduces the class. This decides whether yolo carries the gated-prune
   machinery indefinitely or eventually deletes it.

   _Leaning:_ File it, expect nothing, keep the prune. *"Worth filing; not a fix to wait on"* was
   the original leaning and nothing has changed it — but the filing itself is still unrecorded,
   which is the only reason this is open rather than settled. The search below found no existing
   report, so filing is still the open step. A draft follows the search.

   **What exists upstream, SOURCED 2026-09-25.** Searched: mise's issues and pull requests
   (GitHub search API), and its Discussions (GitHub GraphQL). Discussions is where the project
   takes bug reports: the rust fixes [#11794](https://github.com/jdx/mise/pull/11794) and
   [#11798](https://github.com/jdx/mise/pull/11798) each cite a Discussion in its
   "Troubleshooting and bug reports" category. The search terms combined `CARGO_HOME`, `cargo home`, `symlink`,
   `installs`, `config_root`, `shared`, `MISE_DATA_DIR`, "same version" and "two projects".

   - **Nothing describes this defect**, that is, a store entry keyed by tool and version whose
     value comes from a project's config.
   - **Upstream now re-points the entry on every install.**
     [#11798](https://github.com/jdx/mise/pull/11798), merged 2026-08-09 and shipped in
     v2026.8.4, resolves the Cargo home from the evaluated config `[env]`. It also "reinstall[s]
     an already tracked toolchain when its mise install symlink points to a previous Cargo
     home". READ FROM the v2026.8.6 source (`src/plugins/core/rust.rs`): the install writes
     `installs/rust/<version>` as a symlink to the resolved proxy directory. And
     `is_install_satisfied` returns false when the symlink points elsewhere.
   - **MEASURED 2026-09-25 on mise 2026.8.6**, the version this jail runs (`mise --version`).
     The draft's repro below was run in throwaway directories. They had their own `HOME`,
     `MISE_DATA_DIR`, cache, state and config dirs. The ambient `CARGO_HOME` and `RUSTUP_HOME`
     pointed at empty temp dirs, so nothing touched `/mise`. Two projects, `a` and `b`, each
     pinned `rust = { version = "1.85.0", profile = "minimal" }` with `CARGO_HOME` and
     `RUSTUP_HOME` under `{{ config_root }}`. Each install took about 6 s.
     - `installs/rust/1.85.0` flipped to the installing project's `.cargo/bin` on every
       `mise install`: `a`, then `b`, then `a`, then `b`.
     - `mise exec` in `a`, while the entry pointed into `b`, auto-installed and re-pointed it.
     - **Resolution does not read the entry.** In `a`, while the entry pointed into `b`,
       `mise bin-paths` and `mise which cargo` both named `a/.cargo/bin`. In `b`, while the
       entry pointed into `a`, `mise env` exported `b`'s own `CARGO_HOME` and `PATH`, and the
       `cargo` shim ran without re-pointing the entry.
     - `mise where rust` returned the shared entry path. That path's target was the other
       project's `.cargo/bin`.

     So on this version the harm for rust is narrower than the reference's residue table used
     to describe. The entry flips on every install or exec, and `where` names another project's
     directory, but the toolchain does not break mid-session through a shim.
   - **The reference's residue table was updated for this on 2026-09-25**, in
     [`../reference/jail-state-separation-design.md`](../reference/jail-state-separation-design.md#the-jailjail-residue-in-the-shared-store).
     Two rows, and fact 2, now carry the 2026.8.6 behavior. The shim half above was re-run for
     that update, and so was one more case: with the entry deleted, as a rust-less jail's prune
     would, the next `cargo` shim run re-installed the version and re-created the entry, with
     mise's auto-install at its default.
   - **Nearest reports, none of them this defect.**
     [#8943](https://github.com/jdx/mise/discussions/8943) (`[env]` Cargo home ignored; fixed
     by #11798).
     [#7811](https://github.com/jdx/mise/discussions/7811) (Ideas, open: the same
     `{{ config_root }}/…` Cargo home pattern, used for a native/docker split, asking for
     ergonomics).
     [#8549](https://github.com/jdx/mise/discussions/8549) and
     [#8581](https://github.com/jdx/mise/pull/8581), merged 2026-03-13 (shared and system
     install directories, which make a shared store more common).
     [#4023](https://github.com/jdx/mise/issues/4023) (closed 2025: the rust backend ignored
     `CARGO_HOME`).

   **Draft for you to file**, as a Discussion in "Troubleshooting and bug reports". The repro
   is the one measured above. It uses `mise trust` where the measurement used
   `MISE_TRUSTED_CONFIG_PATHS`.

   > **rust: `installs/rust/<version>` records a project-configured Cargo home, so projects
   > sharing a data dir keep re-pointing one entry**
   >
   > The rust backend stores a toolchain as a symlink `installs/rust/<version>` pointing to
   > the resolved Cargo proxy directory (`$CARGO_HOME/bin` for a managed home). That path is
   > keyed by tool and version only. Since #11798 the Cargo home can come from a project's
   > `[env]`, for example `CARGO_HOME = "{{ config_root }}/.cargo"`. So two projects that pin
   > the same version with different Cargo homes share one entry that can point at only one of
   > them. `is_install_satisfied` treats a symlink into another Cargo home as not installed.
   > So every `mise install`, and every auto-installing `mise exec`, in either project
   > re-points the entry.
   >
   > Resolution itself is fine: `mise env`, `mise which` and the shims all use each project's
   > own Cargo home. What goes wrong is everything that reads the entry:
   >
   > - `mise where rust` returns `installs/rust/<version>`, and that resolves into the other
   >   project's `.cargo/bin`.
   > - The entry is removed and re-created by whichever project installed or exec'd last.
   >   A project that removes its `.cargo` leaves the entry dangling for everyone else who
   >   shares the data dir.
   >
   > Repro on 2026.8.6:
   >
   > ```sh
   > export MISE_DATA_DIR="$(mktemp -d)"
   > mkdir -p a b
   > printf '[env]\nCARGO_HOME = "{{ config_root }}/.cargo"\nRUSTUP_HOME = "{{ config_root }}/.rustup"\n\n[tools]\nrust = { version = "1.85.0", profile = "minimal" }\n' > a/mise.toml
   > cp a/mise.toml b/mise.toml
   > (cd a && mise trust && mise install); readlink "$MISE_DATA_DIR/installs/rust/1.85.0"   # .../a/.cargo/bin
   > (cd b && mise trust && mise install); readlink "$MISE_DATA_DIR/installs/rust/1.85.0"   # .../b/.cargo/bin
   > (cd a && mise where rust)   # $MISE_DATA_DIR/installs/rust/1.85.0, which resolves into b
   > (cd a && mise install); readlink "$MISE_DATA_DIR/installs/rust/1.85.0"                  # .../a/.cargo/bin again
   > ```
   >
   > Expected: an entry that records nothing project-specific, or one entry per version and
   > resolved homes. Actual: one entry, owned by whichever project installed or exec'd last.
   >
   > We hit this with several sandboxed containers (yolo-jail) sharing one data dir. Each
   > mounts its project at the same path, so the common case is harmless: the strings match.
   > Any two projects whose Cargo home strings differ fight over the entry. Possible fixes,
   > in the order we would prefer them:
   >
   > 1. Make the install entry hold nothing project-specific, since resolution already reads
   >    the evaluated config.
   > 2. Key the install entry by the resolved homes as well as the version.
   > 3. At least document that a project-level `CARGO_HOME` makes `installs/rust/<version>`
   >    per-project, and warn when an install re-points an entry.
   >
   > Related: #11798, #8943, #7811, #8581.

   **Answer:**
   > _(empty — fill in when decided)_

Dispositioned on the roadmap as one of the *deliberately not* rows: it concerns a shipped mechanism
working as designed, not a gap, and it blocks nothing.
