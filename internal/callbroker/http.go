package callbroker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

const (
	httpClassifierInputLimit = 4_456_448
	httpBodyLimit            = 3 << 20
	httpRawAggregateLimit    = 16 << 10
	httpHeaderCountLimit     = 64
	httpHeaderNameLimit      = 256
)

// HTTPHeader carries one case-insensitive field name and its ordered values.
type HTTPHeader struct {
	Name   string   `json:"name"`
	Values []string `json:"values"`
}

// HTTPCall is an in-memory request. BodyPresent distinguishes no body from a
// present, zero-length body; it is never serialized directly to the classifier.
type HTTPCall struct {
	Method      string       `json:"method"`
	Path        string       `json:"path"`
	Query       string       `json:"query"`
	Headers     []HTTPHeader `json:"headers"`
	BodyPresent bool         `json:"body_present"`
	Body        []byte       `json:"body_base64"`
}

type httpClassifierRequest struct {
	Method      string       `json:"method"`
	Path        string       `json:"path"`
	Query       string       `json:"query"`
	Headers     []HTTPHeader `json:"headers"`
	BodyPresent bool         `json:"body_present"`
	BodyBase64  string       `json:"body_base64"`
}

type httpClassifierInput struct {
	Version string                `json:"version"`
	Front   string                `json:"front"`
	Request httpClassifierRequest `json:"request"`
	Roots   []string              `json:"roots"`
	Facts   []LookupFact          `json:"facts,omitempty"`
}

type httpBodyRewrite struct {
	Mode    string  `json:"mode"`
	Present bool    `json:"present"`
	SHA256  *string `json:"sha256,omitempty"`
	Base64  *string `json:"base64,omitempty"`
}

type httpCanonicalRequest struct {
	Method  string          `json:"method"`
	Path    string          `json:"path"`
	Query   string          `json:"query"`
	Headers []HTTPHeader    `json:"headers"`
	Body    httpBodyRewrite `json:"body"`
}

type httpClassification struct {
	Canonical httpCanonicalRequest `json:"canonical"`
	Reads     [][]Resource         `json:"reads,omitempty"`
	Writes    []Write              `json:"writes,omitempty"`
}

type httpClassifierAnswer struct {
	Version        string              `json:"version"`
	Classification *httpClassification `json:"classification,omitempty"`
	Lookups        []LookupRequest     `json:"lookups,omitempty"`
	Refused        *Refusal            `json:"refused,omitempty"`
	Local          bool                `json:"local,omitempty"`
}

// HTTPResult contains only canonical executable bytes and core-computed digests.
// It does not authorize or execute a request.
type HTTPResult struct {
	Canonical            HTTPCall
	Reads                [][]Resource
	Writes               []Write
	OriginalIntentSHA256 string
	CanonicalSHA256      string
	Refused              *Refusal
}

// ClassifyHTTP runs the selected staged classifier with a bounded one-value JSON
// input. allowedHeaders comes only from the gate-approved selected declaration.
func (c Classifier) ClassifyHTTP(ctx context.Context, original HTTPCall, allowedHeaders []string) (HTTPResult, error) {
	return c.ClassifyHTTPWithLookups(ctx, original, allowedHeaders, nil)
}

