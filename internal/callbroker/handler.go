package callbroker

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"reflect"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokeraudit"
	"github.com/mschulkind-oss/yolo-jail/internal/brokercore"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/callbrokerinput"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

// CLIHandler is core's hostservice operation handler. The launch-input file is
// host-owned, written from the approved gate result. Selected packs may start
// its read front. A bound launch may file, approve and spend exact Allow once
// only after current host authority passes on each operation.
type CLIHandler struct {
	inputFile  string
	original   []byte
	input      callbrokerinput.Launch
	workspace  string
	scopePath  string
	bound      brokerscope.File
	start      string
	shutdown   context.Context
	classifier Classifier
	program    pinnedCLI
	httpClient httpExecutor
	store      *brokerstore.Store
	audit      *brokeraudit.Log
}

// newCLIHandler consumes the private selected launch input, not a jail frame or
// a mutable workspace config. The live file and staged program are checked again
// on each operation; losing either refuses further calls.
func newCLIHandler(inputFile, workspace string, store *brokerstore.Store, audit *brokeraudit.Log) (*CLIHandler, error) {
	data, err := readPrivateInput(inputFile)
	if err != nil {
		return nil, err
	}
	var input callbrokerinput.Launch
	if err := json.Unmarshal(data, &input); err != nil {
		return nil, errors.New("selected call broker input is invalid; relaunch the jail")
	}
	if input.Version != "yolo.call-broker-launch/2" || !loopholedecl.ValidBrokerSourceName(input.Source) || input.Loophole == "" || input.Front != "cli" || input.Program == "" || store == nil || store.SourceName() != input.Source || audit == nil || workspace == "" {
		return nil, errors.New("selected call broker input is incomplete; relaunch the jail with its pack selected")
	}
	program, err := pinCLI(input.Program, input.HostEnvNames)
	if err != nil {
		return nil, err
	}
	c := Classifier{Source: input.Source, Argv: input.Classifier, Identities: input.ProgramIdentities,
		Roots: input.Roots, Resources: input.Resources, Cells: input.Cells, Lookups: input.Lookups}
	return &CLIHandler{inputFile: inputFile, original: append([]byte(nil), data...), input: input,
		workspace: workspace, shutdown: context.Background(), classifier: c, program: program, store: store, audit: audit}, nil
}

func readPrivateInput(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return nil, errors.New("selected call broker input is missing or not private; relaunch the jail")
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, errors.New("selected call broker input cannot be read; relaunch the jail")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || opened.Mode().Perm() != 0o600 || !os.SameFile(info, opened) {
		return nil, errors.New("selected call broker input changed while opening; relaunch the jail")
	}
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return nil, errors.New("selected call broker input is oversized; relaunch the jail")
	}
	return data, nil
}

var (
	errSelectedInputUnavailable = errors.New("selected call broker input is unavailable")
	errSelectedInputChanged     = errors.New("selected call broker input changed")
)

func (h *CLIHandler) live() error {
	data, err := readPrivateInput(h.inputFile)
	if err != nil {
		return errSelectedInputUnavailable
	}
	if !bytes.Equal(data, h.original) {
		return errSelectedInputChanged
	}
	if h.start != "" {
		if _, err := brokercore.Live(h.input.Source, h.scopePath, h.bound, h.start); err != nil {
			return errSelectedInputUnavailable
		}
	}
	return nil
}

func selectedInputAuditReason(err error) string {
	if errors.Is(err, errSelectedInputChanged) {
		return "selected-input-changed"
	}
	return "selected-input-unavailable"
}

