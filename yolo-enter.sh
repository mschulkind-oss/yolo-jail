#!/bin/bash
# YOLO Jail — Global Entry Point
#
# Symlink this to your PATH (e.g., /usr/local/bin/yolo) to use
# `yolo` from any project directory. Resolves the repo root via
# symlink then delegates to the Python CLI.

SOURCE=${BASH_SOURCE[0]}
while [ -L "$SOURCE" ]; do
  DIR=$( cd -P "$( dirname "$SOURCE" )" >/dev/null 2>&1 && pwd )
  SOURCE=$(readlink "$SOURCE")
  [[ $SOURCE != /* ]] && SOURCE=$DIR/$SOURCE
done
REPO_ROOT=$( cd -P "$( dirname "$SOURCE" )" >/dev/null 2>&1 && pwd )

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
