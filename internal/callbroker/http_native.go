package callbroker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/mschulkind-oss/yolo-jail/internal/brokeraudit"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

func (h *HTTPHandler) verifyHTTPNativeWriteChains(ctx context.Context, jail string, writes []Write) error {
	chains := make([][]Resource, 0, len(writes))
	for _, write := range writes {
		chains = append(chains, write.Chain)
	}
	return h.verifyHTTPNativeReads(ctx, jail, chains)
}

// verifyHTTPNativeReads obtains each declared parent from a new host read.
// Neither the classifier's chain nor a prior request's fact proves ancestry.
func (h *HTTPHandler) verifyHTTPNativeReads(ctx context.Context, jail string, reads [][]Resource) error {
	seen := make(map[Resource]string)
	for _, chain := range reads {
		if len(chain)-1 > maxLookupRounds {
			return errors.New("HTTP native ancestry exceeds three lookup rounds; narrow the requested resource")
		}
		for i := 0; i+1 < len(chain); i++ {
			child, claimedParent := chain[i], chain[i+1]
			lookup, err := nativeHTTPLookup(h.input.Lookups, child.Type)
			if err != nil {
				return err
			}
			parent, err := h.readHTTPNativeParent(ctx, jail, lookup, child)
			if err != nil {
				return err
			}
			if prior, found := seen[child]; found && prior != parent {
				return errors.New("HTTP native parent changed within the call; inspect the host resource before retrying")
			}
			seen[child] = parent
			if claimedParent.ID != parent {
				return errors.New("HTTP native parent differs from the classified chain; inspect the host resource and retry")
			}
		}
	}
	return nil
}

func nativeHTTPLookup(lookups []loopholedecl.CallBrokerLookup, resource string) (loopholedecl.CallBrokerLookup, error) {
	var matched loopholedecl.CallBrokerLookup
	for _, lookup := range lookups {
		if lookup.HTTPResource == resource {
			if matched.Name != "" {
				return matched, errors.New("HTTP native resource has conflicting lookup mappings; fix the selected pack")
			}
			matched = lookup
		}
	}
	if matched.Name == "" || matched.HTTPMethod != "GET" || !strings.Contains(matched.HTTPPath, "{id}") {
		return matched, errors.New("HTTP native resource has no declared GET mapping; fix the selected pack and retry")
	}
	return matched, nil
}

func (h *HTTPHandler) readHTTPNativeParent(ctx context.Context, jail string, lookup loopholedecl.CallBrokerLookup, child Resource) (string, error) {
	return h.readHTTPNativeParentWithFact(ctx, jail, lookup, child, nil)
}

func (h *HTTPHandler) readHTTPNativeParentWithFact(ctx context.Context, jail string, lookup loopholedecl.CallBrokerLookup, child Resource, fact *json.RawMessage) (parent string, err error) {
	sf, _, err := h.currentAuthority()
	if err != nil {
		return "", err
	}
	if h.input.Executor != "core" {
		return "", errors.New("custom HTTP signer has no approved lookup protocol; use a core executor pack")
	}
	path := strings.ReplaceAll(lookup.HTTPPath, "{id}", url.PathEscape(child.ID))
	original := HTTPCall{Method: "GET", Path: path}
	classifier := h.classifier
	classifier.Roots = slices.Clone(sf.Repos)
	classified, err := classifier.ClassifyHTTP(ctx, original, h.input.HTTPBinding.HTTP.RequestHeaders)
	if err != nil || classified.Refused != nil || len(classified.Writes) != 0 || len(classified.Reads) != 1 || len(classified.Reads[0]) == 0 || classified.Reads[0][0] != child || !slices.Contains(sf.Repos, classified.Reads[0][len(classified.Reads[0])-1].ID) || !sameHTTPBytes(original, classified.Canonical) {
		return "", errors.New("HTTP native lookup did not classify as the identical standing read; fix the selected pack")
	}
	if _, _, err := h.currentAuthority(); err != nil {
		return "", err
	}
	if err := h.audit.RequiredContext(ctx, brokeraudit.Event{Event: "call-authorization", Service: h.input.Source, Jail: jail, Set: "read", Outcome: "authorized-before-lookup", CommandDigest: classified.CanonicalSHA256}); err != nil {
		return "", errors.New("HTTP native lookup audit unavailable; repair the host audit log before retrying")
	}
	credential, err := loadHTTPCredential(h.input.HTTPBinding.HTTP.Credential, []string{h.input.HTTPBinding.HTTP.Credential.Key})
	if err != nil {
		return "", err
	}
	if _, _, err := h.currentAuthority(); err != nil {
		return "", err
	}
	response, err := h.runStandingHTTP(ctx, classified.Canonical, credential)
	outcome := "refused"
	if response.PossibleTransmission {
		outcome = "ran"
	}
	h.audit.Append(brokeraudit.Event{Event: "lookup", Service: h.input.Source, Jail: jail, Set: "read", Outcome: outcome, CommandDigest: classified.CanonicalSHA256})
	if errors.Is(err, errFrontWithdrawn) {
		return "", err
	}
	if err != nil || response.Status != 200 {
		return "", errors.New("HTTP native fact lookup failed or returned no usable response; inspect the host resource before retrying")
	}
	if err := rejectDuplicateJSONFields(response.Body); err != nil {
		return "", errors.New("HTTP native fact has duplicate or trailing fields; inspect the host resource")
	}
	id, err := httpPointerString(response.Body, lookup.HTTPIDPointer)
	if err != nil || id != child.ID {
		return "", errors.New("HTTP native lookup returned a different or missing ID; inspect the host resource")
	}
	if lookup.HTTPParentPointer != "" {
		parent, err = httpPointerString(response.Body, lookup.HTTPParentPointer)
		if err != nil || parent == "" || len(classified.Reads[0]) < 2 || classified.Reads[0][1].ID != parent {
			return "", errors.New("HTTP native lookup returned an unknown or changed parent; inspect the host resource")
		}
	} else if len(classified.Reads[0]) != 1 {
		return "", errors.New("HTTP native root lookup asserted an undeclared parent; inspect the selected pack")
	}
	if _, _, err := h.currentAuthority(); err != nil {
		return "", err
	}
	if fact != nil {
		*fact = append((*fact)[:0], response.Body...)
	}
	return parent, nil
}

