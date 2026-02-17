---
name: debug-devtools
description: Troubleshooting for Chrome DevTools MCP, browser automation wedges, and agent log analysis. Use when tool calls timeout or the browser becomes unresponsive.
---

# Debug DevTools Skill

This skill provides guidance for diagnosing and fixing issues with the `chrome-devtools` MCP and browser automation environments.

## When to Use This Skill
- You see `Request timed out` (-32001) errors from `chrome-devtools`.
- The browser seems "stuck" and won't take screenshots or click elements.
- You suspect a `window.confirm()` or `window.alert()` dialog is blocking the protocol.
- You need to inspect agent-level logs to find the root cause of an MCP failure.

## Core Troubleshooting
See [references/mcp-troubleshooting.md](references/mcp-troubleshooting.md) for:
1. **Dialog Wedge Recovery**: How to unstick a blocked CDP protocol.
2. **Common Error Fixes**: Solutions for "Target closed" and path resolution issues.
3. **Log Analysis**: Commands for tailing and grepping agent logs.

## Key Commands
- `devtools-handle_dialog accept`: The first thing to try when timed out.
- `yolo ps` + `docker stop`: To reset a wedged environment.
