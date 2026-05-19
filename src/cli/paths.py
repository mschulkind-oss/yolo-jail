"""Module-level constants used across the cli package.

Lives in its own module so it can be imported from any cli/* submodule
without dragging in the rest of the cli __init__ (which transitively
loads typer, rich, pyjson5, the loopholes runtime, etc.).
"""

import sys
from pathlib import Path

IS_LINUX = sys.platform == "linux"
IS_MACOS = sys.platform == "darwin"

# Supported container runtimes.  Docker was removed — podman is the
# first-class Linux runtime (rootless, daemonless, cgroup delegation
# that matches how yolo-cglimit talks to the host), and Apple Container
# is the native macOS runtime on Tahoe+.  Any config that sets
# runtime: "docker" gets a migration error; see _validate_config.
SUPPORTED_RUNTIMES = ("podman", "container")

JAIL_IMAGE = "localhost/yolo-jail:latest"
# Apple Container CLI doesn't recognize the localhost/ prefix
JAIL_IMAGE_SHORT = "yolo-jail:latest"

GLOBAL_STORAGE = Path.home() / ".local/share/yolo-jail"
GLOBAL_HOME = GLOBAL_STORAGE / "home"
GLOBAL_MISE = GLOBAL_STORAGE / "mise"
GLOBAL_CACHE = GLOBAL_STORAGE / "cache"
CONTAINER_DIR = GLOBAL_STORAGE / "containers"
AGENTS_DIR = GLOBAL_STORAGE / "agents"
BUILD_DIR = GLOBAL_STORAGE / "build"
USER_CONFIG_PATH = Path.home() / ".config" / "yolo-jail" / "config.jsonc"

# Directory inside the jail where all host service sockets appear.
# All bind mounts land under this path.
JAIL_HOST_SERVICES_DIR = "/run/yolo-services"

# Name of the builtin cgroup delegate service.  Reserved — user-configured
# services in `loopholes` cannot use this name.
BUILTIN_CGROUP_LOOPHOLE_NAME = "cgroup-delegate"

# Name of the builtin journal bridge service.  Off by default; opt in with
# top-level config key `journal: "user"` or `"full"`.  Reserved — user
# `loopholes` cannot shadow it.
BUILTIN_JOURNAL_LOOPHOLE_NAME = "journal"
JOURNAL_SOCKET_NAME = "journal.sock"

# Legacy name used by the cgroup daemon when it was hard-coded at
# /tmp/yolo-cgd/cgroup.sock.  Kept as a constant for the refactor so the
# existing handler can be reused without changes.
CGD_SOCKET_NAME = "cgroup.sock"
