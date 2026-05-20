"""Inject host git / jj identity into the jail.

cli.py forwards ``YOLO_GIT_NAME``, ``YOLO_GIT_EMAIL``,
``YOLO_GLOBAL_GITIGNORE``, ``YOLO_JJ_NAME``, and ``YOLO_JJ_EMAIL`` from
the host's git/jj config so commits made inside the jail attribute to
the right person without the agent ever seeing the host's
``~/.gitconfig`` or ``~/.config/jj/`` directly.

Both functions are best-effort — if git or jj isn't on PATH yet (rare,
since the image guarantees git; jj is opt-in via mise_tools), they
silently no-op.
"""

import os
import shutil
import subprocess
from pathlib import Path


def configure_git():
    """Set git name, email, and global gitignore from host env vars."""
    if not shutil.which("git"):
        return
    env = os.environ
    if env.get("YOLO_GIT_NAME"):
        subprocess.run(
            ["git", "config", "--global", "user.name", env["YOLO_GIT_NAME"]],
            capture_output=True,
        )
    if env.get("YOLO_GIT_EMAIL"):
        subprocess.run(
            ["git", "config", "--global", "user.email", env["YOLO_GIT_EMAIL"]],
            capture_output=True,
        )
    gitignore = env.get("YOLO_GLOBAL_GITIGNORE", "")
    if gitignore and Path(gitignore).is_file():
        subprocess.run(
            ["git", "config", "--global", "core.excludesFile", gitignore],
            capture_output=True,
        )


def configure_jj():
    """Set jj user identity from host env vars."""
    if not shutil.which("jj"):
        return
    env = os.environ
    if env.get("YOLO_JJ_NAME"):
        subprocess.run(
            ["jj", "config", "set", "--user", "user.name", env["YOLO_JJ_NAME"]],
            capture_output=True,
        )
    if env.get("YOLO_JJ_EMAIL"):
        subprocess.run(
            ["jj", "config", "set", "--user", "user.email", env["YOLO_JJ_EMAIL"]],
            capture_output=True,
        )