// ClassifyHTTPWithLookups lets core satisfy only selected, exact standing-read
// lookup templates. Each classifier process has its own deadline
// (classifierDeadline); the whole classification, lookups included, is bounded
// only by classificationBudget, for callers that bring no deadline of their own.
func (c Classifier) ClassifyHTTPWithLookups(ctx context.Context, original HTTPCall, allowedHeaders []string, read LookupReader) (HTTPResult, error) {
	if c.Source == "" || len(c.Argv) == 0 {
		return HTTPResult{}, errors.New("call broker classifier has no selected declaration; retry with its pack selected")
	}
	if !validHTTPRoots(c.Roots) {
		return HTTPResult{}, errors.New("selected HTTP classifier roots exceed the metadata budget; retry with the selected pack")
	}
	prepared, err := prepareHTTPCall(original, allowedHeaders)
	if err != nil {
		return HTTPResult{}, err
	}
	originalDigest := digestHTTPCall(original, nil, nil)
	input := httpClassifierInput{Version: "yolo.call-broker-classifier/1", Front: "http", Roots: append([]string(nil), c.Roots...),
		Request: httpClassifierRequest{Method: prepared.Method, Path: prepared.Path, Query: prepared.Query,
			Headers: prepared.Headers, BodyPresent: prepared.BodyPresent, BodyBase64: base64.StdEncoding.EncodeToString(prepared.Body)}}
	ctx, cancel := context.WithTimeout(ctx, classificationBudget)
	defer cancel()
	seenLookups := make(map[string]bool)
	failedLookup := false
	for round := 0; round <= maxLookupRounds; round++ {
		payload, err := json.Marshal(input)
		if err != nil {
			return HTTPResult{}, err
		}
		if len(payload) > httpClassifierInputLimit {
			return HTTPResult{}, errors.New("HTTP classifier input exceeds 4,456,448 bytes; reduce the request metadata and retry")
		}
		raw, err := c.invokePayload(ctx, payload)
		if err != nil {
			return HTTPResult{}, fmt.Errorf("call broker %s HTTP classifier failed: %w; check the selected pack and retry", c.Source, err)
		}
		if err := rejectDuplicateJSONFields(raw); err != nil {
			return HTTPResult{}, fmt.Errorf("call broker %s HTTP classifier answer is not strict JSON: %w; check the selected pack and retry", c.Source, err)
		}
		if err := validateHTTPAnswerShape(raw); err != nil {
			return HTTPResult{}, fmt.Errorf("call broker %s HTTP classifier answer is malformed: %w; check the selected pack and retry", c.Source, err)
		}
		var answer httpClassifierAnswer
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&answer); err != nil {
			return HTTPResult{}, fmt.Errorf("call broker %s HTTP classifier answer is not strict JSON: %w; check the selected pack and retry", c.Source, err)
		}
		if decoder.Decode(new(any)) != io.EOF {
			return HTTPResult{}, errors.New("HTTP classifier answer has trailing data; check the selected pack and retry")
		}
		if answer.Version != input.Version {
			return HTTPResult{}, errors.New("HTTP classifier used an unknown protocol version; check the selected pack and retry")
		}
		kinds := 0
		if answer.Classification != nil {
			kinds++
		}
		if len(answer.Lookups) != 0 {
			kinds++
		}
		if answer.Refused != nil {
			kinds++
		}
		if answer.Local {
			kinds++
		}
		if kinds != 1 {
			return HTTPResult{}, errors.New("HTTP classifier must return exactly one answer kind; check the selected pack and retry")
		}
		if len(answer.Lookups) != 0 {
			if round == maxLookupRounds {
				return HTTPResult{}, errors.New("HTTP classifier exceeded three lookup rounds; check the selected pack and retry")
			}
			if read == nil {
				return HTTPResult{}, errors.New("HTTP lookup needs current core read standing; request read access and retry")
			}
			for _, lookup := range answer.Lookups {
				var declared *loopholedecl.CallBrokerLookup
				for i := range c.Lookups {
					if c.Lookups[i].Name == lookup.Name {
						declared = &c.Lookups[i]
						break
					}
				}
				if declared == nil || declared.HTTPResource == "" || declared.HTTPMethod != "GET" || len(lookup.Arguments) != 1 || lookup.Arguments[0] == "" {
					return HTTPResult{}, errors.New("HTTP classifier requested an unknown or untyped read lookup; fix the selected pack")
				}
				key, _ := json.Marshal(lookup)
				if seenLookups[string(key)] {
					return HTTPResult{}, errors.New("HTTP classifier repeated a native lookup cycle; fix the selected pack")
				}
				seenLookups[string(key)] = true
				fact, lookupErr := read(ctx, *declared, lookup.Arguments)
				if lookupErr != nil {
					failedLookup = true
					fact = json.RawMessage("null")
				}
				if !json.Valid(fact) || len(fact) > classifierLimit {
					return HTTPResult{}, errors.New("HTTP native lookup returned malformed or oversized facts; fix the selected pack")
				}
				input.Facts = append(input.Facts, LookupFact{Name: lookup.Name, Arguments: slices.Clone(lookup.Arguments), Value: fact})
			}
			continue
		}
		if answer.Refused != nil {
			if answer.Refused.Class == "" || answer.Refused.Reason == "" || answer.Refused.NextStep == "" ||
				len(answer.Refused.Reason) > 512 || len(answer.Refused.NextStep) > 512 ||
				termsafe.HasUnsafe(answer.Refused.Reason) || termsafe.HasUnsafe(answer.Refused.NextStep) {
				return HTTPResult{}, errors.New("HTTP classifier refusal is missing, unsafe or oversized; check the selected pack and retry")
			}
			return HTTPResult{OriginalIntentSHA256: originalDigest, Refused: answer.Refused}, nil
		}
		if answer.Local || answer.Classification == nil {
			return HTTPResult{}, errors.New("HTTP classifier returned an unsupported local result; check the selected pack and retry")
		}
		if failedLookup {
			return HTTPResult{}, errors.New("a native lookup failed; classifier facts cannot authorize a guessed resource; inspect host read access")
		}
		canonical, reads, writes, err := c.validateHTTPClassification(original, allowedHeaders, *answer.Classification)
		if err != nil {
			return HTTPResult{}, fmt.Errorf("call broker %s HTTP classifier failed: %w; check the selected pack and retry", c.Source, err)
		}
		if len(writes) == 0 && !httpStandingReadMethod(original, canonical, c.ReadOverPost) {
			return HTTPResult{}, errors.New("HTTP standing read needs safe methods or both original and canonical POST on a gate-approved read path; check the selected pack and retry")
		}
		canonicalDigest := digestHTTPCall(canonical, reads, writes)
		return HTTPResult{Canonical: canonical, Reads: reads, Writes: writes,
			OriginalIntentSHA256: originalDigest, CanonicalSHA256: canonicalDigest}, nil
	}
	return HTTPResult{}, errors.New("HTTP classifier exceeded lookup rounds; fix the selected pack")
}