// serveCLI is the operation used by Handle; it accepts only original argv and
// stdin bytes. A read runs through the same classifier and canonical executor as
// a lookup. A bound write revalidates current host authority, files one request
// or spends one exact grant, then invokes the canonical CLI with original stdin.
func (h *CLIHandler) serveCLI(ctx context.Context, op, jail string, argv []string, stdin []byte) (verdict Verdict, result CLIResult, err error) {
	auditSet, auditOutcome, auditReason, auditDigest := "refused", "refused", "request-invalid", ""
	defer func() {
		h.audit.Append(brokeraudit.Event{Event: "call", Service: h.input.Source, Jail: jail,
			Set: auditSet, Outcome: auditOutcome, Reason: auditReason, CommandDigest: auditDigest})
	}()
	if err := h.live(); err != nil {
		auditReason = selectedInputAuditReason(err)
		return Verdict{Verdict: "refused", Next: "Relaunch the jail with this pack selected"}, CLIResult{}, err
	}
	auditReason = "request-invalid"
	if jail == "" || len(argv) == 0 || (op != "evaluate" && op != "ask" && op != "act") {
		return Verdict{Verdict: "refused", Next: "Use a selected call broker with a valid CLI operation"}, CLIResult{}, errors.New("invalid broker operation or host identity")
	}
	if h.start == "" && (!h.input.ReadStanding || h.input.ReadGrantStanding != "granted" || h.input.ReadWithheld) {
		auditReason = "read-standing-missing"
		return Verdict{Verdict: "refused", Rule: "read grant missing", Next: "Ask the human to grant this pack read access with `yolo allow`"}, CLIResult{}, nil
	}
	classifier := h.classifier
	if h.start != "" {
		if jail != h.input.FrontJailID {
			return Verdict{Verdict: "refused", Next: "Use this launch's authenticated jail front"}, CLIResult{}, errors.New("jail identity differs from host launch")
		}
		sf, _, currentErr := h.currentAuthority()
		if currentErr != nil {
			return Verdict{Verdict: "refused", Next: currentErr.Error()}, CLIResult{}, currentErr
		}
		classifier.Roots = slices.Clone(sf.Repos)
	}
	answer, err := classifier.ClassifyCLI(ctx, argv, stdin, h.lookupWithClassifier(jail, classifier))
	if err != nil {
		auditReason = "classifier-failed"
		return Verdict{Verdict: "refused", Next: "Check the selected pack's classifier and retry"}, CLIResult{}, err
	}
	if answer.Refused != nil {
		auditReason = "classifier-refused"
		return Verdict{Verdict: "refused", Rule: "classifier refused", Next: answer.Refused.NextStep}, CLIResult{}, nil
	}
	if answer.Local || answer.Classification == nil {
		auditReason = "local-command"
		return Verdict{Verdict: "refused", Next: "Run the local command in the jail or check the selected pack"}, CLIResult{}, nil
	}
	call := *answer.Classification
	if len(call.Writes) != 0 {
		auditSet = "write"
	} else if len(call.Reads) != 0 {
		auditSet = "read"
	} else {
		auditReason = "classification-unknown"
		return Verdict{Verdict: "refused", Next: "Check the selected pack's classifier and retry"}, CLIResult{}, errors.New("call broker classifier returned no operation")
	}
	q := callRequest(h.input.Source, h.workspace, jail, h.start, h.bound.LaunchID, call, stdin)
	if len(call.Writes) != 0 && h.start != "" {
		q.OriginalArgv = slices.Clone(argv)
		q.Policy = oncePolicy
		q.Digest = brokerstore.CommandDigest(q)
	}
	auditDigest = q.Digest
	_, _, working, now, err := h.store.InspectContext(ctx)
	if err != nil {
		auditReason = "decision-store-failed"
		return Verdict{Verdict: "refused", Next: "Check the host broker store and retry"}, CLIResult{}, err
	}
	verdict = decideCall(call, h.input.EffectiveCells, working, h.workspace, h.input.Source, now)
	verdict.Digest = q.Digest
	if len(call.Writes) != 0 && h.start != "" {
		verdict, result, err = h.boundWrite(ctx, op, q, call, verdict, stdin)
		auditReason = "decision-refused"
		if completedWriteResponse(err) {
			auditOutcome, auditReason = "ran", "host-cli-response-rejected"
		} else if op == "evaluate" {
			auditReason = "evaluation-only"
		}
		if op == "ask" && verdict.Verdict == "asks" {
			auditOutcome, auditReason = "pending", "approval-requested"
		}
		if op == "act" && verdict.Verdict == "runs" && err == nil {
			auditOutcome, auditReason = "ran", ""
		}
		return verdict, result, err
	}
	if len(call.Writes) != 0 {
		if verdict.Verdict == "runs" {
			verdict = Verdict{Verdict: "refused", Rule: "pack approval effects are not active", Next: "Wait for pack broker approval policy and host activation before retrying", Digest: q.Digest}
			auditReason = "approval-inactive"
		} else if verdict.Verdict == "asks" {
			verdict.Next = "Pack requests cannot be filed until the approval policy is settled; ask the human to perform this operation on the host"
			auditReason = "approval-inactive"
		} else {
			auditReason = "decision-refused"
		}
	} else if verdict.Verdict != "runs" {
		auditReason = "decision-refused"
	}
	if op == "evaluate" {
		auditReason = "evaluation-only"
		return verdict, CLIResult{}, nil
	}
	if op == "ask" && len(call.Writes) != 0 {
		if verdict.Verdict == "asks" {
			auditReason = "approval-inactive"
		}
		return verdict, CLIResult{}, nil
	}
	if len(call.Writes) != 0 {
		return verdict, CLIResult{}, nil
	}
	if verdict.Verdict != "runs" {
		return verdict, CLIResult{}, nil
	}
	if err := h.live(); err != nil {
		auditReason = selectedInputAuditReason(err)
		return Verdict{Verdict: "refused", Next: "Relaunch the jail"}, CLIResult{}, err
	}
	if h.start != "" {
		if _, _, err := h.currentAuthority(); err != nil {
			return Verdict{Verdict: "refused", Next: err.Error()}, CLIResult{}, err
		}
	}
	result, err = h.program.run(ctx, call, stdin)
	if err != nil {
		if executionCompleted(err) {
			auditOutcome, auditReason = "ran", "host-cli-response-rejected"
		} else {
			auditReason = "host-cli-failed"
		}
		if ctx.Err() != nil {
			auditOutcome = "canceled"
			auditReason = "host-cli-canceled"
		}
		return verdict, CLIResult{}, err
	}
	auditOutcome, auditReason = "ran", ""
	return verdict, result, nil
}

