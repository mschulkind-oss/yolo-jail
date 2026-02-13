#!/bin/bash
# YOLO Jail Entrypoint Script

# 1. Create a writable directory for dynamic shims
SHIM_DIR="/tmp/yolo-shims"
mkdir -p "$SHIM_DIR"

# 2. Default blocked tools
DEFAULT_BLOCKED="grep find"
BLOCKED_TOOLS="$DEFAULT_BLOCKED"

# 3. Read blocked tools from environment variable (injected by Python CLI)
if [ -n "$YOLO_BLOCK_CONFIG" ]; then
    # Use python to parse the JSON and output bash-friendly arrays
    # Output format: name|message|suggestion
    python3 -c "
import json, os, sys
try:
    config = json.loads(os.environ['YOLO_BLOCK_CONFIG'])
    for tool in config:
        name = tool.get('name')
        if not name: continue
        msg = tool.get('message', f'Error: tool {name} is blocked in this project.')
        sug = tool.get('suggestion', '')
        print(f'{name}|{msg}|{sug}')
except Exception:
    pass
" | while IFS='|' read -r tool message suggestion; do
        SHIM_PATH="$SHIM_DIR/$tool"
        
        if [ "$tool" == "grep" ] || [ "$tool" == "find" ]; then
             cat <<EOF > "$SHIM_PATH"
#!/bin/sh
if [ -t 1 ] && [ -z "\$YOLO_BYPASS_SHIMS" ]; then
  echo "$message" >&2
  [ -n "$suggestion" ] && echo "Suggestion: $suggestion" >&2
  exit 127
fi
exec /bin/$tool "\$@"
EOF
        else
             cat <<EOF > "$SHIM_PATH"
#!/bin/sh
echo "$message" >&2
[ -n "$suggestion" ] && echo "Suggestion: $suggestion" >&2
exit 127
EOF
        fi
        chmod +x "$SHIM_PATH"
    done
fi

# 5. Place shims first in PATH
export PATH="$SHIM_DIR:$PATH"

# 6. Run the startup command passed from Justfile
exec bash -c "$@"
