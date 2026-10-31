package ghbroker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
)

// dispatch.go is the `dispatch-workflow` user grant (docs/design/github-write-scopes.md GW-D14):
// a grant naming a workflow file, or `*`, on a repository lets the broker run that workflow
// unasked, but only on the repository's DEFAULT branch. A dispatch runs the workflow file as it
// exists on the ref it names, with the repository's secrets, so a jail able to push a branch
// could otherwise push a changed workflow and dispatch it there. Any other ref, a workflow named
// by id or display name, and every other workflow command (run rerun, run cancel, workflow
// enable) are not covered: they fall through to a timed grant or Allow once, as any
// repository-level write does. The workflow's inputs pass through unchanged.

// GrantDispatchWorkflow is the user grant kind for running a workflow, as packs/github declares
// it; a covered run is audited under the set of the same name, as a creation is under its kind.
const GrantDispatchWorkflow = brokerscope.KindDispatchWorkflow

// workflowDispatch is one workflow run a canonical argv asks for.
type workflowDispatch struct {
	Workflow string // the workflow's file name, as the argv names it
	Ref      string // the ref the argv names; "" when it names none, and gh takes the default branch
}

// dispatchOf is the workflow run the canonical argv asks for, ok false for anything else: `gh
// workflow run <file>` with at most one --ref, or `gh api` POST of
// repos/{o}/{r}/actions/workflows/{file}/dispatches with exactly one `ref` field and no --input.
// A workflow named other than by a file name (a numeric id, a display name) is not one.
func dispatchOf(argv []string, t writeTarget) (workflowDispatch, bool) {
	if t.Kind != targetRepo || t.Repo == "" {
		return workflowDispatch{}, false
	}
	p, err := parseArgv(argv)
	if err != nil || p.cmd == nil {
		return workflowDispatch{}, false
	}
	var w workflowDispatch
	switch p.cmd.path {
	case "workflow run":
		refs := p.values("ref")
		if len(p.positionals) != 1 || len(refs) > 1 {
			return workflowDispatch{}, false
		}
		w.Workflow = p.positionals[0]
		if len(refs) == 1 {
			if refs[0] == "" {
				return workflowDispatch{}, false
			}
			w.Ref = refs[0]
		}
	case "api":
		var ok bool
		if w, ok = apiDispatch(p, t.Repo); !ok {
			return workflowDispatch{}, false
		}
	default:
		return workflowDispatch{}, false
	}
	if w.Workflow == brokerscope.AnyWorkflow || !brokerscope.ValidGrantWorkflow(w.Workflow) {
		return workflowDispatch{}, false
	}
	return w, true
}

// apiDispatch reads a `gh api` workflow dispatch: POST (stated, or implied by its fields) to
// repos/{o}/{r}/actions/workflows/{file}/dispatches on repo, written plainly, with no --input
// (a body read from it could carry another ref unseen) and exactly one field, -f or -F, whose
// key is `ref` (none rooted at it otherwise, such as `ref[]` or `ref]`). An endpoint carrying a
// query or a fragment is never one: gh sends the query unchanged, and GitHub could read a ref
// from it the broker never checked.
func apiDispatch(p *parsed, repo string) (workflowDispatch, bool) {
	if len(p.positionals) != 1 || p.has("input") || strings.ContainsAny(p.positionals[0], "?#") {
		return workflowDispatch{}, false
	}
	path, why := apiEndpointPath(p.positionals[0])
	if why != "" {
		return workflowDispatch{}, false
	}
	m := apiRepoPathRE.FindStringSubmatch(path)
	if m == nil || !strings.EqualFold(m[1]+"/"+m[2], repo) {
		return workflowDispatch{}, false
	}
	rest := strings.Split(strings.Trim(m[3], "/"), "/")
	for _, seg := range rest {
		if d, err := url.PathUnescape(seg); err != nil || d != seg {
			return workflowDispatch{}, false
		}
	}
	if len(rest) != 4 || rest[0] != "actions" || rest[1] != "workflows" || rest[3] != "dispatches" {
		return workflowDispatch{}, false
	}
	method := "POST"
	if vals := p.values("method"); len(vals) > 0 {
		method = strings.ToUpper(vals[len(vals)-1])
	} else if !p.has("field") && !p.has("raw-field") {
		method = "GET"
	}
	if method != "POST" {
		return workflowDispatch{}, false
	}
	var refs []string
	for _, long := range []string{"field", "raw-field"} {
		for _, v := range p.values(long) {
			k, val, _ := strings.Cut(v, "=")
			if strings.EqualFold(fieldRoot(k), "ref") {
				if k != "ref" {
					return workflowDispatch{}, false
				}
				refs = append(refs, val)
			}
		}
	}
	if len(refs) != 1 || refs[0] == "" {
		return workflowDispatch{}, false
	}
	return workflowDispatch{Workflow: rest[2], Ref: refs[0]}, true
}

// fieldRoot is a `gh api` field key's root as gh's own field parsing could read it: the key up
// to its first `[` or `]`. A key such as `ref]` or `ref[x]` is rooted at `ref` without being it.
func fieldRoot(k string) string {
	if i := strings.IndexAny(k, "[]"); i >= 0 {
		return k[:i]
	}
	return k
}