func (c Classifier) validateHTTPClassification(original HTTPCall, allowed []string, classification httpClassification) (HTTPCall, [][]Resource, []Write, error) {
	canonical := HTTPCall{Method: classification.Canonical.Method, Path: classification.Canonical.Path,
		Query: classification.Canonical.Query, Headers: classification.Canonical.Headers}
	if err := validateHTTPFields(canonical.Method, canonical.Path, canonical.Query); err != nil {
		return HTTPCall{}, nil, nil, err
	}
	validated, err := prepareHTTPCall(canonical, allowed)
	if err != nil || !sameHeaders(validated.Headers, canonical.Headers) {
		return HTTPCall{}, nil, nil, errors.New("canonical HTTP headers are invalid or outside the selected allowlist")
	}
	canonical = validated
	body := classification.Canonical.Body
	switch body.Mode {
	case "original":
		want := digestHTTPBody(original.BodyPresent, original.Body)
		if body.Present != original.BodyPresent || body.SHA256 == nil || *body.SHA256 != want || body.Base64 != nil {
			return HTTPCall{}, nil, nil, errors.New("canonical body reference does not endorse the original body presence and digest")
		}
		canonical.BodyPresent = original.BodyPresent
		canonical.Body = append([]byte(nil), original.Body...)
	case "inline_base64":
		if body.SHA256 != nil || body.Base64 == nil {
			return HTTPCall{}, nil, nil, errors.New("inline canonical body must not claim an original digest")
		}
		decoded, err := base64.StdEncoding.Strict().DecodeString(*body.Base64)
		if err != nil || len(decoded) > httpBodyLimit || (!body.Present && len(decoded) != 0) {
			return HTTPCall{}, nil, nil, errors.New("canonical HTTP body rewrite is malformed or oversized")
		}
		canonical.BodyPresent = body.Present
		canonical.Body = decoded
	default:
		return HTTPCall{}, nil, nil, errors.New("canonical HTTP body mode is missing or unknown")
	}
	if len(canonical.Body) > httpBodyLimit {
		return HTTPCall{}, nil, nil, errors.New("canonical HTTP body exceeds 3 MiB")
	}
	reads := cloneChains(classification.Reads)
	writes := append([]Write(nil), classification.Writes...)
	if len(reads)+len(writes) == 0 {
		return HTTPCall{}, nil, nil, errors.New("HTTP classification has no read or write resources")
	}
	for _, chain := range reads {
		if err := c.validateChain(chain); err != nil {
			return HTTPCall{}, nil, nil, err
		}
	}
	for _, write := range writes {
		found := false
		for _, cell := range c.Cells {
			if cell.Name == write.Cell {
				found = true
				break
			}
		}
		if !found || write.Cell == "read" || write.Description == "" || len(write.Description) > 512 || termsafe.HasUnsafe(write.Description) {
			return HTTPCall{}, nil, nil, errors.New("unknown HTTP write cell or invalid description")
		}
		if err := c.validateChain(write.Chain); err != nil {
			return HTTPCall{}, nil, nil, err
		}
	}
	return canonical, reads, writes, nil
}

