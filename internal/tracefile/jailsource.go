package tracefile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	// JailSourceMaxBytes bounds the lexical window examined by one source read.
	JailSourceMaxBytes = 1 << 20
	// JailSourceMaxRows bounds complete physical rows considered by one source read.
	JailSourceMaxRows = 2000
	// JailSourceMaxOccurrences is the lifetime admission ceiling for one trace.
	JailSourceMaxOccurrences = 2000
)

// JailSourceInstance is a caller-minted source key used only by the caller-owned lifetime
// budget. The parser neither creates this key nor returns it as observation data.
type JailSourceInstance [16]byte

// JailSourceInput is one already-read byte range. AbsoluteOffset is the file offset of Bytes[0].
// FirstRowComplete is evidence from the source reader that Bytes[0] starts at a physical-row
// boundary; the parser clears it when it must discard bytes to enforce its tail bound. A stable,
// host-minted SourceInstance lets the budget recognize rereads without inventing source identity.
type JailSourceInput struct {
	Bytes            []byte             `json:"-"`
	AbsoluteOffset   int64              `json:"-"`
	FirstRowComplete bool               `json:"-"`
	SourceInstance   JailSourceInstance `json:"-"`
}

func (JailSourceInput) String() string   { return "[jail source input]" }
func (JailSourceInput) GoString() string { return "[jail source input]" }

// JailSourceObservation is one valid complete physical producer row. Name is display-only and
// sanitized. The exact eligible producer spelling is private matching evidence, deliberately
// unexported so encoding/json cannot export it; consumers compare it with JailSourceNamesEqual.
type JailSourceObservation struct {
	Offset        int64  `json:"offset"`
	Time          int64  `json:"time"`
	Kind          string `json:"kind"`
	Name          string `json:"name"`
	PID           int    `json:"pid"`
	CoarseSeconds bool   `json:"coarse_seconds,omitempty"`

	matchingName jailSourceMatchingName
}

// String and GoString keep ordinary debug formatting on the display-safe projection too.
func (o JailSourceObservation) String() string {
	return fmt.Sprintf("JailSourceObservation{Offset:%d Time:%d Kind:%q Name:%q PID:%d CoarseSeconds:%t}",
		o.Offset, o.Time, o.Kind, o.Name, o.PID, o.CoarseSeconds)
}

func (o JailSourceObservation) GoString() string { return o.String() }

type jailSourceMatchingName struct {
	spelling string
	eligible bool
}

func (jailSourceMatchingName) String() string   { return "[private jail-source matching name]" }
func (jailSourceMatchingName) GoString() string { return "[private jail-source matching name]" }

// JailSourceResult reports only bounded observations and aggregate parse limits. It never
// retains a source row, unknown field, source path, or rejected payload.
type JailSourceResult struct {
	Observations    []JailSourceObservation
	MalformedRows   int
	TruncatedBytes  bool
	TruncatedRows   bool
	BudgetExhausted bool
	InvalidInput    bool
}

// JailSourceBudget is caller-owned and scoped to one trace. Callers serialize access and retain
// the budget for the trace lifetime. Its bounded occurrence keys make a reread of the same
// (source instance, absolute offset) free, while distinct source instances remain distinct.
// This in-memory parser budget is not a persistent store or a substitute for U7b admission.
type JailSourceBudget struct {
	limit    int
	admitted map[jailSourceOccurrence]struct{}
}

type jailSourceOccurrence struct {
	instance JailSourceInstance
	offset   int64
}

// NewJailSourceBudget creates an empty lifetime budget at the design's 2,000-occurrence limit.
func NewJailSourceBudget() *JailSourceBudget {
	return NewJailSourceBudgetWithLimit(JailSourceMaxOccurrences)
}

// NewJailSourceBudgetWithLimit creates a smaller explicit budget for a caller or a focused test.
// Values above the settled per-trace ceiling are clamped; negative values admit no occurrences.
func NewJailSourceBudgetWithLimit(limit int) *JailSourceBudget {
	if limit < 0 {
		limit = 0
	}
	if limit > JailSourceMaxOccurrences {
		limit = JailSourceMaxOccurrences
	}
	return &JailSourceBudget{limit: limit, admitted: make(map[jailSourceOccurrence]struct{})}
}

// Remaining is the number of new source occurrences this budget can admit.
func (b *JailSourceBudget) Remaining() int {
	if b == nil {
		return 0
	}
	return b.limit - len(b.admitted)
}

// Admitted is the number of distinct source occurrences admitted so far.
func (b *JailSourceBudget) Admitted() int {
	if b == nil {
		return 0
	}
	return len(b.admitted)
}

// String exposes only bounded admission counts. It deliberately never formats the map or keys.
func (b JailSourceBudget) String() string {
	admitted := len(b.admitted)
	return fmt.Sprintf("JailSourceBudget{limit:%d admitted:%d remaining:%d}", b.limit, admitted, b.limit-admitted)
}

