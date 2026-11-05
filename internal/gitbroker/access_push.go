package gitbroker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokeraudit"
	"github.com/mschulkind-oss/yolo-jail/internal/brokercore"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/gitproto"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

type gitPushNext struct {
	Who    string `json:"who"`
	Where  string `json:"where"`
	Run    string `json:"run,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// parsePushAdvertisementCaps validates the advertisement fields that affect push
// interpretation. Git defaults to SHA-1 when object-format is absent; unlike Caps.Has,
// push-options is negotiated only by its exact bare token.
func parsePushAdvertisementCaps(raw []byte) (gitproto.Caps, int, error) {
	caps := gitproto.Caps{}
	seenNames := map[string]bool{}
	objectFormat := "sha1"
	objectFormatSeen := false
	for _, b := range raw {
		if b < 0x20 && b != '\t' && b != '\n' && b != '\r' || b > 0x7e {
			return nil, 0, errors.New("malformed remote capabilities; push from the host")
		}
	}
	for _, token := range strings.FieldsFunc(string(raw), func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '\r'
	}) {
		if caps[token] {
			return nil, 0, errors.New("duplicate remote capability; push from the host")
		}
		caps[token] = true
		name, value, hasValue := strings.Cut(token, "=")
		if name == "" {
			return nil, 0, errors.New("malformed remote capability; push from the host")
		}
		for _, c := range name {
			if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' {
				return nil, 0, errors.New("malformed remote capability; push from the host")
			}
		}
		if seenNames[name] {
			return nil, 0, errors.New("duplicate remote capability; push from the host")
		}
		seenNames[name] = true
		if name != "object-format" {
			continue
		}
		if !hasValue || value == "" || objectFormatSeen {
			return nil, 0, errors.New("malformed or duplicate object-format capability; push from the host")
		}
		objectFormatSeen = true
		switch value {
		case "sha1", "sha256":
			objectFormat = value
		default:
			return nil, 0, errors.New("unsupported remote object format; push from the host")
		}
	}
	width := 40
	if objectFormat == "sha256" {
		width = 64
	}
	return caps, width, nil
}

// verifyProposedPush reads exactly one v0 receive-pack advertisement and binds every
// selected ref's old ID to the host's observation. No ref command or pack is sent upstream.
func verifyProposedPush(cmds []gitproto.Command, opts []string, advertisement io.Reader) (string, error) {
	if err := validateProposedPush(cmds, opts); err != nil {
		return "", err
	}
	refs := map[string]string{}
	seen := map[string]bool{}
	width := 0
	r := gitproto.NewReader(io.LimitReader(advertisement, int64(maxAdvertisement)+1))
	first := true
	capabilities := gitproto.Caps{}
	objectWidth := 40
	used := 0
	for {
		p, err := r.Read()
		if err != nil {
			return "", fmt.Errorf("remote advertisement incomplete: %w; retry the preflight", err)
		}
		used += len(p.Data) + 4
		if used > maxAdvertisement {
			return "", errors.New("remote advertisement too large; push from the host")
		}
		if p.Kind == gitproto.Flush {
			break
		}
		if p.Kind != gitproto.Data || len(p.Data) == 0 {
			return "", errors.New("unsupported remote advertisement; push from the host")
		}
		line := bytes.TrimSuffix(p.Data, []byte{'\n'})
		if first {
			var capLine []byte
			var hasCaps bool
			line, capLine, hasCaps = bytes.Cut(line, []byte{0})
			if !hasCaps || bytes.ContainsRune(capLine, 0) {
				return "", errors.New("malformed remote capabilities; push from the host")
			}
			var capErr error
			capabilities, objectWidth, capErr = parsePushAdvertisementCaps(capLine)
			if capErr != nil {
				return "", capErr
			}
			first = false
		} else if bytes.ContainsRune(line, 0) {
			return "", errors.New("malformed remote advertisement; retry the preflight")
		}
		parts := bytes.Split(line, []byte{' '})
		if len(parts) != 2 || !objectID(string(parts[0])) || len(parts[1]) == 0 {
			return "", errors.New("malformed remote advertisement; retry the preflight")
		}
		id, ref := string(parts[0]), string(parts[1])
		if len(id) != objectWidth {
			return "", errors.New("remote object IDs do not match its advertised object format; push from the host")
		}
		if width != 0 && width != len(id) {
			return "", errors.New("mixed remote object ID widths; push from the host")
		}
		width = len(id)
		if ref == "capabilities^{}" && strings.Trim(id, "0") == "" {
			continue
		}
		for _, ch := range ref {
			if ch <= ' ' || ch == 0x7f {
				return "", errors.New("malformed remote ref advertisement; retry the preflight")
			}
		}
		if seen[ref] || !strings.HasPrefix(ref, "refs/") {
			return "", errors.New("ambiguous remote ref advertisement; retry the preflight")
		}
		seen[ref] = true
		refs[ref] = id
	}
	if width == 0 {
		return "", errors.New("remote advertised no object format; push from the host")
	}
	if len(opts) > 0 && !capabilities["push-options"] {
		return "", errors.New("remote does not advertise push-options; retry without push options or push from the host")
	}
	selected := map[string]bool{}
	for _, c := range cmds {
		if selected[c.Ref] {
			return "", errors.New("duplicate proposed ref; use one explicit refspec per destination")
		}
		selected[c.Ref] = true
		if len(c.Old) != width || len(c.New) != width {
			return "", errors.New("candidate and remote object ID widths differ; retry the preflight")
		}
		current := refs[c.Ref]
		if current == "" {
			current = strings.Repeat("0", width)
		}
		if current != c.Old {
			return "", fmt.Errorf("remote ref %s changed since preflight; rerun yolo access with a fresh git push proposal", c.Ref)
		}
		if c.New == current {
			return "", fmt.Errorf("remote ref %s is already up to date; rerun git push preflight", c.Ref)
		}
	}
	return widthName(width), nil
}
func validateProposedPush(cmds []gitproto.Command, opts []string) error {
	if len(cmds) == 0 {
		return errors.New("no selected ref commands; run git push with explicit refs to ask the broker")
	}
	if err := checkPushArgv(pushArgv(cmds, opts)); err != nil {
		return fmt.Errorf("invalid candidate: %w; run git push with explicit refs", err)
	}
	seenCandidate := map[string]bool{}
	for _, c := range cmds {
		if len(c.Old) != len(c.New) {
			return errors.New("candidate object ID widths differ; rerun git push preflight")
		}
		if seenCandidate[c.Ref] {
			return errors.New("duplicate proposed ref; use one explicit refspec per destination")
		}
		seenCandidate[c.Ref] = true
		if !validProposedRef(c.Ref) {
			return errors.New("malformed proposed ref; use a valid Git refspec")
		}
		if c.Create() && c.Delete() {
			return errors.New("proposed ref has no object to create or delete; rerun git push preflight")
		}
	}
	return nil
}

// validProposedRef checks Git's full destination ref grammar before any host remote leg.
func validProposedRef(ref string) bool {
	if !strings.HasPrefix(ref, "refs/heads/") && !strings.HasPrefix(ref, "refs/tags/") {
		return false
	}
	if strings.HasSuffix(ref, "/") || strings.HasSuffix(ref, ".") || strings.Contains(ref, "..") || strings.Contains(ref, "@{") {
		return false
	}
	for _, component := range strings.Split(ref, "/") {
		if component == "" || strings.HasPrefix(component, ".") || strings.HasSuffix(component, ".lock") {
			return false
		}
		for _, ch := range component {
			if ch <= ' ' || ch == 0x7f || strings.ContainsRune("~^:?*[\\", ch) {
				return false
			}
		}
	}
	return true
}

func widthName(width int) string {
	if width == 64 {
		return "sha256"
	}
	return "sha1"
}

// freshPushAdvertisement opens only the approved remote's receive-pack read leg. HTTPS
// issues GET info/refs; SSH sends a flush after the advertisement and no update commands.
func (b *Broker) freshPushAdvertisement(ctx context.Context, remote string, say func(string)) ([]byte, error) {
	transport, err := transportOf(remote)
	if err != nil {
		return nil, err
	}
	if transport == transportHTTPS {
		ev := brokeraudit.Event{Event: "evaluate", Service: Source, Workspace: b.bound.Workspace, Remote: remote}
		h, err := b.newHTTPSLeg(ctx, remote, serviceReceivePack, &ev, say)
		if err != nil {
			return nil, err
		}
		h.readOnly = true
		h.credentialPolicy = func() (bool, string, error) {
			f, err := brokercore.Live(Source, b.scopeFile, b.bound, b.start)
			if err != nil {
				return false, "", err
			}
			repo, _ := gitproto.Identity(remote)
			if !scopeContains(f, remote) || !brokerscope.ReadCovers(f.Grants, repo, func(a, b string) bool { return a == b }) {
				return false, "remote no longer approved", nil
			}
			return true, "", brokerscope.ValidateCurrentWithMatcher(b.scopeFile, b.bound, remote, equivalentEndpoint)
		}
		resp, code := h.advertisement()
		if code != 0 {
			return nil, fmt.Errorf("receive-pack advertisement GET failed (%d); check the remote from the host", code)
		}
		defer drainClose(resp)
		r := gitproto.NewReader(resp.Body)
		first, err := skipServiceLine(r)
		if err != nil {
			return nil, err
		}
		var out bytes.Buffer
		w := gitproto.NewWriter(&out)
		if first.Kind == gitproto.Flush {
			return nil, errors.New("remote advertised no object format; push from the host")
		}
		if first.Kind != gitproto.Data {
			return nil, errors.New("unsupported remote advertisement; push from the host")
		}
		if err = w.Data(first.Data); err != nil {
			return nil, err
		}
		r.Tee(&out)
		if err = readToFlush(r, out.Len()); err != nil {
			return nil, err
		}
		return out.Bytes(), nil
	}
	argv, err := sshArgv(remote, serviceReceivePack, "")
	if err != nil {
		return nil, err
	}
	ssh, err := lookPath("ssh", b.getenv("PATH"))
	if err != nil {
		return nil, err
	}
	if why := brokercore.PlacementRefusal("ssh", b.bound.Workspace, b.spawnCwd)(ssh); why != "" {
		return nil, errors.New(why)
	}
	u, err := startUpstream(ctx, ssh, argv, sshEnv(b.getenv, ""), paths.BrokerSourceDir(Source), io.Discard)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	r := gitproto.NewReader(u.stdout)
	r.Tee(&out)
	for out.Len() <= maxAdvertisement {
		p, err := r.Read()
		if err != nil {
			_ = u.stdin.Close()
			u.wait()
			return nil, fmt.Errorf("receive-pack advertisement incomplete: %w", err)
		}
		if p.Kind == gitproto.Flush {
			break
		}
	}
	if out.Len() > maxAdvertisement {
		_ = u.stdin.Close()
		u.wait()
		return nil, errors.New("remote advertisement too large")
	}
	_, _ = io.WriteString(u.stdin, "0000")
	_ = u.stdin.Close()
	_, _ = io.Copy(io.Discard, u.stdout)
	if code := u.wait(); code != 0 {
		return nil, fmt.Errorf("receive-pack read leg exited %d; check the remote from the host", code)
	}
	return out.Bytes(), nil
}

func lapsedPushNext(id string) []gitPushNext {
	return []gitPushNext{
		{Who: "human", Where: "host", Detail: "Confirm request " + termsafe.Visible(id) + " is still wanted before retrying."},
		{Who: "agent", Where: "jail", Detail: "Run the same push once to record the expired Allow once refusal; it may contact the remote, but sends no ref updates upstream."},
		{Who: "agent", Where: "jail", Detail: "Then run the same push again to file a new approval request."},
	}
}

func uniformCoveredBy(refs []brokeraudit.RefUpdate) string {
	if len(refs) == 0 {
		return ""
	}
	by := refs[0].CoveredBy
	for _, ref := range refs[1:] {
		if ref.CoveredBy != by {
			return ""
		}
	}
	return by
}

func (b *Broker) evaluatePush(ctx context.Context, jail, op string, p gitproto.PushProposal) []byte {
	answer := map[string]any{"schema": "yolo.access.prediction/1", "source": Source, "source_status": "not-read", "verdict": "refused", "rule": "candidate not verified; retry with an explicit refspec or push from the host", "next": []gitPushNext{{Who: "agent", Where: "jail", Detail: "Retry git push with an explicit source:refs/heads/name through the yolo helper."}}}
	evidence := []any{p.Service, p.Commands, p.PushOptions}
	finish := func() []byte {
		serialized, _ := json.Marshal(append(evidence, answer["rule"], answer["verdict"], answer["classified"], answer["covered_by"], answer["relies_on_until"]))
		sum := sha256.Sum256(serialized)
		answer["inputs_digest"] = hex.EncodeToString(sum[:])
		if _, ok := answer["call"]; !ok {
			answer["call_unavailable"] = "no verified Git command; retry the preflight"
		}
		data, _ := json.Marshal(answer)
		return append(data, '\n')
	}
	if jail != paths.HostServicesDirName(holderJail(b.bound)) && !(strings.HasPrefix(jail, paths.HostServicesSessionPrefix(b.bound.Container)) && paths.IsHostServicesSessionDirName(jail)) {
		return finish()
	}
	if p.Service != serviceReceivePack || p.URL == "" {
		return finish()
	}
	f, err := brokercore.Live(Source, b.scopeFile, b.bound, b.start)
	if err != nil || f.Form != brokerscope.FormRemoteURL {
		return finish()
	}
	repo, err := gitproto.Identity(p.URL)
	if err != nil || !scopeContains(f, p.URL) {
		answer["rule"] = "remote is outside the approved scope; use the exact approved Git remote or push from the host"
		return finish()
	}
	answer["scope_source"] = "launch"
	if f.LiveScope {
		answer["scope_source"] = "live-host"
	}
	evidence = append(evidence, f.Grants, f.LiveScope, repo)
	if !brokerscope.HoldsRead(f.Grants) || !brokerscope.ReadCovers(f.Grants, repo, func(a, b string) bool { return a == b }) {
		answer["rule"] = config.BrokerNoReadRefusal(Source, ReadCapability, repo, f.LiveScope)
		return finish()
	}
	if err = brokerscope.ValidateCurrentWithMatcher(b.scopeFile, b.bound, p.URL, equivalentEndpoint); err != nil {
		answer["rule"] = "approved remote changed; run yolo check on the host, then retry the preflight"
		return finish()
	}
	answer["scope_verified"] = true
	if err := validateProposedPush(p.Commands, p.PushOptions); err != nil {
		answer["rule"] = err.Error()
		return finish()
	}
	say := func(string) {}
	adv, err := b.freshPushAdvertisement(ctx, p.URL, say)
	if err != nil {
		answer["source_status"] = "unavailable"
		answer["source_error"] = "receive-pack advertisement unavailable; check the remote from the host"
		answer["rule"] = "cannot read the remote's current refs; check the remote from the host, then retry"
		return finish()
	}
	_, err = verifyProposedPush(p.Commands, p.PushOptions, bytes.NewReader(adv))
	evidence = append(evidence, adv)
	if err != nil {
		answer["source_status"] = "unavailable"
		answer["source_error"] = "receive-pack advertisement or ref verification failed; check the remote from the host"
		answer["rule"] = termsafe.Visible(err.Error())
		return finish()
	}
	answer["source_status"] = "verified"
	q := b.pushRequest(repo, p.Commands, p.PushOptions)
	answer["command_digest"] = q.Digest
	answer["call"] = map[string]any{"source": Source, "canonical": q.Argv, "digest": q.Digest}
	inspected, err := b.store.InspectExactGrantContext(ctx, q, b.revalidate)
	evidence = append(evidence, inspected.Now, inspected.Eligible, inspected.LapsedRequest, inspected.EligibilityErr == nil)
	for _, entry := range inspected.Working {
		if brokerstore.SameWorkspace(entry.Workspace, b.bound.Workspace) && entry.Repo == repo && entry.LiveAt(inspected.Now) {
			evidence = append(evidence, entry.ID, entry.Kind, entry.Resource, entry.Expires)
		}
	}
	for _, prior := range inspected.Requests {
		if prior.Jail == q.Jail && brokerstore.SameWorkspace(prior.Workspace, b.bound.Workspace) {
			evidence = append(evidence, prior.Digest, prior.State, prior.Decided, prior.Expires(), prior.Lapsed)
		}
	}
	if err != nil || inspected.EligibilityErr != nil {
		answer["eligibility_status"] = "unavailable"
		answer["rule"] = "host grant store cannot be inspected; run yolo approve on the host to diagnose it"
		return finish()
	}
	answer["eligibility_status"] = "not-covered"
	if inspected.Eligible {
		answer["eligibility_status"] = "exact-grant"
	}
	menu := pushMenuFrom(ctx)
	cover := b.coverForSnapshot(f, repo, say, menu, inspected.Working, inspected.Now)
	refs, reasons, _ := judge(p.Commands, cover)
	answer["classified"] = refs
	// A working-set entry or exact grant can lapse without the proposal changing.
	// This is a latest bound, not a reservation; mutable policy can change sooner.
	for _, ref := range refs {
		for _, entry := range inspected.Working {
			if ref.CoveredBy == entry.ID && !entry.Expires.IsZero() {
				if until, ok := answer["relies_on_until"].(time.Time); !ok || entry.Expires.Before(until) {
					answer["relies_on_until"] = entry.Expires
				}
			}
		}
	}
	if inspected.Eligible && !inspected.Grant.Expires.IsZero() {
		answer["relies_on_until"] = inspected.Grant.Expires
	}
	needs := approvable(p.Commands, reasons)
	for _, reason := range reasons {
		if reason == reasonOutsideRefs {
			answer["rule"] = "a ref outside approved heads or tags cannot be requested; push it from the host"
			answer["next"] = []gitPushNext{{Who: "human", Where: "host", Detail: "Push this ref from the host if still wanted."}}
			return finish()
		}
	}
	uncovered := len(needs) > 0
	if inspected.Eligible {
		uncovered = false
		answer["covered_by"] = inspected.Grant.ID
		for i := range refs {
			refs[i].CoveredBy = inspected.Grant.ID
		}
		answer["classified"] = refs
	}
	if !uncovered {
		answer["coverage_status"] = "covered"
		answer["verdict"] = "runs"
		answer["rule"] = "the host currently covers every ref; the remote may still reject it"
		if !inspected.Eligible && len(refs) > 0 {
			if by := uniformCoveredBy(refs); by != "" {
				answer["covered_by"] = by
			}
		}
		answer["next"] = []gitPushNext{}
		return finish()
	}
	answer["verdict"] = "asks"
	answer["coverage_status"] = "needs-approval"
	answer["rule"] = "one or more refs need approval"
	answer["next"] = []gitPushNext{{Who: "agent", Where: "jail", Detail: "Run `yolo access --ask --` followed by this exact git push argv to file approval without pushing."}, {Who: "human", Where: "host", Detail: "After filing, run yolo approve with the returned request ID."}}
	// A duplicate joins before the store applies its cap/cooldown. Use the same
	// snapshot clock as exact-grant and working-set inspection.
	pending := 0
	duplicatePending := false
	for _, prior := range inspected.Requests {
		if prior.Jail != q.Jail {
			continue
		}
		state, decided := prior.State, prior.Decided
		if state == "pending" && !inspected.Now.Before(prior.Expires()) {
			state, decided = "expired", prior.Expires()
		}
		if state == "pending" {
			pending++
			if prior.Digest == q.Digest && prior.Start == q.Start && prior.Set == q.Set {
				duplicatePending = true
				if op != "ask" {
					answer["request_id"] = prior.ID
					answer["next"] = []gitPushNext{{Who: "agent", Where: "jail", Run: "yolo wait " + prior.ID}, {Who: "human", Where: "host", Run: "yolo approve " + prior.ID}}
					return finish()
				}
			}
		}
		if !duplicatePending && inspected.LapsedRequest == "" && prior.Digest == q.Digest && (state == "denied" || state == "expired") && inspected.Now.Before(decided.Add(10*time.Minute)) {
			answer["verdict"] = "refused"
			answer["rule"] = filingRefusal(&brokerstore.DenialCooldownError{Request: prior.ID, Until: decided.Add(10 * time.Minute)}).next
			answer["next"] = []gitPushNext{{Who: "human", Where: "host", Detail: "Review the denied request with yolo approve; push from the host if still wanted."}}
			return finish()
		}
	}
	if inspected.LapsedRequest != "" && !duplicatePending {
		answer["verdict"] = "refused"
		answer["rule"] = filingRefusal(&brokerstore.LapsedAllowError{Request: inspected.LapsedRequest}).reason
		answer["next"] = lapsedPushNext(inspected.LapsedRequest)
		return finish()
	}
	if !duplicatePending && pending >= 3 {
		answer["verdict"] = "refused"
		answer["rule"] = "this jail has three pending requests; ask the human to run yolo approve on the host, then retry"
		answer["next"] = []gitPushNext{{Who: "human", Where: "host", Detail: "Review pending requests with yolo approve, then retry."}}
		return finish()
	}
	if op != "ask" {
		return finish()
	}
	q.NeedsApproval = needs
	q.About = pushAbout(repo, needs)
	gate := func(parent brokerstore.Request) (brokerstore.Policy, error) {
		if err := ctx.Err(); err != nil {
			return brokerstore.Policy{}, err
		}
		if !brokerstore.SameWorkspace(parent.Workspace, q.Workspace) {
			return brokerstore.Policy{}, errors.New("another workspace's grant")
		}
		held := parent
		held.Jail, held.Start, held.LaunchID, held.Workspace = q.Jail, q.Start, q.LaunchID, q.Workspace
		if _, err := b.revalidate(held); err != nil {
			return brokerstore.Policy{}, err
		}
		return b.revalidate(q)
	}
	coverageBy := ""
	var lateBound time.Time
	lateRefs := append([]brokeraudit.RefUpdate(nil), refs...)
	coverage := func(working []brokerstore.WorkingEntry, at time.Time) (bool, error) {
		live, err := brokercore.Live(Source, b.scopeFile, b.bound, b.start)
		if err != nil {
			return false, err
		}
		if !scopeContains(live, p.URL) {
			return false, errors.New("remote is no longer approved; rerun the preflight")
		}
		if err := brokerscope.ValidateCurrentWithMatcher(b.scopeFile, b.bound, p.URL, equivalentEndpoint); err != nil {
			return false, err
		}
		current := b.coverForSnapshot(live, repo, func(string) {}, pushMenuFrom(ctx), working, at)
		lateRefs, reasons, _ = judge(p.Commands, current)
		for _, reason := range reasons {
			if reason != "" {
				return false, nil
			}
		}
		coverageBy = uniformCoveredBy(lateRefs)
		lateBound = time.Time{}
		for _, ref := range lateRefs {
			for _, entry := range working {
				if ref.CoveredBy == entry.ID && entry.LiveAt(at) && !entry.Expires.IsZero() && (lateBound.IsZero() || entry.Expires.Before(lateBound)) {
					lateBound = entry.Expires
				}
			}
		}
		return true, nil
	}
	filed, grant, covered, err := b.store.FileIfStillAsksWithCoverageContext(ctx, q, gate, coverage)
	if len(lateRefs) > 0 {
		answer["classified"] = lateRefs
	}
	if err != nil {
		answer["verdict"] = "refused"
		answer["rule"] = filingRefusal(err).next
		if id, ok := brokerstore.IsLapsedAllow(err); ok {
			answer["rule"] = filingRefusal(err).reason
			answer["next"] = lapsedPushNext(id)
		} else {
			answer["next"] = []gitPushNext{{Who: "human", Where: "host", Detail: "Inspect pending requests with yolo approve, then retry."}}
		}
		return finish()
	}
	if covered {
		answer["coverage_status"] = "covered"
		answer["verdict"] = "runs"
		answer["eligibility_status"] = "not-covered"
		delete(answer, "relies_on_until")
		if !lateBound.IsZero() {
			answer["relies_on_until"] = lateBound
		}
		if grant.ID != "" {
			answer["eligibility_status"] = "exact-grant"
			answer["relies_on_until"] = grant.Expires
			coverageBy = grant.ID
			for i := range lateRefs {
				lateRefs[i].CoveredBy = grant.ID
			}
			if len(lateRefs) > 0 {
				answer["classified"] = lateRefs
			}
		}
		if coverageBy != "" {
			answer["covered_by"] = coverageBy
		} else {
			delete(answer, "covered_by")
		}
		answer["rule"] = "the host now covers every ref; the remote may still reject it"
		answer["next"] = []gitPushNext{}
		return finish()
	}
	answer["request_id"] = filed.ID
	answer["next"] = []gitPushNext{{Who: "agent", Where: "jail", Run: "yolo wait " + filed.ID}, {Who: "human", Where: "host", Run: "yolo approve " + filed.ID}}
	return finish()
}