func httpStandingReadMethod(original, canonical HTTPCall, declared []string) bool {
	if original.Method == "POST" || canonical.Method == "POST" {
		if original.Method != "POST" || canonical.Method != "POST" || original.Path != canonical.Path {
			return false
		}
		return slices.Contains(declared, original.Path)
	}
	return safeHTTPReadMethod(original.Method) && safeHTTPReadMethod(canonical.Method)
}

func safeHTTPReadMethod(method string) bool {
	return method == "GET" || method == "HEAD" || method == "OPTIONS"
}

func cloneChains(in [][]Resource) [][]Resource {
	out := make([][]Resource, len(in))
	for i := range in {
		out[i] = append([]Resource(nil), in[i]...)
	}
	return out
}

func prepareHTTPCall(call HTTPCall, approved []string) (HTTPCall, error) {
	if err := validateHTTPFields(call.Method, call.Path, call.Query); err != nil {
		return HTTPCall{}, err
	}
	if len(call.Body) > httpBodyLimit || (!call.BodyPresent && len(call.Body) != 0) {
		return HTTPCall{}, errors.New("HTTP request body is inconsistent or exceeds 3 MiB; reduce the body and retry")
	}
	if len(call.Headers) > httpHeaderCountLimit || len(approved) > httpHeaderCountLimit {
		return HTTPCall{}, errors.New("HTTP request exceeds the selected header count or value budget")
	}
	allowed := make(map[string]string, len(approved))
	for _, name := range approved {
		canonical, ok := validHTTPHeaderName(name)
		if !ok || blockedHTTPHeader(canonical) {
			return HTTPCall{}, fmt.Errorf("selected HTTP header %q is invalid or prohibited", name)
		}
		allowed[strings.ToLower(canonical)] = canonical
	}
	values := make(map[string][]string)
	headerOccurrences := 0
	for _, header := range call.Headers {
		occurrences := len(header.Values)
		if occurrences == 0 {
			occurrences = 1
		}
		headerOccurrences += occurrences
		if headerOccurrences > httpHeaderCountLimit {
			return HTTPCall{}, errors.New("HTTP request exceeds the selected header count or value budget")
		}
		canonical, ok := validHTTPHeaderName(header.Name)
		if !ok {
			return HTTPCall{}, errors.New("HTTP request contains an invalid header name")
		}
		key := strings.ToLower(canonical)
		if blockedHTTPHeader(canonical) {
			continue
		}
		if _, ok := allowed[key]; !ok {
			continue
		}
		for _, value := range header.Values {
			if !validHTTPHeaderValue(value) {
				return HTTPCall{}, fmt.Errorf("HTTP request header %q has an invalid value", header.Name)
			}
			values[key] = append(values[key], value)
		}
	}
	keys := make([]string, 0, len(values))
	headerValueBytes := 0
	for key, vals := range values {
		keys = append(keys, key)
		for _, value := range vals {
			headerValueBytes += len(value)
		}
	}
	if len(keys) > httpHeaderCountLimit || headerValueBytes > httpRawAggregateLimit {
		return HTTPCall{}, errors.New("HTTP request exceeds the selected header count or value budget")
	}
	sort.Strings(keys)
	headers := make([]HTTPHeader, 0, len(keys))
	for _, key := range keys {
		headers = append(headers, HTTPHeader{Name: allowed[key], Values: append([]string(nil), values[key]...)})
	}
	call.Headers = headers
	return call, nil
}

func validateHTTPFields(method, path, query string) error {
	if !validHTTPToken(method) || len(method) > httpHeaderNameLimit || !utf8.ValidString(path) || !utf8.ValidString(query) ||
		!strings.HasPrefix(path, "/") || strings.ContainsAny(path, "?#\x00\r\n") || strings.ContainsAny(query, "#\x00\r\n") ||
		strings.IndexFunc(path+query, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		return errors.New("HTTP method, raw path or query is invalid")
	}
	rawLen := len(path) + len(query)
	if query != "" {
		rawLen++
	}
	if rawLen > httpRawAggregateLimit {
		return errors.New("HTTP raw path and query exceed 16 KiB")
	}
	return nil
}