func (h *HTTPHandler) lookupHTTP(jail string) LookupReader {
	return func(ctx context.Context, declared loopholedecl.CallBrokerLookup, arguments []string) (json.RawMessage, error) {
		if len(arguments) != 1 || arguments[0] == "" || strings.ContainsAny(arguments[0], "\x00\r\n") {
			return nil, errors.New("HTTP lookup needs one valid native ID; fix the selected classifier")
		}
		bound, err := nativeHTTPLookup(h.input.Lookups, declared.HTTPResource)
		if err != nil || bound.Name != declared.Name || bound.HTTPPath != declared.HTTPPath {
			return nil, errors.New("HTTP lookup is not uniquely declared for its resource; fix the selected pack")
		}
		var fact json.RawMessage
		_, err = h.readHTTPNativeParentWithFact(ctx, jail, bound, Resource{Type: bound.HTTPResource, ID: arguments[0]}, &fact)
		if err != nil {
			return nil, err
		}
		if len(fact) > classifierLimit {
			return nil, errors.New("HTTP lookup facts exceed 64 KiB; narrow the selected read")
		}
		return fact, nil
	}
}

func (h *HTTPHandler) runStandingHTTP(ctx context.Context, canonical HTTPCall, credential string) (HTTPExecution, error) {
	prepared, err := h.httpClient.preflight(ctx, h.input.HTTPBinding.HTTP, canonical, credential)
	if err != nil {
		return HTTPExecution{}, err
	}
	defer prepared.close()
	if _, _, err := h.currentAuthority(); err != nil {
		return HTTPExecution{}, err
	}
	if err := ctx.Err(); err != nil {
		return HTTPExecution{}, err
	}
	if err := h.begin(); err != nil {
		return HTTPExecution{}, err
	}
	defer h.end()
	return prepared.send()
}

func sameHTTPBytes(a, b HTTPCall) bool {
	return a.Method == b.Method && a.Path == b.Path && a.Query == b.Query && a.BodyPresent == b.BodyPresent &&
		bytes.Equal(a.Body, b.Body) && sameHeaders(a.Headers, b.Headers)
}

// httpPointerString implements RFC 6901 object and array segments, selecting
// exactly one JSON string. Its returned bytes are never classifier authority.
func httpPointerString(body []byte, pointer string) (string, error) {
	var current json.RawMessage = bytes.TrimSpace(body)
	if pointer != "" {
		if !strings.HasPrefix(pointer, "/") {
			return "", errors.New("invalid JSON Pointer")
		}
		for _, part := range strings.Split(pointer[1:], "/") {
			var key strings.Builder
			for i := 0; i < len(part); i++ {
				if part[i] != '~' {
					key.WriteByte(part[i])
					continue
				}
				i++
				if i >= len(part) || part[i] != '0' && part[i] != '1' {
					return "", errors.New("invalid JSON Pointer escape")
				}
				if part[i] == '0' {
					key.WriteByte('~')
				} else {
					key.WriteByte('/')
				}
			}
			current = bytes.TrimSpace(current)
			if len(current) == 0 {
				return "", errors.New("JSON Pointer field missing")
			}
			switch current[0] {
			case '{':
				var object map[string]json.RawMessage
				if err := json.Unmarshal(current, &object); err != nil {
					return "", err
				}
				current = object[key.String()]
			case '[':
				var list []json.RawMessage
				index, err := strconv.Atoi(key.String())
				if err != nil || index < 0 || strconv.Itoa(index) != key.String() || json.Unmarshal(current, &list) != nil || index >= len(list) {
					return "", errors.New("JSON Pointer array index missing")
				}
				current = list[index]
			default:
				return "", errors.New("JSON Pointer crossed a scalar")
			}
		}
	}
	current = bytes.TrimSpace(current)
	if len(current) == 0 || current[0] != '"' {
		return "", errors.New("JSON Pointer did not select a string")
	}
	var result string
	if err := json.Unmarshal(current, &result); err != nil || result == "" || strings.IndexFunc(result, unicode.IsControl) >= 0 {
		return "", errors.New("JSON Pointer selected an invalid native ID")
	}
	return result, nil
}