// pinnedDispatchArgv is the canonical argv with its ref written as the full branch ref,
// refs/heads/<def>: `--ref=refs/heads/<def>` for `gh workflow run` (added when the argv names
// none), and the `ref` field's value for `gh api`. What runs then names exactly the branch the
// broker read as the default, so neither gh's own default-branch lookup nor a tag of the same
// name can change it. ok false when the argv is not a dispatch dispatchOf reads.
func pinnedDispatchArgv(argv []string, t writeTarget, def string) ([]string, bool) {
	if _, ok := dispatchOf(argv, t); !ok {
		return nil, false
	}
	p, err := parseArgv(argv)
	if err != nil || p.cmd == nil {
		return nil, false
	}
	full := "refs/heads/" + def
	var flags []flagUse
	switch p.cmd.path {
	case "workflow run":
		refFlag := p.cmd.flagByLong("ref")
		if refFlag == nil {
			return nil, false
		}
		for _, u := range p.flags {
			if u.flag.long != "ref" {
				flags = append(flags, u)
			}
		}
		flags = append(flags, flagUse{flag: refFlag, value: full, hasValue: true})
	case "api":
		for _, u := range p.flags {
			if (u.flag.long == "field" || u.flag.long == "raw-field") && u.hasValue {
				if k, _, _ := strings.Cut(u.value, "="); k == "ref" {
					u.value = "ref=" + full
				}
			}
			flags = append(flags, u)
		}
	default:
		return nil, false
	}
	p.flags = flags
	return p.canonical(), true
}

// dispatchGrantHeld reports whether grants hold a dispatch-workflow grant covering w on repo:
// on the repository or `*`, naming w's file or `*`.
func dispatchGrantHeld(grants []brokerscope.Grant, repo string, w workflowDispatch) bool {
	for _, g := range grants {
		if g.Kind == GrantDispatchWorkflow && (g.Repo == brokerscope.AnyRepo || sameRepo(g.Repo, repo)) &&
			(g.Workflow == brokerscope.AnyWorkflow || g.Workflow == w.Workflow) {
			return true
		}
	}
	return false
}

// onDefaultBranch reports whether the dispatch w runs on repo's default branch, read by the
// broker's own host read of `repos/{o}/{r}`, and returns that branch's name. A ref is the
// default branch when it is omitted (gh would send the default branch), is its name, or is
// refs/heads/<name>. A covered run is then sent as refs/heads/<name> (pinnedDispatchArgv), so a
// tag of the same name cannot shadow the branch the broker checked. Any failed or unreadable
// read is an error, and the dispatch is not covered.
func (b *Broker) onDefaultBranch(ctx context.Context, repo string, w workflowDispatch) (string, bool, error) {
	var repoObj struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := b.hostRead(ctx, "repos/"+repo, &repoObj); err != nil {
		return "", false, err
	}
	def := repoObj.DefaultBranch
	if !plainBranchName(def) {
		return "", false, fmt.Errorf("the default branch's name %q is not one the broker reads plainly", def)
	}
	switch w.Ref {
	case "", def, "refs/heads/" + def:
		return def, true, nil
	}
	return def, false, nil
}

// hostRead is one read the broker makes on its own account through the host gh, `gh api
// <path>`, decoded into into. A non-zero exit or an answer that does not decode is an error.
func (b *Broker) hostRead(ctx context.Context, path string, into any) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var out bytes.Buffer
	argv := []string{"api", path}
	sink := func(p []byte) {
		if out.Len() < 1<<20 {
			out.Write(p)
		}
	}
	var res RunResult
	switch {
	case b.execute != nil:
		res = b.execute(ctx, argv, nil, sink, func([]byte) {})
	case b.runner != nil:
		res = b.runner.RunContext(ctx, argv, nil, sink, func([]byte) {})
	default:
		return errors.New("no host gh")
	}
	if res.Exit != 0 {
		return fmt.Errorf("gh api exited %d", res.Exit)
	}
	if err := json.Unmarshal(out.Bytes(), into); err != nil {
		return errors.New("unreadable answer")
	}
	return nil
}

// plainBranchName reports whether s is a branch name made only of letters, digits, `.`, `_`,
// `-` and `/`, which a REST path carries as written. Any other default branch name is not
// read, and a dispatch on it asks.
func plainBranchName(s string) bool {
	if s == "" || len(s) > 255 {
		return false
	}
	for _, c := range []byte(s) {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-' || c == '/') {
			return false
		}
	}
	return true
}

// dispatchHint is the clause a pending request for a workflow run adds: the lasting grant that
// would let it run unasked on the default branch. "" for any other write.
func dispatchHint(argv []string, t writeTarget) string {
	w, ok := dispatchOf(argv, t)
	if !ok {
		return ""
	}
	return fmt.Sprintf(" A lasting right to run %s on %s's default branch is a dispatch-workflow grant "+
		"(`yolo request github dispatch_workflows %s`, then `yolo config apply` on the host); a run on any other ref still asks.",
		w.Workflow, t.Repo, w.Workflow)
}
