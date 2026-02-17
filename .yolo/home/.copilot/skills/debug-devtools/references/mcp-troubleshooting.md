# Chrome DevTools & MCP Troubleshooting

## Dialog Timeout Wedge
A common failure mode where a `window.confirm/alert/prompt` blocks the entire Chrome DevTools Protocol.

### Symptoms
- Consecutive `MCP error -32001: Request timed out`.
- Browser becomes unresponsive to screenshots, clicks, or script evaluation.

### Recovery Flow
1. **Try `handle_dialog accept` or `handle_dialog dismiss`**. This is the most common fix.
2. **Try `navigate_page`** to a fresh URL to break the dialog state.
3. **Kill and Restart**: If stuck, stop the container and restart the agent session.

## Common Protocol Errors

### "Target.setDiscoverTargets: Target closed"
- **Cause**: Persistent profile corruption.
- **Fix**: Use the `--isolated` flag in MCP args for a fresh temp profile.

### "Runtime.callFunctionOn timed out"
- **Cause**: Missing fontconfig or environment variables.
- **Fix**: Ensure `FONTCONFIG_PATH` and `/etc/fonts` are present.

### "libstdc++.so.6" or "Cannot find module"
- **Cause**: Environment sanitization or path resolution failures.
- **Fix**: Use absolute paths for wrappers (e.g., `/home/agent/.local/bin/mcp-wrappers/node`).

## Debugging the Agent Logs
- **Copilot**: `tail -f ~/.copilot/logs/$(ls -1t ~/.copilot/logs | head -1)`
- **Gemini**: `tail -f ~/.cache/gemini-cli/logs/$(ls -1t ~/.cache/gemini-cli/logs | head -1)`
- **Grep**: Search for `MCP.*error`, `timed out`, or `target closed`.