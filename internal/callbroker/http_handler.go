package callbroker

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokeraudit"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/callbrokerinput"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

// HTTPHandler uses the same selected launch and live host authority as the CLI
// handler, but cannot run or inspect a host CLI to make an HTTP decision.
type HTTPHandler struct {
	*CLIHandler
	beforeHTTPCommit func() // deterministic store-boundary test seam; nil in production
	beforeSealWrite  func() // between the token and seal leaf writes; test seam, nil in production
	// front is this broker start's sealed selected front, if any. Only Handle
	// reads it; approval authority never builds or consults it.
	front httpFrontState
}

func newHTTPHandler(inputFile, workspace string, store *brokerstore.Store, audit *brokeraudit.Log) (*HTTPHandler, error) {
	data, err := readPrivateInput(inputFile)
	if err != nil {
		return nil, err
	}
	if err := callbrokerinput.RejectDuplicateLaunchHTTPFrontGeneration(data); err != nil {
		return nil, errors.New("selected HTTP call broker input has an invalid prepared-generation reference")
	}
	var in callbrokerinput.Launch
	if err := json.Unmarshal(data, &in); err != nil || in.Version != "yolo.call-broker-launch/2" || !loopholedecl.ValidBrokerSourceName(in.Source) || in.Front != "http" || in.HTTPBinding == nil || in.HTTPBinding.Pack != in.Pack || in.HTTPBinding.Source != in.Source || in.DeclarationHash == "" || in.HTTPBinding.DeclarationHash != in.DeclarationHash || store == nil || store.SourceName() != in.Source || audit == nil || workspace == "" {
		return nil, errors.New("selected HTTP call broker binding is invalid; relaunch with the pack selected")
	}
	if in.HTTPFrontGeneration != "" {
		if err := validatePreparedHTTPGeneration(inputFile, in); err != nil {
			return nil, err
		}
	}
	c := Classifier{Source: in.Source, Argv: in.Classifier, Identities: in.ProgramIdentities,
		Roots: in.Roots, Resources: in.Resources, Cells: in.Cells, Lookups: in.Lookups,
		ReadOverPost: slices.Clone(in.HTTPBinding.ReadOverPost)}
	return &HTTPHandler{CLIHandler: &CLIHandler{inputFile: inputFile, original: bytes.Clone(data), input: in,
		workspace: workspace, shutdown: context.Background(), classifier: c, store: store, audit: audit}}, nil
}

func validatePreparedHTTPGeneration(inputFile string, in callbrokerinput.Launch) error {
	if len(in.HTTPFrontGeneration) != 32 {
		return errors.New("selected HTTP prepared generation reference is invalid; relaunch the selected pack")
	}
	if _, err := hex.DecodeString(in.HTTPFrontGeneration); err != nil {
		return errors.New("selected HTTP prepared generation reference is invalid; relaunch the selected pack")
	}
	preparedPath := inputFile + ".prepared"
	data, err := readPrivateInput(preparedPath)
	if err != nil {
		return errors.New("selected HTTP prepared identity is missing or unsafe; relaunch the selected pack")
	}
	prepared, err := callbrokerinput.DecodePreparedHTTPClientGeneration(data)
	if err != nil {
		return errors.New("selected HTTP prepared identity is invalid; relaunch the selected pack")
	}
	scope, err := brokerscope.ReadFile(in.ScopeFile)
	if err != nil {
		return errors.New("selected HTTP scope identity is unavailable; relaunch the selected pack")
	}
	if err := callbrokerinput.ValidatePreparedHTTPClientLaunch(prepared, in, scope.Container, scope.LaunchID, scope.Source, scope.Repos); err != nil {
		return errors.New("selected HTTP prepared identity does not match the selected launch scope; relaunch the selected pack")
	}
	return nil
}

