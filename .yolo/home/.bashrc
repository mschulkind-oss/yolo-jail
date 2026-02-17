# YOLO Jail Prompt
YELLOW='\[\033[1;33m\]'
RED='\[\033[1;31m\]'
GREEN='\[\033[1;32m\]'
BLUE='\[\033[1;34m\]'
MAGENTA='\[\033[1;35m\]'
CYAN='\[\033[1;36m\]'
NC='\[\033[0m\]' # No Color

# Big colorful warning
JAIL_BANNER="${RED}🔒 YOLO-JAIL${NC}"
HOST_INFO="${CYAN}(host: ${YOLO_HOST_DIR:-unknown})${NC}"

export PS1="\n${JAIL_BANNER} ${HOST_INFO}\n${GREEN}jail${NC}:${BLUE}\w${NC}\$ "

# Set PROMPT_COMMAND to update tmux window title on every prompt
# This overrides tmux's automatic-rename which shows process names
_JAIL_DIR="$(basename "${YOLO_HOST_DIR:-/workspace}")"
export PROMPT_COMMAND='printf "\033]0;JAIL '"$_JAIL_DIR"'\033\\"'

# Initialize font cache for Chromium
fc-cache -f >/dev/null 2>&1

# Agent-friendly defaults (no pagers, no line numbers)
export PAGER=cat
export BAT_PAGER=""
export BAT_STYLE="plain"
export GIT_PAGER=cat
export EDITOR=nvim

# Setup PATH with npm-global and go binaries (from docker env variables)
export NPM_CONFIG_PREFIX="${NPM_CONFIG_PREFIX:-$HOME/.npm-global}"
export GOPATH="${GOPATH:-$HOME/go}"
SHIM_DIR="${HOME}/.yolo-shims"
export PATH="$SHIM_DIR:$NPM_CONFIG_PREFIX/bin:$GOPATH/bin:/mise/shims:/bin:/usr/bin"

# Aliases
alias ls='ls --color=auto'
alias ll='ls -alF'
alias gemini='gemini --yolo'
alias copilot='copilot --yolo'
alias vi='nvim'
alias vim='nvim'
alias bat='bat --style=plain --paging=never'
