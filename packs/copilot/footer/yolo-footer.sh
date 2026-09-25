# yolo's segment for copilot's footer: the `custom` footer item runs this file
# (docs/design/agent-footer.md §2, §3).
#
# copilot's config names only this file's PATH, and yolo fills that value into
# ~/.copilot/config.json once, after which it is yours and no later release can
# change it. So the path stays fixed and this file carries everything that may
# change: the copilot pack ships it and yolo rewrites it on every boot. It lives
# in ~/.copilot/yolo/, a directory only yolo uses, never beside copilot's own
# state.
#
# It is run as `sh <this file>`, because nothing an embedded pack ships can carry
# an exec bit (packs/embed.go), and only when the file is readable, because a
# backend that delivers no `files` (macos-user) still renders copilot's config.
# With no yolo on PATH it prints nothing and exits 0, and copilot's footer item
# stays empty.
#
# --bridged names the providers copilot reaches through the wire bridge rather
# than an endpoint of their own; internal/footer's TestBridgedRoutesAreMarked
# computes that set from the shipped packs and fails when this list drifts.
command -v yolo >/dev/null 2>&1 || exit 0
exec yolo internal footer --agent copilot --login 'Copilot subscription' \
  --bridged openai-codex --bridged cerebras --bridged kilo \
  --template 'yolo: {yolo.billing} · {yolo.notch}'