func (h *CLIHandler) lookupWithClassifier(jail string, classifier Classifier) LookupReader {
	return func(ctx context.Context, declared loopholedecl.CallBrokerLookup, args []string) (json.RawMessage, error) {
		outcome, reason := "refused", "request-invalid"
		digest := ""
		defer func() {
			h.audit.Append(brokeraudit.Event{Event: "lookup", Service: h.input.Source, Jail: jail,
				Set: "read", Outcome: outcome, Reason: reason, CommandDigest: digest})
		}()
		if err := h.live(); err != nil {
			reason = selectedInputAuditReason(err)
			return nil, err
		}
		if h.start != "" {
			if _, _, err := h.currentAuthority(); err != nil {
				reason = "read-standing-missing"
				return nil, err
			}
		}
		if len(declared.CLI) == 0 || len(args) != 1 || args[0] == "" || strings.ContainsAny(args[0], "\x00\r\n") {
			return nil, errors.New("lookup arguments are invalid; check the selected pack")
		}
		argv := make([]string, len(declared.CLI))
		used := false
		for i, word := range declared.CLI {
			if word == "{id}" {
				argv[i], used = args[0], true
			} else {
				argv[i] = word
			}
			if strings.Contains(argv[i], "{") {
				return nil, errors.New("lookup template is incomplete; check the selected pack")
			}
		}
		if !used {
			return nil, errors.New("lookup template has no id; check the selected pack")
		}
		answer, err := classifier.ClassifyCLI(ctx, argv, nil, nil)
		if err != nil {
			reason = "classifier-failed"
			return nil, errors.New("lookup did not classify as its exact declared standing read; check the selected pack")
		}
		if answer.Refused != nil {
			reason = "classifier-refused"
			return nil, errors.New("lookup did not classify as its exact declared standing read; check the selected pack")
		}
		if answer.Classification == nil || len(answer.Classification.Writes) != 0 || len(answer.Classification.Reads) == 0 || !reflect.DeepEqual(answer.Classification.Argv, argv) {
			reason = "classifier-failed"
			return nil, errors.New("lookup did not classify as its exact declared standing read; check the selected pack")
		}
		if err := h.live(); err != nil {
			reason = selectedInputAuditReason(err)
			return nil, err
		}
		if h.start != "" {
			if _, _, err := h.currentAuthority(); err != nil {
				reason = "read-standing-missing"
				return nil, err
			}
		}
		digest = callRequest(h.input.Source, h.workspace, jail, "", "", *answer.Classification, nil).Digest
		result, err := h.program.run(ctx, *answer.Classification, nil)
		if err != nil {
			if executionCompleted(err) {
				outcome, reason = "ran", "host-cli-response-rejected"
			} else if ctx.Err() != nil {
				outcome, reason = "canceled", "host-cli-canceled"
			} else {
				reason = "host-cli-failed"
			}
			return nil, errors.New("host lookup failed; check the selected host program and retry")
		}
		outcome = "ran"
		if result.ExitCode != 0 {
			reason = "host-cli-failed"
			return nil, errors.New("host lookup failed; check the selected host program and retry")
		}
		if len(result.Stdout) > classifierLimit || !json.Valid(result.Stdout) {
			reason = "host-cli-response-rejected"
			return nil, errors.New("host lookup returned invalid or oversized JSON; narrow the lookup and retry")
		}
		if err := h.live(); err != nil {
			reason = selectedInputAuditReason(err)
			return nil, err
		}
		reason = ""
		return json.RawMessage(result.Stdout), nil
	}
}