// GoString keeps %#v formatting on the same key-free projection.
func (b JailSourceBudget) GoString() string { return b.String() }

// JailSourceNamesEqual compares only exact, bounded producer spellings. Display normalization,
// truncation and ineligible spellings can never establish a match; even an empty eligible name
// compares only with another empty eligible name.
func JailSourceNamesEqual(a, b JailSourceObservation) bool {
	return a.matchingName.eligible && b.matchingName.eligible && a.matchingName.spelling == b.matchingName.spelling
}

// PrivateMatchingName returns the exact eligible producer spelling for host-private ingestion
// bookkeeping only. Callers must never place it in diagnostics, metadata, attributes, or exports.
func (o JailSourceObservation) PrivateMatchingName() (string, bool) {
	return o.matchingName.spelling, o.matchingName.eligible
}

// ParseJailSource lexically reads id-less Go/shell jail producer rows. It performs no filesystem
// access, identity generation, pairing, or persistence. The caller supplies stable source identity
// and one trace-lifetime budget, and remains responsible for continuity checks and persisted
// bookkeeping. A malformed complete row consumes the per-read physical-row bound but not a
// lifetime occurrence slot; only observations accepted by this parser are admitted occurrences.
func ParseJailSource(input JailSourceInput, budget *JailSourceBudget) JailSourceResult {
	var result JailSourceResult
	if budget == nil || input.AbsoluteOffset < 0 || int64(len(input.Bytes)) > math.MaxInt64-input.AbsoluteOffset {
		result.InvalidInput = true
		return result
	}

	window := input.Bytes
	windowOffset := input.AbsoluteOffset
	firstRowComplete := input.FirstRowComplete
	if len(window) > JailSourceMaxBytes {
		cut := len(window) - JailSourceMaxBytes
		firstRowComplete = cut > 0 && input.Bytes[cut-1] == '\n'
		window = window[cut:]
		windowOffset += int64(cut)
		result.TruncatedBytes = true
	}

	rows := make([]jailSourceRow, 0, JailSourceMaxRows)
	position := 0
	for position < len(window) {
		relativeEnd := bytes.IndexByte(window[position:], '\n')
		if relativeEnd < 0 {
			break // an incomplete final physical row is not an occurrence
		}
		end := position + relativeEnd
		if position == 0 && !firstRowComplete {
			position = end + 1
			continue // bytes before the first newline are an incomplete first physical row
		}
		row := jailSourceRow{bytes: window[position:end], offset: windowOffset + int64(position)}
		if len(rows) == JailSourceMaxRows {
			copy(rows, rows[1:])
			rows[len(rows)-1] = row
			result.TruncatedRows = true
		} else {
			rows = append(rows, row)
		}
		position = end + 1
	}
	for _, row := range rows {
		observation, ok := parseJailSourceRow(row.bytes)
		if !ok {
			result.MalformedRows++
			continue
		}
		observation.Offset = row.offset
		key := jailSourceOccurrence{instance: input.SourceInstance, offset: row.offset}
		if _, seen := budget.admitted[key]; !seen {
			if len(budget.admitted) >= budget.limit {
				result.BudgetExhausted = true
				continue
			}
			budget.admitted[key] = struct{}{}
		}
		result.Observations = append(result.Observations, observation)
	}
	return result
}

type jailSourceRow struct {
	bytes  []byte
	offset int64
}

func parseJailSourceRow(line []byte) (JailSourceObservation, bool) {
	var observation JailSourceObservation
	if !utf8.Valid(line) {
		return observation, false
	}
	decoder := json.NewDecoder(bytes.NewReader(line))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return observation, false
	}
	values := make(map[string]json.RawMessage, 5)
	seenKeys := make(map[string]struct{}, 5)
	for decoder.More() {
		token, err = decoder.Token()
		if err != nil {
			return observation, false
		}
		key, ok := token.(string)
		if !ok {
			return observation, false
		}
		if _, duplicate := seenKeys[key]; duplicate {
			return observation, false
		}
		seenKeys[key] = struct{}{}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return observation, false
		}
		switch key {
		case "t", "k", "name", "pid", "res":
			values[key] = raw
		}
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return observation, false
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		return observation, false
	}

	timeValue, ok := parseJailJSONInteger(values["t"], 64)
	if !ok {
		return observation, false
	}
	pidValue, ok := parseJailJSONInteger(values["pid"], strconv.IntSize)
	if !ok {
		return observation, false
	}
	var kind, name string
	if !decodeJailJSONString(values["k"], &kind) || !decodeJailJSONString(values["name"], &name) {
		return observation, false
	}
	if kind != "start" && kind != "end" && kind != "mark" {
		return observation, false
	}
	coarseSeconds := false
	if rawResolution, exists := values["res"]; exists {
		var resolution string
		if !decodeJailJSONString(rawResolution, &resolution) || resolution != "s" {
			return observation, false
		}
		// Producers already write t as nanoseconds. res is resolution evidence only.
		coarseSeconds = true
	}

	observation.Time = timeValue
	observation.Kind = kind
	observation.Name, observation.matchingName = sanitizeJailSourceName(name)
	observation.PID = int(pidValue)
	observation.CoarseSeconds = coarseSeconds
	return observation, true
}

