package callbroker

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokeraudit"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

func (h *HTTPHandler) requestHTTPWrite(jail string, result HTTPResult) brokerstore.Request {
	q := brokerstore.Request{Workspace: h.workspace, Jail: jail, Start: h.start, LaunchID: h.bound.LaunchID,
		Set: h.input.Source, Repo: h.input.Source, Argv: []string{"http"}, StdinDigest: result.OriginalIntentSHA256,
		HTTPOriginalDigest: result.OriginalIntentSHA256, HTTPCanonicalDigest: result.CanonicalSHA256,
		HTTPDeclarationHash: h.input.DeclarationHash, Policy: oncePolicy}
	for _, write := range result.Writes {
		entry := brokerstore.CallWrite{Cell: write.Cell}
		for _, r := range write.Chain {
			entry.Chain = append(entry.Chain, brokerstore.CallResource{Type: r.Type, ID: r.ID})
		}
		q.CallWrites = append(q.CallWrites, entry)
	}
	q.Digest = brokerstore.CommandDigest(q)
	return q
}

// revalidateHTTP is used by the host approval adapter and by the exact store
// transaction. It cannot replay a body from pending storage: act reclassifies
// the resubmitted original inside dispatch before any request is transmitted.
func (h *HTTPHandler) revalidateHTTP(q brokerstore.Request) (brokerstore.Policy, error) {
	sf, user, err := h.currentAuthority()
	if err != nil {
		return brokerstore.Policy{}, err
	}
	if q.Set != h.input.Source || q.Repo != h.input.Source || q.Start != h.start || q.LaunchID != sf.LaunchID || q.Workspace != sf.Workspace || q.Jail != h.input.FrontJailID || q.HTTPDeclarationHash != h.input.DeclarationHash || q.StdinDigest != q.HTTPOriginalDigest || len(q.Argv) != 1 || q.Argv[0] != "http" || len(q.OriginalArgv) != 0 || len(q.CallWrites) == 0 || q.Digest != brokerstore.CommandDigest(q) || !validHTTPHash(q.HTTPOriginalDigest) || !validHTTPHash(q.HTTPCanonicalDigest) {
		return brokerstore.Policy{}, errors.New("HTTP approval does not match the live selected host launch; resubmit the original call")
	}
	for _, write := range q.CallWrites {
		if len(write.Chain) == 0 || !slices.Contains(sf.Repos, write.Chain[len(write.Chain)-1].ID) {
			return brokerstore.Policy{}, errors.New("HTTP write left the selected root; use yolo request and retry")
		}
		policy, exists := h.input.EffectiveCells[write.Cell]
		frozen, declared := policy.Tables["all"]
		current := config.CallBrokerCurrentCeiling(user, h.input.Source, "all", write.Cell)
		if !exists || !declared || frozen.Decision == "refuse" || frozen.Request == "false" || frozen.Ceiling == "off" || current == "off" || frozen.Ceiling == "always" && current != "always" {
			return brokerstore.Policy{}, errors.New("HTTP write host ceiling or selected cell was withdrawn; use yolo allow and yolo config apply")
		}
	}
	return oncePolicy, nil
}

func validHTTPHash(hash string) bool {
	if len(hash) != 64 {
		return false
	}
	for _, c := range hash {
		if c < '0' || c > '9' && c < 'a' || c > 'f' {
			return false
		}
	}
	return true
}

