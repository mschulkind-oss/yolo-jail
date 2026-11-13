package callbroker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokeraudit"
	"github.com/mschulkind-oss/yolo-jail/internal/callbrokerinput"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
)

// publish answers one selected private client's bootstrap with its front's URL, local token and
// trust (pack-brokers-http-plan.md, Task 4). It publishes only to this launch's jail, only for a
// generation this broker start sealed and has not withdrawn, only after the prepared record and
// live authority check again, only to a client the prepared record names exactly once, and only
// when that client/setup cell is published. Every answer is audited without its values.
func (h *HTTPHandler) publish(jail, generation, bin string) (pub callbrokerinput.HTTPClientPublication, err error) {
	outcome, reason := "refused", ""
	defer func() {
		if err == nil {
			outcome = "published"
		} else {
			reason = err.Error()
		}
		h.audit.Append(brokeraudit.Event{Event: "publish", Service: h.input.Source, Jail: jail, Set: "client:" + bin, Outcome: outcome, Reason: reason})
	}()
	if h.input.HTTPFrontGeneration == "" {
		return pub, errors.New("this launch has no selected HTTP front generation; relaunch with the client pack selected")
	}
	if generation != h.input.HTTPFrontGeneration {
		return pub, errFrontGenerationWrong
	}
	if jail == "" || jail != h.input.FrontJailID {
		return pub, errors.New("HTTP client publication was not asked by this launch's jail; stop the jail and relaunch")
	}
	prepared, err := validatePreparedHTTPGeneration(h.inputFile, h.input)
	if err != nil {
		return pub, err
	}
	if _, _, err := h.currentAuthority(); err != nil {
		return pub, err
	}
	var plan *callbrokerinput.PreparedHTTPClientPlan
	for i := range prepared.Clients {
		if prepared.Clients[i].Bin == bin {
			if plan != nil {
				return pub, fmt.Errorf("the prepared generation names client %s twice; stop the jail and relaunch", bin)
			}
			plan = &prepared.Clients[i]
		}
	}
	if plan == nil {
		return pub, fmt.Errorf("client %s is not a selected client of this front; run it through its own pack's front", bin)
	}
	cell := callbrokerinput.HTTPClientCell{Pack: plan.Pack, Bin: plan.Bin, RecipeIdentity: plan.RecipeIdentity, Setup: prepared.Setup}
	if h.published == nil || !h.published(cell) {
		return pub, fmt.Errorf("client %s (pack %s) is not published under setup %s: that cell has not passed its own publication proof; it runs without the front's URL, token and trust", plan.Bin, plan.Pack, prepared.Setup)
	}
	h.front.mu.Lock()
	defer h.front.mu.Unlock()
	if h.front.withdrawn {
		return pub, errFrontWithdrawn
	}
	if h.front.seal == nil || h.start == "" || h.front.seal.BrokerStart != h.start || h.front.seal.Generation != generation || h.front.token == "" {
		return pub, errFrontNotSealed
	}
	pub = callbrokerinput.HTTPClientPublication{Token: h.front.token, URL: "https://" + h.front.seal.Address,
		CertificateBase64: base64.StdEncoding.EncodeToString(h.front.seal.CertificateDER),
		TokenVariable:     plan.TokenVariable, URLVariable: plan.URLVariable, TrustVariable: plan.TrustVariable}
	if err := pub.Validate(); err != nil {
		return callbrokerinput.HTTPClientPublication{}, errors.New(err.Error() + "; stop the jail and relaunch")
	}
	return pub, nil
}

// handlePublish answers the publication operation. Its jail identity is the authenticated
// preamble's, never the frame's hint.
func (h *HTTPHandler) handlePublish(s *hostservice.Session, generation string) {
	raw, _ := s.Get("client")
	bin, ok := raw.(string)
	if !ok || bin == "" {
		s.Stderr("Invalid HTTP client publication request; update yolo in the jail.\n")
		s.Exit(64)
		return
	}
	pub, err := h.publish(s.JailID, generation, bin)
	if err != nil {
		s.Stderr("HTTP client publication refused: " + err.Error() + "\n")
		s.Exit(64)
		return
	}
	answer, err := json.Marshal(pub)
	if err != nil {
		s.Stderr("HTTP client publication cannot be encoded; stop the jail and relaunch.\n")
		s.Exit(64)
		return
	}
	s.StdoutBytes(answer)
	s.Exit(0)
}

// httpClientPublishTimeout bounds the bootstrap's one publication exchange.
const httpClientPublishTimeout = 30 * time.Second

// FetchHTTPClientPublication is the in-jail side of the publication operation: it asks the host,
// over this jail's own authenticated endpoint for the front's loophole, for bin's publication of
// generation, and returns it validated. A refusal returns the host's reason.
func FetchHTTPClientPublication(ctx context.Context, endpoint, generation, bin string) (callbrokerinput.HTTPClientPublication, error) {
	var pub callbrokerinput.HTTPClientPublication
	if !callbrokerinput.ValidHTTPFrontGeneration(generation) || bin == "" {
		return pub, errors.New("the client's binding names no valid front generation")
	}
	payload, err := json.Marshal(struct {
		Operation  string `json:"op"`
		Generation string `json:"front_generation"`
		Client     string `json:"client"`
	}{callbrokerinput.HTTPClientPublishOp, generation, bin})
	if err != nil {
		return pub, err
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, httpClientPublishTimeout)
		defer cancel()
	}
	result, err := HTTPFront{Endpoint: endpoint}.exchange(ctx, payload)
	if err != nil {
		return pub, fmt.Errorf("the host's call broker could not be reached: %w", err)
	}
	if result.exit != 0 {
		message := strings.TrimSpace(result.stderr)
		if message == "" {
			message = fmt.Sprintf("the host refused with exit %d and no reason", result.exit)
		}
		return pub, errors.New(message)
	}
	if err := decodeFrontJSON(result.stdout, &pub); err != nil {
		return callbrokerinput.HTTPClientPublication{}, fmt.Errorf("the host's publication is malformed (%v); update yolo on the host and relaunch", err)
	}
	if err := pub.Validate(); err != nil {
		return callbrokerinput.HTTPClientPublication{}, err
	}
	return pub, nil
}