// serveHTTP is reached only from the host-authenticated operation handler. A
// parent-chain read without fresh native facts is refused, never trusted merely
// because the selected classifier asserted its ancestry.
func (h *HTTPHandler) serveHTTP(ctx context.Context, op, jail string, original HTTPCall) (verdict Verdict, response HTTPExecution, err error) {
	outcome, reason, set, digest := "refused", "request-invalid", "read", ""
	defer func() {
		h.audit.Append(brokeraudit.Event{Event: "call", Service: h.input.Source, Jail: jail, Set: set,
			Outcome: outcome, Reason: reason, CommandDigest: digest})
	}()
	if op != "evaluate" && op != "ask" && op != "act" || h.start == "" || jail == "" || jail != h.input.FrontJailID {
		return Verdict{Verdict: "refused", Next: "Use this launch's authenticated HTTP front"}, response, errors.New("invalid HTTP operation or host identity")
	}
	sf, _, err := h.currentAuthority()
	if err != nil {
		return Verdict{Verdict: "refused", Next: err.Error()}, response, err
	}
	classifier := h.classifier
	classifier.Roots = slices.Clone(sf.Repos)
	classified, err := classifier.ClassifyHTTPWithLookups(ctx, original, h.input.HTTPBinding.HTTP.RequestHeaders, h.lookupHTTP(jail))
	if err != nil {
		reason = "classifier-failed"
		return Verdict{Verdict: "refused", Next: "Check the selected pack's HTTP classifier and retry"}, response, err
	}
	if classified.Refused != nil {
		reason = "classifier-refused"
		return Verdict{Verdict: "refused", Rule: classified.Refused.Reason, Next: classified.Refused.NextStep}, response, nil
	}
	digest = classified.CanonicalSHA256
	if len(classified.Writes) != 0 {
		set = "write"
		for _, write := range classified.Writes {
			if len(write.Chain) == 0 || !slices.Contains(sf.Repos, write.Chain[len(write.Chain)-1].ID) {
				return Verdict{Verdict: "refused", Rule: "HTTP write left the approved root", Next: "Use yolo request for the intended root", Digest: digest}, response, nil
			}
		}
		if err := h.verifyHTTPNativeWriteChains(ctx, jail, classified.Writes); err != nil {
			reason = "native-facts-unavailable"
			return Verdict{Verdict: "refused", Rule: "HTTP write ancestry is unverified", Next: err.Error(), Digest: digest}, response, nil
		}
		_, _, working, now, err := h.store.InspectContext(ctx)
		if err != nil {
			return Verdict{Verdict: "refused", Next: "Check the host broker store before retrying"}, response, err
		}
		predicted := decideCall(Classification{Reads: classified.Reads, Writes: classified.Writes}, h.input.EffectiveCells, working, h.workspace, h.input.Source, now)
		verdict, response, err = h.boundHTTPWrite(ctx, op, jail, original, classified, predicted)
		if response.PossibleTransmission {
			outcome, reason = "ran", "possible-transmission"
		} else if verdict.Verdict == "asks" && op == "ask" {
			outcome, reason = "pending", "approval-requested"
		} else if op == "evaluate" {
			reason = "evaluation-only"
		}
		return verdict, response, err
	}
	for _, chain := range classified.Reads {
		if len(chain) == 0 || !slices.Contains(sf.Repos, chain[len(chain)-1].ID) {
			reason = "native-facts-unavailable"
			return Verdict{Verdict: "refused", Rule: "HTTP read root is outside the approved scope", Next: "Use yolo request to ask for the intended root", Digest: digest}, response, nil
		}
	}
	if err := h.verifyHTTPNativeReads(ctx, jail, classified.Reads); err != nil {
		reason = "native-facts-unavailable"
		return Verdict{Verdict: "refused", Rule: "HTTP read ancestry is unverified", Next: err.Error(), Digest: digest}, response, nil
	}
	verdict = Verdict{Verdict: "runs", Rule: "current standing read", Digest: digest}
	if op != "act" {
		reason = "evaluation-only"
		return verdict, response, nil
	}
	if _, _, err := h.currentAuthority(); err != nil {
		return Verdict{Verdict: "refused", Next: err.Error(), Digest: digest}, response, err
	}
	if h.input.Executor != "core" {
		return Verdict{Verdict: "refused", Rule: "custom HTTP signer has no approved execution protocol", Next: "Use a core HTTP executor pack or wait for the approved signer contract", Digest: digest}, response, nil
	}
	if err := h.audit.RequiredContext(ctx, brokeraudit.Event{Event: "call-authorization", Service: h.input.Source, Jail: jail,
		Set: "read", Outcome: "authorized-before-dispatch", CommandDigest: digest}); err != nil {
		return Verdict{Verdict: "refused", Next: "Repair the host audit log before retrying", Digest: digest}, response, err
	}
	credential, err := loadHTTPCredential(h.input.HTTPBinding.HTTP.Credential, []string{h.input.HTTPBinding.HTTP.Credential.Key})
	if err != nil {
		return Verdict{Verdict: "refused", Next: err.Error(), Digest: digest}, response, err
	}
	if httpURLContainsCredential(original, credential) {
		return Verdict{Verdict: "refused", Next: "Known host credential appears in the original URL; remove it before retrying", Digest: digest}, response, errors.New("HTTP original URL contains host credential")
	}
	if _, _, err := h.currentAuthority(); err != nil {
		return Verdict{Verdict: "refused", Next: err.Error(), Digest: digest}, response, err
	}
	response, err = h.runStandingHTTP(ctx, classified.Canonical, credential)
	if response.PossibleTransmission {
		outcome, reason = "ran", "possible-transmission"
	}
	if err != nil {
		if response.PossibleTransmission {
			return Verdict{Verdict: "refused", Next: "Host HTTP transmission may have occurred; inspect remote state before another request", Digest: digest}, response, err
		}
		return Verdict{Verdict: "refused", Next: err.Error(), Digest: digest}, response, err
	}
	reason = ""
	return verdict, response, nil
}

