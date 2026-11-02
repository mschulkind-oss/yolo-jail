#!/bin/sh
# Prints OWNER/REPO for a GitHub remote URL (the argument, or origin's URL), and nothing for any
# other URL. `just release` names the repository with it, since a `gh` that resolves no repository
# itself (yolo's github broker, for one) refuses a call that names none.
# The configured URL, not `git remote get-url`, which applies url.*.insteadOf rewrites.
url=${1:-$(git config --get remote.origin.url 2>/dev/null)}
printf '%s\n' "${url#yolo::}" | sed -n -E 's#^(https://|ssh://git@|git@)github\.com[:/]([^/]+)/([^/]+)$#\2/\3#p' | sed -E 's#\.git$##; s#/$##'