const cliHandlerCallBudget = 30 * time.Second

// Handle is the production hostservice handler, not a test-only dispatcher.
// Only a fronted, host-identified connection reaches its operation boundary.
func (h *CLIHandler) Handle(s *hostservice.Session) {
	if !s.Fronted || s.JailID == "" || s.JailID == "unknown" {
		s.Stderr("Call broker requires the authenticated launch front; relaunch the jail.\n")
		s.Exit(64)
		return
	}
	allowed := map[string]bool{"op": true, "argv": true, "stdin": true}
	for _, key := range s.Request.Keys() {
		if !allowed[key] {
			s.Stderr("Unsupported call broker field; update the client and retry.\n")
			s.Exit(64)
			return
		}
	}
	op, ok := s.Get("op")
	if !ok {
		s.Stderr("Missing operation; update the client and retry.\n")
		s.Exit(64)
		return
	}
	mode, ok := op.(string)
	if !ok {
		s.Stderr("Invalid operation; update the client and retry.\n")
		s.Exit(64)
		return
	}
	var argv []string
	raw, ok := s.Get("argv")
	if !ok {
		s.Stderr("Missing argv; update the client and retry.\n")
		s.Exit(64)
		return
	}
	list, ok := raw.([]any)
	if !ok || len(list) == 0 {
		s.Stderr("Invalid argv; update the client and retry.\n")
		s.Exit(64)
		return
	}
	for _, item := range list {
		word, ok := item.(string)
		if !ok {
			s.Stderr("Invalid argv; update the client and retry.\n")
			s.Exit(64)
			return
		}
		argv = append(argv, word)
	}
	var stdin []byte
	if encoded, has := s.Get("stdin"); has {
		value, ok := encoded.(string)
		if !ok || len(value) > 4<<20 {
			s.Stderr("Invalid stdin; narrow the request and retry.\n")
			s.Exit(64)
			return
		}
		var err error
		stdin, err = base64.StdEncoding.DecodeString(value)
		if err != nil {
			s.Stderr("Invalid stdin; retry with base64 bytes.\n")
			s.Exit(64)
			return
		}
	}
	ctx, cancel := context.WithTimeout(h.shutdown, cliHandlerCallBudget)
	defer cancel()
	verdict, result, err := h.serveCLI(ctx, mode, s.JailID, argv, stdin)
	if err != nil {
		if completedWriteResponse(err) {
			s.Stderr("Call broker host CLI response rejected after execution: " + verdict.Next + "\n")
			s.Exit(64)
			return
		}
		next := verdict.Next
		if next == "" {
			next = "Check the host broker and retry"
		}
		s.Stderr("Call broker refused: " + next + "\n")
		s.Exit(64)
		return
	}
	if mode == "evaluate" || mode == "ask" {
		data, err := json.Marshal(verdict)
		if err != nil {
			s.Stderr("Cannot encode verdict; check the selected pack and retry.\n")
			s.Exit(64)
			return
		}
		s.Stdout(string(data) + "\n")
		s.Exit(0)
		return
	}
	if verdict.Verdict != "runs" {
		s.Stderr("Call broker " + verdict.Verdict + ": " + verdict.Next + "\n")
		if verdict.Verdict == "asks" {
			s.Exit(75)
		} else {
			s.Exit(64)
		}
		return
	}
	s.StdoutBytes(result.Stdout)
	s.Stderr(string(result.Stderr))
	s.Exit(result.ExitCode)
}
