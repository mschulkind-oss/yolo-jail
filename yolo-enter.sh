#!/bin/bash
# YOLO Jail Global Entrypoint
# This script launches the Python CLI using uv

# Resolve the directory of the real script (handling symlinks)
SOURCE=${BASH_SOURCE[0]}
while [ -L "$SOURCE" ]; do 
  DIR=$( cd -P "$( dirname "$SOURCE" )" >/dev/null 2>&1 && pwd )
  SOURCE=$(readlink "$SOURCE")
  [[ $SOURCE != /* ]] && SOURCE=$DIR/$SOURCE 
done
REPO_ROOT=$( cd -P "$( dirname "$SOURCE" )" >/dev/null 2>&1 && pwd )

# Run the CLI using uv, pointing to the jail project while staying in the user's current directory
if [ -z "$1" ]; then
    # No arguments: start an interactive shell
    exec uv run --project "$REPO_ROOT" "$REPO_ROOT/src/cli.py" run
elif [ "$1" == "init" ] || [ "$1" == "init-user-config" ] || [ "$1" == "run" ] || [ "$1" == "--help" ] || [ "$1" == "-h" ]; then
    # Explicit subcommands or help
    exec uv run --project "$REPO_ROOT" "$REPO_ROOT/src/cli.py" "$@"
elif [ "$1" == "--" ]; then
    # Treat everything after -- as the command to run in the jail
    shift
    exec uv run --project "$REPO_ROOT" "$REPO_ROOT/src/cli.py" run -- "$@"
else
    # Rejection of the "old way" (implicit command execution)
    echo "Error: Unknown argument '$1'. " >&2
    echo "Usage:" >&2
    echo "  yolo              # Open interactive shell" >&2
    echo "  yolo init         # Initialize configuration" >&2
    echo "  yolo init-user-config # Initialize user-level defaults" >&2
    echo "  yolo -- <command> # Run command directly" >&2
    exit 1
fi
