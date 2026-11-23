package entrypoint

// programPhasePerfShellFn appends coarse shell observations to the existing boot
// perf block, like appendProvisionPerf. It never creates a sink: no boot log (host
// launchers included) means no recording. Boot owns block retention. Each row is
// self-contained because a detached refresh may finish after a later boot block.
// PID and foreground/detached mode identify the act, not a claimed launch generation.
//
// Each phase is also a span of the launch's trace (traceShellFn), written whatever the perf
// log's state: the two sinks are independent.
const programPhasePerfShellFn = traceShellFn + `
_yolo_phase_perf() {
    local _phase="$1" _bin="$2" _kind="$3" _mode="$4" _elapsed="$5" _rc="$6"
    _yolo_trace "$_kind" "program.$_phase.$_bin"
    [ -n "${HOME:-}" ] && [ -f "$HOME/.yolo-perf.log" ] || return 0
    {
        printf '  program phase: %.32s %.128s %.8s (pid=%s mode=%.16s; coarse elapsed seconds=%s; exit=%s)\n' \
            "$_phase" "$_bin" "$_kind" "$$" "$_mode" "$_elapsed" "$_rc"
    } 2>/dev/null >> "$HOME/.yolo-perf.log" || true
}
`

// traceShellFn is the generated shell's jail-half trace writer (docs/design/startup-tracing.md
// §5.3): `_yolo_trace KIND NAME` appends one line of internal/jailtrace's form to the jail home's
// file named for the trace in YOLO_TRACEPARENT, the variable the design lets the generated shell
// read (TR-D3). It mints no id (TR-D5).
//
// IT NEVER CREATES THE FILE: a row is appended only when the file exists, and the entrypoint's
// Go writer is what creates it when the launch handed the jail a context. So a shell with no
// context, a malformed one, or a context whose boot never opened the file writes nothing, and a
// link at the path is not written through.
//
// The time is microseconds from EPOCHREALTIME (bash 5) with no fork; where the shell lacks it
// (macOS's bash 3.2) it falls back to whole seconds, from printf's %(%s)T (bash 4.2, no fork)
// where the shell has it and date(1) otherwise, and says so with "res":"s". EPOCHREALTIME's decimal point follows
// the locale, so every non-digit is dropped rather than a "." alone. A name is reduced to
// [A-Za-z0-9_.-] and 64 bytes, as the Go writer does, so it needs no JSON escaping. Every
// failure is silent and returns 0: tracing never changes what a script does.
const traceShellFn = `
_yolo_trace() {
    local _k="$1" _n="$2" _f _t _res=""
    case "$_k" in start|end|mark) ;; *) return 0 ;; esac
    [ -n "${HOME:-}" ] && [ -n "${YOLO_TRACEPARENT:-}" ] || return 0
    local _yolo_trace_pat='^00-[0-9a-f]{32}-[0-9a-f]{16}-[0-9a-f]{2}$'
    [[ "$YOLO_TRACEPARENT" =~ $_yolo_trace_pat ]] || return 0
    _f="$HOME/.yolo-trace-${YOLO_TRACEPARENT:3:32}.jsonl"
    [ -f "$_f" ] && [ ! -L "$_f" ] || return 0
    if [ -n "${EPOCHREALTIME:-}" ]; then
        _t="${EPOCHREALTIME//[!0-9]/}000"
    else
        _res=',"res":"s"'
        printf -v _t '%(%s)T' -1 2>/dev/null || _t=""
        case "$_t" in ''|*[!0-9]*) _t=$(date +%s 2>/dev/null) || _t="" ;; esac
        case "$_t" in ''|*[!0-9]*) return 0 ;; esac
        _t="${_t}000000000"
    fi
    _n="${_n//[!A-Za-z0-9_.-]/_}"
    _n="${_n:0:64}"
    {
        printf '{"t":%s,"k":"%s","name":"%s","pid":%s%s}\n' "$_t" "$_k" "$_n" "$$" "$_res"
    } 2>/dev/null >> "$_f" || true
    return 0
}
`
