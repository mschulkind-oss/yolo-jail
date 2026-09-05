// Package wirebridge is the translation half of the wire bridge
// (docs/design/wire-bridge.md §4): it converts Anthropic Messages wire
// requests into OpenAI chat-completions requests and translates the answers
// back, so claude can ride a chat-completions-only provider such as cerebras.
//
// It is I/O-FREE BY DESIGN — no sockets, no files, no clocks, no net/http.
// JSON bytes (or one stream chunk) in; JSON bytes (or wire events) out.
// Everything transport-shaped belongs to the daemon that wraps it (the
// `yolo-jaild wire-bridge` subcommand, a separate concern built separately):
// listening on the jail's loopback, dialing the upstream, SSE line framing,
// status codes, retries, and the outbound Authorization header (WB-D4 — this
// package sees no keys and ignores whatever inbound token rides the body's
// transport).
//
// Scope is exactly §4's table — what Claude Code actually sends, not the
// whole Anthropic API — with the design's failure posture:
//
//   - count_tokens is NOT translated and the package exposes nothing for it:
//     the caller answers 404 (WB-D14 — a refusal sends claude to its own
//     estimator; a zero-stub would poison the count it was asked for).
//   - Unknown anthropic content-block or tool types FAIL CLOSED:
//     TranslateRequest returns an error naming the type, which the daemon
//     renders as a 400 (WB-D5) — never a silently-mistranslated request.
//   - Upstream reasoning content is dropped, never surfaced as thinking
//     blocks (WB-D5); tools never carry strict (WB-D6); no thinking config
//     and no reasoning_effort is ever emitted upstream (WB-D15).
//   - Unmapped top-level request keys (thinking, top_k, metadata, ...) are
//     dropped by construction, never forwarded — the same disposition the
//     table gives top_k: mapped fields pass, unmapped fields vanish.
//
// Streaming choice: the daemon owns SSE FRAMING — it strips the "data: "
// prefix, recognizes the "data: [DONE]" sentinel, and writes whatever bytes
// the library emits. The library owns the TRANSLATION of one upstream
// chat-completions chunk (the JSON payload of one data: line) into zero or
// more anthropic SSE events, in order, through the stateful StreamTranslator:
// message_start on the first chunk, content_block_start / content_block_delta
// / content_block_stop bookkeeping across chunks, then message_delta +
// message_stop with the first chunk that carries a finish_reason. Events come
// back as Event{Name, Data}; Event.Format renders the full "event:"/"data:"
// wire pair so the daemon can write it verbatim.
package wirebridge
