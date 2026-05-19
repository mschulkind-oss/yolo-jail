"""Terminal-integration helpers: tmux pane/window labels, kitty tab title,
and the start-of-run banner that prints version/runtime/cname to stderr.

All functions are best-effort: they no-op silently when the host
terminal feature isn't present (no TMUX, not a kitty session, no isatty,
or YOLO_NO_TMUX=1).
"""

import os
import platform
import subprocess
import sys
from pathlib import Path


def _get_project_name() -> str:
    """Return the jail project label: SM_PROJECT if set, else cwd basename."""
    return os.environ.get("SM_PROJECT") or Path.cwd().name


def _tmux_rename_window(name: str):
    """Rename the current tmux window. No-op if not in tmux or YOLO_NO_TMUX=1."""
    if os.environ.get("YOLO_NO_TMUX") == "1":
        return
    if os.environ.get("TMUX"):
        try:
            subprocess.run(
                ["tmux", "rename-window", name],
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
            )
        except Exception:
            pass


def _kitty_setup_jail_tab():
    """Set kitty tab title and color for jail indicator. Returns cleanup function or None."""
    if not os.environ.get("KITTY_PID") or not sys.stdin.isatty():
        return None

    project = _get_project_name()
    window_id = os.environ.get("KITTY_WINDOW_ID", "")
    match_arg = f"id:{window_id}" if window_id else "recent:0"

    def _kitten_run(cmd_args):
        try:
            subprocess.run(
                ["kitten", "@", *cmd_args],
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
            )
        except Exception:
            pass

    try:
        old_title = (
            subprocess.check_output(
                ["kitten", "@", "get-tab-title", "--match", match_arg],
                stderr=subprocess.DEVNULL,
            )
            .decode()
            .strip()
        )
    except Exception:
        old_title = ""

    try:
        subprocess.run(
            [
                "kitten",
                "@",
                "set-tab-title",
                "--match",
                match_arg,
                f"🔒 JAIL {project}",
            ],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
    except Exception:
        return None

    # Turn the tab red
    _kitten_run(
        [
            "set-tab-color",
            "--match",
            match_arg,
            "active_bg=#cc0000",
            "active_fg=#ffffff",
            "inactive_bg=#880000",
            "inactive_fg=#cccccc",
        ]
    )

    def restore():
        _kitten_run(["set-tab-title", "--match", match_arg, old_title or "bash"])
        # Reset tab colors to kitty.conf defaults
        _kitten_run(
            [
                "set-tab-color",
                "--match",
                match_arg,
                "active_bg=none",
                "active_fg=none",
                "inactive_bg=none",
                "inactive_fg=none",
            ]
        )

    return restore


def _tmux_setup_jail_pane():
    """Set tmux pane border indicators for the jail. Returns cleanup function."""
    if os.environ.get("YOLO_NO_TMUX") == "1":
        return None
    if not os.environ.get("TMUX") or not sys.stdin.isatty():
        return None

    pane = os.environ.get("TMUX_PANE", "")
    jail_dir = _get_project_name()

    def _tmux_opt(opt):
        try:
            r = subprocess.run(
                ["tmux", "show-option", "-pt", pane, opt],
                capture_output=True,
                text=True,
            )
            if r.returncode == 0 and r.stdout.strip():
                # Output is "option-name value" — extract value after first space
                parts = r.stdout.strip().split(None, 1)
                return parts[1] if len(parts) > 1 else ""
            return None
        except Exception:
            return None

    def _tmux_set(opt, val):
        try:
            subprocess.run(
                ["tmux", "set-option", "-pt", pane, opt, val],
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
            )
        except Exception:
            pass

    # Save old state
    old = {
        opt: _tmux_opt(opt)
        for opt in [
            "pane-border-style",
            "pane-active-border-style",
            "pane-border-status",
            "pane-border-format",
        ]
    }
    old_window = None
    old_auto_rename = None
    try:
        r = subprocess.run(
            ["tmux", "display-message", "-p", "#{window_name}"],
            capture_output=True,
            text=True,
        )
        old_window = r.stdout.strip() if r.returncode == 0 else None
        r = subprocess.run(
            ["tmux", "show-window-option", "-v", "automatic-rename"],
            capture_output=True,
            text=True,
        )
        old_auto_rename = r.stdout.strip() if r.returncode == 0 else None
    except Exception:
        pass

    # Set jail indicators
    _tmux_set("pane-border-style", "fg=red,bold")
    _tmux_set("pane-active-border-style", "fg=red,bold")
    _tmux_set("pane-border-status", "bottom")
    _tmux_set("pane-border-format", f" 🔒 JAIL {jail_dir} ")
    try:
        subprocess.run(
            ["tmux", "set-window-option", "automatic-rename", "off"],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
        subprocess.run(
            ["tmux", "rename-window", "JAIL"],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
    except Exception:
        pass

    def restore():
        # Batch all tmux restores into a single command to minimize shutdown delay
        cmds = []
        for opt, val in old.items():
            if val is not None:
                cmds.append(f"set-option -pt {pane} {opt} {val}")
            else:
                cmds.append(f"set-option -put {pane} {opt}")
        if old_window:
            cmds.append(f"rename-window {old_window}")
        if old_auto_rename == "on":
            cmds.append("set-window-option automatic-rename on")
        if cmds:
            try:
                # Execute all restores in one tmux invocation using \;
                full_cmd = ["tmux"]
                for i, cmd in enumerate(cmds):
                    if i > 0:
                        full_cmd.append(";")
                    full_cmd.extend(cmd.split())
                subprocess.run(
                    full_cmd, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL
                )
            except Exception:
                pass

    return restore


def _print_startup_banner(
    version: str,
    runtime: str,
    cname: str,
    res_parts: "list[str] | None" = None,
    jail_version: "str | None" = None,
):
    """Print startup info to stderr for debugging and log sharing.

    ``version`` is the host CLI's version (what's running right now).
    ``jail_version``, if given, is the ``YOLO_VERSION`` baked into the
    already-running container — shown only when it differs from the
    host version, since that's the gap that silently causes stale
    shims / stale mounts / stale entrypoint logic on attach.
    """
    host_platform = f"{sys.platform}/{platform.machine()}"
    if jail_version and jail_version != version:
        ver_part = f"yolo-jail {version} (attached to jail built at {jail_version})"
    else:
        ver_part = f"yolo-jail {version}"
    parts = [ver_part, host_platform, runtime, cname]
    print(" | ".join(parts), file=sys.stderr)
    if res_parts:
        print(f"Resource limits: {', '.join(res_parts)}", file=sys.stderr)
