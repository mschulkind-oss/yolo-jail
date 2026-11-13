package check

// noRuntimeGolden is the pinned ANSI-stripped full output of Check() over the
// no-runtime / not-in-jail / no-repo-root fixture (paths normalized to $HOME).
// This golden fixes the section ordering, badge semantics, note indent,
// and the pass/warn/fail counts. Regenerate only via a deliberate golden bump.
const noRuntimeGolden = `
YOLO Jail Check

Version: 9.9.9-test

Container Runtime
  [FAIL] No container runtime installed
       -> None of apt, dnf or pacman is on this PATH, so yolo has no install line to name.
          Install Podman with your distribution's command, listed at https://podman.io/docs/installation
          then: yolo check

Nix
  [FAIL] nix not found
       -> Install Nix with the NixOS Nix installer (its --extra-conf makes the Nix daemon trust you):
            curl -sSfL https://artifacts.nixos.org/nix-installer | sh -s -- install --extra-conf "extra-trusted-users = $(whoami)"
          then, in a new terminal: yolo check

Global Storage
  [WARN] Home directory missing: $HOME/.local/share/yolo-jail/home
       -> Will be created on first run
  [WARN] Mise (jail store) directory missing: $HOME/.local/share/yolo-jail/mise
       -> Will be created on first run
  [WARN] Containers directory missing: $HOME/.local/share/yolo-jail/containers
       -> Will be created on first run
  [WARN] Agents directory missing: $HOME/.local/share/yolo-jail/agents
       -> Will be created on first run
  [WARN] Build directory missing: $HOME/.local/share/yolo-jail/build
       -> Will be created on first run

Config Files
  [PASS] No user config found: $HOME/.config/yolo-jail/config.toml
  [PASS] No workspace config found (yolo-jail.toml; a project needs none, and ` + "`yolo init`" + ` writes one)

Resource Groups
  Resource groups: disabled (no workspace mapping)

  [FAIL] Could not resolve the yolo-jail repo root
       -> The yolo CLI needs a complete matched flake/helper bundle for jail image builds.
          Fix: Reinstall yolo from Homebrew or a release archive, which include the build files each jail is made from (see "Other ways to install" at https://docs.yolo-jail.mschulkind.dev); a ` + "`go install`" + ` or pipx install does not include them. In a yolo-jail checkout, run ` + "`just install`" + `, or run ` + "`just build-dev-bundle`" + ` and then the launcher it prints.
          Then run ` + "`yolo check`" + ` again.
Merged Configuration
  - No runtime available — see Container Runtime above

Scope
  - Disk usage: inventory and reclaim are separate from readiness checks — yolo stores / yolo prune
  - Not reached: packs and subsequent readiness checks

Summary
  3 failed, 5 warnings

`
