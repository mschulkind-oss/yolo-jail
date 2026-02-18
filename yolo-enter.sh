#!/bin/bash
# YOLO Jail Global Entrypoint (thin wrapper — all logic is in src/cli.py)
#
# This script resolves the repo root then delegates to the Python CLI.
# Users may symlink ~/.local/bin/yolo → this file.
# The Python CLI handles tmux decoration, arg routing, and everything else.

SOURCE=${BASH_SOURCE[0]}
while [ -L "$SOURCE" ]; do
  DIR=$( cd -P "$( dirname "$SOURCE" )" >/dev/null 2>&1 && pwd )
  SOURCE=$(readlink "$SOURCE")
  [[ $SOURCE != /* ]] && SOURCE=$DIR/$SOURCE
done
REPO_ROOT=$( cd -P "$( dirname "$SOURCE" )" >/dev/null 2>&1 && pwd )

# Route arguments: bare `yolo` → `run`, `yolo -- cmd` → `run -- cmd`
if [ -z "$1" ]; then
    exec uv run --project "$REPO_ROOT" python "$REPO_ROOT/src/cli.py" run
elif [ "$1" == "--" ]; then
    shift
    exec uv run --project "$REPO_ROOT" python "$REPO_ROOT/src/cli.py" run -- "$@"
elif [ "$1" == "--new" ]; then
    shift
    if [ "$1" == "--" ]; then shift; fi
    exec uv run --project "$REPO_ROOT" python "$REPO_ROOT/src/cli.py" run --new -- "$@"
else
    exec uv run --project "$REPO_ROOT" python "$REPO_ROOT/src/cli.py" "$@"
fi