func validHTTPRoots(roots []string) bool {
	if len(roots) > httpHeaderCountLimit {
		return false
	}
	total := 0
	for _, root := range roots {
		if len(root) > httpHeaderNameLimit || !utf8.ValidString(root) {
			return false
		}
		total += len(root)
	}
	return total <= 6<<10
}

func validHTTPToken(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if !(r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || strings.ContainsRune("!#$%&'*+-.^_`|~", r)) {
			return false
		}
	}
	return true
}

func validHTTPHeaderName(value string) (string, bool) {
	if len(value) == 0 || len(value) > httpHeaderNameLimit || !validHTTPToken(value) {
		return "", false
	}
	return http.CanonicalHeaderKey(value), true
}

func validHTTPHeaderValue(value string) bool {
	if !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if r == '\r' || r == '\n' || r == 0 || r < 0x20 && r != '\t' || r == 0x7f {
			return false
		}
	}
	return true
}

func blockedHTTPHeader(name string) bool {
	switch strings.ToLower(name) {
	case "authorization", "host", "cookie", "set-cookie", "yolo-verdict", "yolo-request", "connection", "keep-alive", "proxy-connection", "proxy-authenticate", "proxy-authorization", "te", "trailer", "transfer-encoding", "upgrade":
		return true
	}
	return strings.HasPrefix(strings.ToLower(name), "yolo-") || strings.HasPrefix(strings.ToLower(name), "proxy-")
}

func sameHeaders(a, b []HTTPHeader) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name || len(a[i].Values) != len(b[i].Values) {
			return false
		}
		for j := range a[i].Values {
			if a[i].Values[j] != b[i].Values[j] {
				return false
			}
		}
	}
	return true
}

func digestHTTPBody(present bool, body []byte) string {
	h := sha256.New()
	writeDigestPart(h, []byte{boolByte(present)})
	writeDigestPart(h, body)
	return hex.EncodeToString(h.Sum(nil))
}

