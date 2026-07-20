package check

// noRuntimeGolden is the pinned ANSI-stripped full output of Check() over the
// no-runtime / not-in-jail / no-repo-root fixture (paths normalized to $HOME).
// This is a Go-native golden (not a diff against Python) per the Stage-15
// output contract: it fixes the section ordering, badge semantics, note indent,
// and the pass/warn/fail counts. Regenerate only via a deliberate golden bump.
const noRuntimeGolden = `
YOLO Jail Check

Version: 9.9.9-test

Container Runtime
  [FAIL] No container runtime installed
       -> Install one:
            Linux:  your package manager, e.g. ` + "`sudo apt install podman`" + `
            macOS:  ` + "`brew install podman`" + ` then ` + "`podman machine init && podman machine start`" + `,
                    or ` + "`brew install container`" + ` then ` + "`container system start`" + `

Nix
  [FAIL] nix not found
       -> Install Nix: https://nixos.org/download/

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
  [PASS] No user config found: $HOME/.config/yolo-jail/config.jsonc
  [PASS] No workspace yolo-jail.jsonc found

  [FAIL] Could not resolve the yolo-jail repo root
Merged Configuration
  [FAIL] No container runtime found on PATH

Summary
  4 failed, 5 warnings

`