func decodeJailJSONString(raw json.RawMessage, destination *string) bool {
	if len(raw) == 0 || raw[0] != '"' {
		return false
	}
	return json.Unmarshal(raw, destination) == nil
}

// parseJailJSONInteger accepts any JSON number whose exact decimal value is integral and fits
// bitSize. It validates the JSON number grammar and never passes through float64.
func parseJailJSONInteger(raw json.RawMessage, bitSize int) (int64, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || !json.Valid(raw) {
		return 0, false
	}
	text := string(raw)
	negative := false
	if text[0] == '-' {
		negative = true
		text = text[1:]
		if text == "" {
			return 0, false
		}
	}

	exponentText := ""
	if index := strings.IndexAny(text, "eE"); index >= 0 {
		exponentText = text[index+1:]
		text = text[:index]
		if !validJSONExponent(exponentText) {
			return 0, false
		}
	}
	integerPart, fractionPart, hasFraction := strings.Cut(text, ".")
	if integerPart == "" || (hasFraction && fractionPart == "") {
		return 0, false
	}
	if integerPart != "0" {
		if integerPart[0] < '1' || integerPart[0] > '9' {
			return 0, false
		}
		for i := 1; i < len(integerPart); i++ {
			if integerPart[i] < '0' || integerPart[i] > '9' {
				return 0, false
			}
		}
	}
	for i := range fractionPart {
		if fractionPart[i] < '0' || fractionPart[i] > '9' {
			return 0, false
		}
	}
	digits := integerPart + fractionPart
	allZero := true
	for i := range digits {
		if digits[i] != '0' {
			allZero = false
			break
		}
	}
	// Once the mantissa and exponent syntax are validated, zero is integral for every
	// exponent. Do not convert a huge exponent or allocate a power of ten for it.
	if allZero {
		return 0, true
	}

	exponent := 0
	if exponentText != "" {
		var ok bool
		exponent, ok = parseBoundedExponent(exponentText, len(raw)+20)
		if !ok {
			return 0, false
		}
	}
	fractionDigits := 0
	if hasFraction {
		fractionDigits = len(fractionPart)
	}
	digits = strings.TrimLeft(digits, "0")
	scale := exponent - fractionDigits
	if scale < 0 {
		remove := -scale
		trailingZeros := 0
		for i := len(digits) - 1; i >= 0 && digits[i] == '0'; i-- {
			trailingZeros++
		}
		if trailingZeros < remove {
			return 0, false
		}
		digits = digits[:len(digits)-remove]
	} else if scale > 0 {
		if scale > 19 || len(digits)+scale > 19 {
			return 0, false
		}
		digits += strings.Repeat("0", scale)
	}
	if bitSize == 32 && len(digits) > 10 || bitSize == 64 && len(digits) > 19 {
		return 0, false
	}
	if negative {
		digits = "-" + digits
	}
	value, err := strconv.ParseInt(digits, 10, bitSize)
	return value, err == nil
}

func validJSONExponent(text string) bool {
	if text == "" {
		return false
	}
	if text[0] == '+' || text[0] == '-' {
		text = text[1:]
		if text == "" {
			return false
		}
	}
	for i := range text {
		if text[i] < '0' || text[i] > '9' {
			return false
		}
	}
	return true
}

func parseBoundedExponent(text string, bound int) (int, bool) {
	if text == "" {
		return 0, false
	}
	negative := false
	if text[0] == '+' || text[0] == '-' {
		negative = text[0] == '-'
		text = text[1:]
		if text == "" {
			return 0, false
		}
	}
	value := 0
	for i := range text {
		c := text[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		digit := int(c - '0')
		if value > (bound-digit)/10 {
			return 0, false
		}
		value = value*10 + digit
	}
	if negative {
		return -value, true
	}
	return value, true
}

func sanitizeJailSourceName(name string) (display string, matching jailSourceMatchingName) {
	eligible := len(name) <= 64
	for i := 0; i < len(name); i++ {
		c := name[i]
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '.' || c == '-') {
			eligible = false
		}
	}
	limit := len(name)
	if limit > 64 {
		limit = 64
	}
	var builder strings.Builder
	builder.Grow(limit)
	for i := 0; i < limit; i++ {
		c := name[i]
		switch {
		case c >= 'A' && c <= 'Z':
			builder.WriteByte(c + ('a' - 'A'))
		case c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_':
			builder.WriteByte(c)
		case c == '.':
			builder.WriteByte(c)
		default:
			builder.WriteByte('_')
		}
	}
	if eligible {
		matching = jailSourceMatchingName{spelling: name, eligible: true}
	}
	return builder.String(), matching
}