func digestHTTPCall(call HTTPCall, reads [][]Resource, writes []Write) string {
	h := sha256.New()
	writeDigestPart(h, []byte(call.Method))
	writeDigestPart(h, []byte(call.Path))
	writeDigestPart(h, []byte(call.Query))
	writeDigestPart(h, []byte{boolByte(call.BodyPresent)})
	writeDigestPart(h, call.Body)
	writeHTTPHeaders(h, call.Headers)
	writeDigestPart(h, uint64Bytes(uint64(len(reads))))
	for _, chain := range reads {
		writeResourceChain(h, chain)
	}
	writeDigestPart(h, uint64Bytes(uint64(len(writes))))
	for _, write := range writes {
		writeDigestPart(h, []byte(write.Cell))
		writeDigestPart(h, []byte(write.Description))
		writeResourceChain(h, write.Chain)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func writeHTTPHeaders(w io.Writer, headers []HTTPHeader) {
	writeDigestPart(w, uint64Bytes(uint64(len(headers))))
	for _, header := range headers {
		writeDigestPart(w, []byte(strings.ToLower(header.Name)))
		writeDigestPart(w, uint64Bytes(uint64(len(header.Values))))
		for _, value := range header.Values {
			writeDigestPart(w, []byte(value))
		}
	}
}

func writeResourceChain(w io.Writer, chain []Resource) {
	writeDigestPart(w, uint64Bytes(uint64(len(chain))))
	for _, resource := range chain {
		writeDigestPart(w, []byte(resource.Type))
		writeDigestPart(w, []byte(resource.ID))
	}
}

func writeDigestPart(w io.Writer, value []byte) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = w.Write(length[:])
	_, _ = w.Write(value)
}

func uint64Bytes(n uint64) []byte {
	var out [8]byte
	binary.BigEndian.PutUint64(out[:], n)
	return out[:]
}
func boolByte(value bool) byte {
	if value {
		return 1
	}
	return 0
}

func validateHTTPAnswerShape(data []byte) error {
	var answer map[string]json.RawMessage
	if err := json.Unmarshal(data, &answer); err != nil || answer == nil {
		return errors.New("answer must be one JSON object")
	}
	if err := checkExactFields(answer, "version", "classification", "lookups", "refused", "local"); err != nil {
		return err
	}
	if err := requireJSONString(answer, "version"); err != nil {
		return err
	}
	for key, want := range map[string]byte{"classification": '{', "lookups": '[', "refused": '{', "local": 'b'} {
		raw, ok := answer[key]
		if !ok {
			continue
		}
		if !hasJSONType(raw, want) {
			return fmt.Errorf("%s has the wrong JSON type", key)
		}
	}
	if raw, ok := answer["refused"]; ok {
		var refused map[string]json.RawMessage
		if !hasJSONType(raw, '{') || json.Unmarshal(raw, &refused) != nil || refused == nil {
			return errors.New("refused must be an object")
		}
		if err := checkExactFields(refused, "class", "reason", "next_step"); err != nil {
			return err
		}
		for _, key := range []string{"class", "reason", "next_step"} {
			if err := requireJSONString(refused, key); err != nil {
				return err
			}
		}
	}
	if raw, ok := answer["lookups"]; ok {
		var lookups []json.RawMessage
		if !hasJSONType(raw, '[') || json.Unmarshal(raw, &lookups) != nil {
			return errors.New("lookups must be an array")
		}
		for _, rawLookup := range lookups {
			var lookup map[string]json.RawMessage
			if !hasJSONType(rawLookup, '{') || json.Unmarshal(rawLookup, &lookup) != nil || lookup == nil {
				return errors.New("lookup must be an object")
			}
			if err := checkExactFields(lookup, "name", "arguments"); err != nil {
				return err
			}
			if err := requireJSONString(lookup, "name"); err != nil {
				return err
			}
			if err := requireStringArray(lookup, "arguments"); err != nil {
				return err
			}
		}
	}
	classificationRaw, ok := answer["classification"]
	if !ok {
		return nil
	}
	var classification map[string]json.RawMessage
	if err := json.Unmarshal(classificationRaw, &classification); err != nil || classification == nil {
		return errors.New("classification must be an object")
	}
	if err := checkExactFields(classification, "canonical", "reads", "writes"); err != nil {
		return err
	}
	canonicalRaw, ok := classification["canonical"]
	if !ok || !hasJSONType(canonicalRaw, '{') {
		return errors.New("classification canonical request must be an object")
	}
	var canonical map[string]json.RawMessage
	if err := json.Unmarshal(canonicalRaw, &canonical); err != nil {
		return err
	}
	if err := checkExactFields(canonical, "method", "path", "query", "headers", "body"); err != nil {
		return err
	}
	for _, key := range []string{"method", "path", "query"} {
		if err := requireJSONString(canonical, key); err != nil {
			return err
		}
	}
	headersRaw, ok := canonical["headers"]
	if !ok || !hasJSONType(headersRaw, '[') {
		return errors.New("canonical headers must be a JSON array")
	}
	var headers []json.RawMessage
	if err := json.Unmarshal(headersRaw, &headers); err != nil {
		return err
	}
	for _, raw := range headers {
		var header map[string]json.RawMessage
		if !hasJSONType(raw, '{') || json.Unmarshal(raw, &header) != nil || header == nil {
			return errors.New("canonical header must be an object")
		}
		if err := checkExactFields(header, "name", "values"); err != nil {
			return err
		}
		if err := requireJSONString(header, "name"); err != nil {
			return err
		}
		values, ok := header["values"]
		if !ok || !hasJSONType(values, '[') {
			return errors.New("canonical header values must be an array")
		}
		var stringsOnly []json.RawMessage
		if err := json.Unmarshal(values, &stringsOnly); err != nil {
			return err
		}
		for _, value := range stringsOnly {
			if len(bytes.TrimSpace(value)) == 0 || bytes.TrimSpace(value)[0] != '"' {
				return errors.New("canonical header value must be a string")
			}
		}
	}
	bodyRaw, ok := canonical["body"]
	if !ok || !hasJSONType(bodyRaw, '{') {
		return errors.New("canonical body must be an object")
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(bodyRaw, &body); err != nil || body == nil {
		return errors.New("canonical body must be an object")
	}
	if err := checkExactFields(body, "mode", "present", "sha256", "base64"); err != nil {
		return err
	}
	if err := requireJSONString(body, "mode"); err != nil {
		return err
	}
	present, ok := body["present"]
	if !ok || !hasJSONType(present, 'b') {
		return errors.New("canonical body presence must be a boolean")
	}
	var mode string
	_ = json.Unmarshal(body["mode"], &mode)
	switch mode {
	case "original":
		if err := requireJSONString(body, "sha256"); err != nil {
			return err
		}
		if _, exists := body["base64"]; exists {
			return errors.New("original body reference must not include inline bytes")
		}
	case "inline_base64":
		if err := requireJSONString(body, "base64"); err != nil {
			return err
		}
		if _, exists := body["sha256"]; exists {
			return errors.New("inline body rewrite must not claim an original digest")
		}
	default:
		return errors.New("canonical body mode is unknown")
	}
	if err := validateHTTPAnswerResourceLists(classification); err != nil {
		return err
	}
	return nil
}

func validateHTTPAnswerResourceLists(classification map[string]json.RawMessage) error {
	if raw, ok := classification["reads"]; ok {
		var chains [][]json.RawMessage
		if !hasJSONType(raw, '[') || json.Unmarshal(raw, &chains) != nil {
			return errors.New("reads must be an array of resource chains")
		}
		for _, chain := range chains {
			for _, resource := range chain {
				if err := validateHTTPResourceShape(resource); err != nil {
					return err
				}
			}
		}
	}
	if raw, ok := classification["writes"]; ok {
		var writes []json.RawMessage
		if !hasJSONType(raw, '[') || json.Unmarshal(raw, &writes) != nil {
			return errors.New("writes must be an array")
		}
		for _, rawWrite := range writes {
			var write map[string]json.RawMessage
			if !hasJSONType(rawWrite, '{') || json.Unmarshal(rawWrite, &write) != nil || write == nil {
				return errors.New("write must be an object")
			}
			if err := checkExactFields(write, "cell", "chain", "description"); err != nil {
				return err
			}
			for _, key := range []string{"cell", "description"} {
				if err := requireJSONString(write, key); err != nil {
					return err
				}
			}
			var chain []json.RawMessage
			chainRaw, ok := write["chain"]
			if !ok || !hasJSONType(chainRaw, '[') || json.Unmarshal(chainRaw, &chain) != nil {
				return errors.New("write chain must be an array")
			}
			for _, resource := range chain {
				if err := validateHTTPResourceShape(resource); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func validateHTTPResourceShape(raw json.RawMessage) error {
	var resource map[string]json.RawMessage
	if !hasJSONType(raw, '{') || json.Unmarshal(raw, &resource) != nil || resource == nil {
		return errors.New("resource must be an object")
	}
	if err := checkExactFields(resource, "type", "id"); err != nil {
		return err
	}
	if err := requireJSONString(resource, "type"); err != nil {
		return err
	}
	return requireJSONString(resource, "id")
}

func requireStringArray(object map[string]json.RawMessage, key string) error {
	raw, ok := object[key]
	if !ok || !hasJSONType(raw, '[') {
		return fmt.Errorf("%s must be a JSON string array", key)
	}
	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return err
	}
	for _, value := range values {
		if !hasJSONType(value, '"') {
			return fmt.Errorf("%s must contain only strings", key)
		}
	}
	return nil
}

func checkExactFields(object map[string]json.RawMessage, allowed ...string) error {
	accepted := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		accepted[key] = struct{}{}
	}
	for key := range object {
		if _, ok := accepted[key]; !ok {
			return fmt.Errorf("unknown or incorrectly cased field %q", key)
		}
	}
	return nil
}

func requireJSONString(object map[string]json.RawMessage, key string) error {
	raw, ok := object[key]
	if !ok || !hasJSONType(raw, '"') {
		return fmt.Errorf("%s must be a JSON string", key)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("%s must be a JSON string", key)
	}
	return nil
}

func hasJSONType(raw json.RawMessage, want byte) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && (trimmed[0] == want || want == 'b' && (trimmed[0] == 't' || trimmed[0] == 'f'))
}

func rejectDuplicateJSONFields(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := scanJSONValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("trailing JSON value")
	}
	return nil
}

func scanJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("object key is not a string")
			}
			if _, exists := seen[key]; exists {
				return fmt.Errorf("duplicate object field %q", key)
			}
			seen[key] = struct{}{}
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	case '[':
		for decoder.More() {
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	default:
		return errors.New("unexpected JSON delimiter")
	}
}