func (h *HTTPHandler) boundHTTPWrite(ctx context.Context, op, jail string, original HTTPCall, result HTTPResult, predicted Verdict) (Verdict, HTTPExecution, error) {
	var reply HTTPExecution
	q := h.requestHTTPWrite(jail, result)
	predicted.Digest = q.Digest
	if predicted.Verdict == "refused" {
		return predicted, reply, nil
	}
	if h.input.Executor != "core" {
		return Verdict{Verdict: "refused", Rule: "custom HTTP signer has no approved execution protocol", Next: "Use a core HTTP executor pack or wait for the approved signer contract", Digest: q.Digest}, reply, nil
	}
	if _, err := h.revalidateHTTP(q); err != nil {
		return Verdict{Verdict: "refused", Next: err.Error(), Digest: q.Digest}, reply, err
	}
	var fresh HTTPResult
	gate := func(held brokerstore.Request) (brokerstore.Policy, error) {
		if _, err := h.revalidateHTTP(q); err != nil {
			return brokerstore.Policy{}, err
		}
		// Store calls this under its source transaction before it can change a
		// grant's uses. No archived HTTP bytes can satisfy this check.
		current, err := h.classifier.ClassifyHTTPWithLookups(ctx, original, h.input.HTTPBinding.HTTP.RequestHeaders, h.lookupHTTP(jail))
		if err != nil || current.Refused != nil || current.OriginalIntentSHA256 != q.HTTPOriginalDigest || current.CanonicalSHA256 != q.HTTPCanonicalDigest || !reflect.DeepEqual(h.requestHTTPWrite(jail, current).CallWrites, q.CallWrites) {
			return brokerstore.Policy{}, errors.New("HTTP original body, canonical request or ordered writes changed; resubmit and ask again")
		}
		if err := h.verifyHTTPNativeWriteChains(ctx, jail, current.Writes); err != nil {
			return brokerstore.Policy{}, err
		}
		policy, err := h.revalidateHTTP(held)
		if err == nil && op == "act" {
			err = h.audit.RequiredContext(ctx, brokeraudit.Event{Event: "call-preflight", Service: h.input.Source, Jail: jail,
				Set: "write", Outcome: "verified-before-store-dispatch", CommandDigest: q.Digest})
		}
		if err == nil {
			fresh = current
		}
		return policy, err
	}
	inspection, err := h.store.InspectExactGrantContext(ctx, q, gate)
	if err != nil || inspection.EligibilityErr != nil {
		if err == nil {
			err = inspection.EligibilityErr
		}
		return Verdict{Verdict: "refused", Next: "Check current host approval and retry the original call", Digest: q.Digest}, reply, err
	}
	if inspection.Eligible {
		predicted.Verdict, predicted.Rule = "runs", "exact Allow once is available"
	}
	if op == "evaluate" {
		return predicted, reply, nil
	}
	attempted := false
	var credential string
	var preparedRequest *preparedHTTPRequest
	defer func() {
		if preparedRequest != nil {
			preparedRequest.close()
		}
	}()
	prepared := false
	preflightFailed := false
	prepare := func() (err error) {
		defer func() {
			if err != nil {
				preflightFailed = true
			}
		}()
		if h.beforeHTTPCommit != nil {
			h.beforeHTTPCommit()
		}
		if _, err := h.revalidateHTTP(q); err != nil {
			return err
		}
		credential, err = loadHTTPCredential(h.input.HTTPBinding.HTTP.Credential, []string{h.input.HTTPBinding.HTTP.Credential.Key})
		if err != nil {
			return err
		}
		if httpURLContainsCredential(original, credential) {
			return errors.New("HTTP original URL contains the known host credential; remove it before retrying")
		}
		if _, err := h.revalidateHTTP(q); err != nil {
			return err
		}
		preparedRequest, err = h.httpClient.preflight(ctx, h.input.HTTPBinding.HTTP, fresh.Canonical, credential)
		if err != nil {
			return err
		}
		if _, err := h.revalidateHTTP(q); err != nil {
			return err
		}
		prepared = true
		return nil
	}
	dispatch := func() error {
		// The source transaction validated the resubmitted original and
		// captured host-only credentials before any use was committed.
		if !prepared || preparedRequest == nil || fresh.CanonicalSHA256 != q.HTTPCanonicalDigest {
			return errors.New("HTTP transaction has no freshly checked original; retry the call")
		}
		var err error
		reply, err = preparedRequest.send()
		attempted = reply.PossibleTransmission
		return err
	}
	coverage := func(working []brokerstore.WorkingEntry, now time.Time) (bool, error) {
		decision := decideCall(Classification{Writes: result.Writes, Reads: result.Reads}, h.input.EffectiveCells, working, q.Workspace, h.input.Source, now)
		if decision.Verdict == "refused" {
			return false, errors.New("one HTTP write is refused by current coverage; change the request and retry")
		}
		if decision.Verdict != "runs" {
			return false, nil
		}
		if op == "act" {
			if err := prepare(); err != nil {
				return false, err
			}
			if err := dispatch(); err != nil {
				return false, err
			}
		}
		return true, nil
	}
	filed, _, covered, err := h.store.FileIfStillAsksWithCoverageContext(ctx, q, gate, coverage)
	if err != nil {
		return h.httpWriteFailure(q.Digest, attempted, preflightFailed, err), reply, err
	}
	if covered {
		if op == "ask" {
			return Verdict{Verdict: "runs", Rule: "covered without spending", Digest: q.Digest}, reply, nil
		}
		_, spent, next, spendErr := h.store.SpendExactOrFilePreparedContext(ctx, q, gate, prepare, dispatch)
		if spendErr != nil {
			return h.httpWriteFailure(q.Digest, attempted, preflightFailed, spendErr), reply, spendErr
		}
		if spent || reply.PossibleTransmission {
			return Verdict{Verdict: "runs", Rule: "exact Allow once spent", Digest: q.Digest}, reply, nil
		}
		filed = next
	}
	if filed.ID == "" {
		return Verdict{Verdict: "refused", Next: "Inspect the host approval store before retrying", Digest: q.Digest}, reply, errors.New("HTTP call was neither filed nor covered")
	}
	return Verdict{Verdict: "asks", Rule: "host approval required", Next: "Ask the human to run `yolo approve " + filed.ID + "` on the host, then resubmit the original call", Digest: q.Digest}, reply, nil
}

func (h *HTTPHandler) httpWriteFailure(digest string, attempted, preflightFailed bool, err error) Verdict {
	if attempted {
		return Verdict{Verdict: "refused", Rule: "HTTP transmission may have occurred", Next: "Inspect the remote host state before any new request; a spent Allow once is not refunded", Digest: digest}
	}
	if preflightFailed {
		return Verdict{Verdict: "refused", Rule: "HTTP preflight refused before spend", Next: "No HTTP request bytes were transmitted and Allow once was not spent; repair host authority, credential or TLS before resubmitting", Digest: digest}
	}
	if _, lapsed := brokerstore.IsLapsedAllow(err); lapsed {
		return Verdict{Verdict: "refused", Next: "Allow once expired; resubmit the original call to file a new host request", Digest: digest}
	}
	return Verdict{Verdict: "refused", Rule: "HTTP request bytes were not observed", Next: "Inspect host approval state before resubmitting: a grant may already be spent even though no HTTP request bytes were observed", Digest: digest}
}