// Handle consumes a bounded hostservice frame with its jail identity supplied by
// the authenticated preamble, never from caller-controlled HTTP headers.
func (h *HTTPHandler) Handle(s *hostservice.Session) {
	if !s.Fronted || s.JailID == "" || s.JailID == "unknown" {
		s.Stderr("HTTP call broker requires an authenticated launch front; relaunch the jail.\n")
		s.Exit(64)
		return
	}
	originalFrame, err := jsonx.DumpsCompact(s.Request)
	if err != nil || validateHTTPBrokerFrame([]byte(originalFrame)) != nil {
		s.Stderr("Invalid original HTTP broker schema; update the selected client and retry.\n")
		s.Exit(64)
		return
	}
	for _, key := range s.Request.Keys() {
		if key != "op" && key != "http" && key != "jail_id" && key != "front_generation" && key != "front" {
			s.Stderr("Unsupported HTTP broker field; update the selected client.\n")
			s.Exit(64)
			return
		}
	}
	mode, ok := s.Get("op")
	if !ok {
		s.Stderr("Missing HTTP broker operation; update the selected client.\n")
		s.Exit(64)
		return
	}
	op, ok := mode.(string)
	generationValue, generationPresent := s.Get("front_generation")
	generation, generationOK := generationValue.(string)
	if generationPresent && !generationOK {
		s.Stderr("HTTP broker refused: " + errFrontGenerationWrong.Error() + "\n")
		s.Exit(64)
		return
	}
	if ok && op == httpFrontSealOp {
		h.handleSeal(s, generation)
		return
	}
	raw, present := s.Get("http")
	if !ok || !present {
		s.Stderr("Invalid HTTP broker operation or request; update the selected client.\n")
		s.Exit(64)
		return
	}
	encodedCall, err := jsonx.DumpsCompact(raw)
	if err != nil {
		s.Stderr("Cannot decode HTTP broker request; update the selected client.\n")
		s.Exit(64)
		return
	}
	var original HTTPCall
	decoder := json.NewDecoder(bytes.NewBufferString(encodedCall))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&original); err != nil {
		s.Stderr("Invalid HTTP request fields; update the selected client.\n")
		s.Exit(64)
		return
	}
	if err := h.checkFrontGeneration(generationPresent, generation, []byte(originalFrame), original.Body); err != nil {
		s.Stderr("HTTP broker refused: " + err.Error() + "\n")
		s.Exit(64)
		return
	}
	ctx, cancel := context.WithTimeout(h.shutdown, 30*time.Second)
	defer cancel()
	verdict, reply, err := h.serveHTTP(ctx, op, s.JailID, original)
	if err != nil {
		s.Stderr("HTTP broker refused: " + verdict.Next + "\n")
		s.Exit(64)
		return
	}
	if op != "act" || verdict.Verdict != "runs" {
		encoded, _ := json.Marshal(verdict)
		s.Stdout(string(encoded) + "\n")
		if op == "act" && verdict.Verdict != "runs" {
			if verdict.Verdict == "asks" {
				s.Exit(75)
			} else {
				s.Exit(64)
			}
		} else {
			s.Exit(0)
		}
		return
	}
	encoded, err := json.Marshal(struct {
		Status  int         `json:"status"`
		Headers http.Header `json:"headers"`
		Body    []byte      `json:"body_base64"`
	}{reply.Status, reply.Headers, reply.Body})
	if err != nil {
		s.Stderr(fmt.Sprintf("HTTP broker reply cannot be encoded; inspect host state before retrying: %v\n", err))
		s.Exit(64)
		return
	}
	s.StdoutBytes(encoded)
	s.Exit(0)
}
