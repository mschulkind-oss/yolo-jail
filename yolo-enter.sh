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

# If inside tmux, set visual jail indicators and restore on exit
if [ -n "$TMUX" ]; then
    _JAIL_DIR="$(basename "$PWD")"
    _OLD_BORDER=$(tmux show-option -pqv pane-border-style 2>/dev/null)
    _OLD_ACTIVE=$(tmux show-option -pqv pane-active-border-style 2>/dev/null)
    _OLD_BSTATUS=$(tmux show-option -pqv pane-border-status 2>/dev/null)
    _OLD_BFORMAT=$(tmux show-option -pqv pane-border-format 2>/dev/null)
    _OLD_WINNAME=$(tmux display-message -p '#W' 2>/dev/null)
    tmux rename-window "JAIL $_JAIL_DIR" >/dev/null 2>&1
    tmux set-option -p pane-border-style "fg=red,bold" >/dev/null 2>&1
    tmux set-option -p pane-active-border-style "fg=red,bold" >/dev/null 2>&1
    tmux set-option -p pane-border-status bottom >/dev/null 2>&1
    tmux set-option -p pane-border-format " 🔒 JAIL $_JAIL_DIR " >/dev/null 2>&1
    _restore_border() {
        if [ -n "$_OLD_BORDER" ]; then
            tmux set-option -p pane-border-style "$_OLD_BORDER" >/dev/null 2>&1
        else
            tmux set-option -pu pane-border-style >/dev/null 2>&1
        fi
        if [ -n "$_OLD_ACTIVE" ]; then
            tmux set-option -p pane-active-border-style "$_OLD_ACTIVE" >/dev/null 2>&1
        else
            tmux set-option -pu pane-active-border-style >/dev/null 2>&1
        fi
        if [ -n "$_OLD_BSTATUS" ]; then
            tmux set-option -p pane-border-status "$_OLD_BSTATUS" >/dev/null 2>&1
        else
            tmux set-option -pu pane-border-status >/dev/null 2>&1
        fi
        if [ -n "$_OLD_BFORMAT" ]; then
            tmux set-option -p pane-border-format "$_OLD_BFORMAT" >/dev/null 2>&1
        else
            tmux set-option -pu pane-border-format >/dev/null 2>&1
        fi
        tmux rename-window "$_OLD_WINNAME" >/dev/null 2>&1
    }
    trap _restore_border EXIT
fi

# Run the CLI using uv, pointing to the jail project while staying in the user's current directory
_run_jail() { uv run --project "$REPO_ROOT" "$REPO_ROOT/src/cli.py" "$@"; }

if [ -z "$1" ]; then
    # No arguments: start an interactive shell
    _run_jail run
elif [ "$1" == "init" ] || [ "$1" == "init-user-config" ] || [ "$1" == "run" ] || [ "$1" == "--help" ] || [ "$1" == "-h" ]; then
    # Explicit subcommands or help
    _run_jail "$@"
elif [ "$1" == "--" ]; then
    # Treat everything after -- as the command to run in the jail
    shift
    _run_jail run -- "$@"
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
