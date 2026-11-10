// Package approvalfixtures is the shared fixture set of the approval-experience work
// (docs/design/approval-experience/native-notification-gallery.md): sanitized representative
// approval requests, each labeled with where it came from and what evidence exists for how it
// renders. The native notification gallery, the browser request queue and service tests read
// the same files.
//
// One fixture is one JSON file in fixtures/, named after its id. These doc comments are the
// format's reference; Validate enforces every rule they state, and the package's tests run it
// over every file, so a fixture that breaks a rule fails `go test`.
//
// The labels a fixture carries answer the umbrella's rule 5 (one sanitized fixture set,
// honest provenance):
//
//   - Support: is this a request yolo can file today (real), or a story about a service or
//     operation yolo does not mediate yet (synthetic)?
//   - Origin: was the request object produced by `yolo approve` over a real brokerstore by the
//     capture test (captured), or written by hand (hand-written)? A real fixture's request is
//     always captured; a synthetic one's never is.
//   - Evidence: for each platform, is there a native capture (a screenshot of a real OS
//     notification server rendering this fixture), a preview (anything else, such as an HTML
//     tile), or nothing (missing)? A missing capture is listed as missing, never filled with a
//     placeholder.
package approvalfixtures

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
)

// Schema is the fixture format's version. A field may be added within a schema; a removal or
// a change of meaning bumps it.
const Schema = 1

// Fixture is one representative approval request and its evidence.
type Fixture struct {
	Schema int `json:"schema"`
	// ID is the fixture's stable name, lowercase letters, digits and '-', and its file's stem.
	ID string `json:"id"`
	// Title names the scenario in a few words, for a gallery heading.
	Title string `json:"title"`
	// Family groups fixtures by service: git, github, circleci, s3, or cross-cutting for a
	// lifecycle state (denial, expiry, staleness) that is not about one service.
	Family string `json:"family"`
	// Support is "real" for an operation yolo mediates today, "synthetic" for a future one.
	Support string `json:"support"`
	// SupportNote says why: the code path that files the request, or what is not built.
	SupportNote string `json:"support_note"`
	// Prompt is "request" when the operation files an approval request, or "none" when policy
	// runs it without asking (an audited read). A "none" fixture has no Request and no
	// HookEnv: the gallery must not invent a prompt where policy needs none.
	Prompt string `json:"prompt"`
	// State is the scenario's lifecycle state, one of States.
	State string `json:"state"`
	// Story is who did what, in a sentence or two, with generalized names.
	Story string `json:"story"`
	// Origin is "captured" or "hand-written" (see the package comment). Present iff Request is.
	Origin string `json:"origin,omitempty"`
	// CapturedWith is, for a captured fixture, the command whose output Request is, such as
	// "yolo approve --json" or "yolo approve --watch --json (request-decided)".
	CapturedWith string `json:"captured_with,omitempty"`
	// Request is the request object exactly as `yolo approve --json` prints it (schema 1 of
	// that surface, brokerstore.RequestView), times and ids included. Kept as raw JSON so a
	// field the surface adds later survives a round trip untouched.
	Request json.RawMessage `json:"request,omitempty"`
	// HookEnv is the YOLO_REQUEST_* environment the on_request hook receives for this request
	// (brokerhook.Env), with the same origin as Request. Present iff Request is.
	HookEnv map[string]string `json:"hook_env,omitempty"`
	// Decision is, when the scenario is about answering, the `yolo approve` call and what it
	// returned: the outcome every surface must honor (rule 4: transport is not authorization).
	Decision *Decision `json:"decision,omitempty"`
	// Evidence is what exists for how this request renders, one entry per platform and
	// renderer. Every fixture lists at least one entry for linux and one for macos.
	Evidence []Evidence `json:"evidence"`
}

// Decision is one `yolo approve` call against the fixture's request.
type Decision struct {
	// Argv is the call after `yolo`, such as ["approve", "r-…", "--deny"].
	Argv []string `json:"argv"`
	// Exit is its exit code: 0 done, 3 already decided, 4 unknown id, 5 refused by policy,
	// 6 no longer confirmable (internal/cli/approve.go).
	Exit int `json:"exit"`
	// Stderr is the message it printed, verbatim; for a captured fixture, as captured.
	Stderr string `json:"stderr,omitempty"`
}

// Evidence is one rendering of a fixture, or the record that none exists.
type Evidence struct {
	// Platform is linux, macos or browser.
	Platform string `json:"platform"`
	// Kind is native-capture, preview or missing.
	Kind string `json:"kind"`
	// Renderer names what drew it or would draw it, such as "owner baseline: on_request
	// script via notify-send to swaync" or "proposed: toast notifier (boundary-broker §6)".
	Renderer string `json:"renderer"`
	// File is the image or page, relative to the fixtures directory's gallery
	// (docs/design/approval-experience/gallery/). Required for a native capture; absent for
	// missing.
	File string `json:"file,omitempty"`
	// Tested is what the evidence shows: "visual" (how it looks only), or "action" and
	// "lifecycle" when a button press or a withdrawal was also driven and observed.
	Tested string `json:"tested,omitempty"`
	// Provenance is, for a native capture, the environment that drew it. Required keys:
	// os, daemon, daemon_version, theme, scale, captured_at, commit. Any other key is kept.
	Provenance map[string]string `json:"provenance,omitempty"`
	// Note says anything a reader must know, such as why a capture is missing.
	Note string `json:"note,omitempty"`
}

// The closed vocabularies.
var (
	Families  = []string{"git", "github", "circleci", "s3", "cross-cutting"}
	Supports  = []string{"real", "synthetic"}
	Prompts   = []string{"request", "none"}
	Origins   = []string{"captured", "hand-written"}
	Platforms = []string{"linux", "macos", "browser"}
	Kinds     = []string{"native-capture", "preview", "missing"}
	Tests     = []string{"visual", "action", "lifecycle"}
	// States: pending, allowed, denied and expired are the store's own; refused is a request
	// whose jail stopped first; stale is a pending request the host can no longer confirm
	// (exit 6); decided-elsewhere is one another front-end answered first (exit 3); no-prompt
	// is an operation policy runs without a request.
	States = []string{"pending", "allowed", "denied", "expired", "refused", "stale", "decided-elsewhere", "no-prompt"}
	// ProvenanceKeys are required on every native capture.
	ProvenanceKeys = []string{"os", "daemon", "daemon_version", "theme", "scale", "captured_at", "commit"}
)

var idPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// sanitized are the only workspace and repository spellings a fixture may use, so that no
// real path, organization or host reaches a public file (AE-R6). Captured fixtures are filed
// against these on purpose.
var (
	sanitizedWorkspace = regexp.MustCompile(`^/home/dev/(code|src)/[a-z0-9-]+$`)
	sanitizedRepo      = regexp.MustCompile(`^(example\.test/)?example-org/[a-z0-9-]+$`)
)

//go:embed fixtures/*.json
var files embed.FS

// Load reads and validates every fixture, sorted by id. A fixture that fails Validate, or a
// duplicate id, is an error naming the file.
func Load() ([]Fixture, error) {
	entries, err := files.ReadDir("fixtures")
	if err != nil {
		return nil, err
	}
	var out []Fixture
	seen := map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		b, err := files.ReadFile(path.Join("fixtures", name))
		if err != nil {
			return nil, err
		}
		f, err := Parse(b)
		if err != nil {
			return nil, fmt.Errorf("fixtures/%s: %w", name, err)
		}
		if f.ID+".json" != name {
			return nil, fmt.Errorf("fixtures/%s: id %q does not match the file name", name, f.ID)
		}
		if seen[f.ID] {
			return nil, fmt.Errorf("fixtures/%s: duplicate id %q", name, f.ID)
		}
		seen[f.ID] = true
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Parse decodes one fixture, refusing unknown fields, and validates it.
func Parse(b []byte) (Fixture, error) {
	var f Fixture
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return f, err
	}
	return f, f.Validate()
}

// requestCore is the part of a `yolo approve --json` request object Validate reads.
type requestCore struct {
	Schema    int    `json:"schema"`
	ID        string `json:"id"`
	Source    string `json:"source"`
	Workspace string `json:"workspace"`
	Repo      string `json:"repo"`
	State     string `json:"state"`
	Headline  string `json:"headline"`
}

// Validate checks every rule the field comments state.
func (f Fixture) Validate() error {
	var errs []string
	bad := func(format string, a ...any) { errs = append(errs, fmt.Sprintf(format, a...)) }
	if f.Schema != Schema {
		bad("schema is %d, want %d", f.Schema, Schema)
	}
	if !idPattern.MatchString(f.ID) {
		bad("id %q is not lowercase words joined by '-'", f.ID)
	}
	for _, field := range []struct{ name, v string }{{"title", f.Title}, {"support_note", f.SupportNote}, {"story", f.Story}} {
		if strings.TrimSpace(field.v) == "" {
			bad("%s is empty", field.name)
		}
	}
	oneOf := func(name, v string, set []string) {
		for _, s := range set {
			if v == s {
				return
			}
		}
		bad("%s %q is not one of %s", name, v, strings.Join(set, ", "))
	}
	oneOf("family", f.Family, Families)
	oneOf("support", f.Support, Supports)
	oneOf("prompt", f.Prompt, Prompts)
	oneOf("state", f.State, States)

	switch {
	case f.Prompt == "none":
		if f.State != "no-prompt" {
			bad(`prompt "none" needs state "no-prompt"`)
		}
		if len(f.Request) != 0 || f.HookEnv != nil || f.Origin != "" || f.Decision != nil {
			bad(`prompt "none" carries no request, hook_env, origin or decision: policy files no request`)
		}
	case f.Prompt == "request":
		if f.State == "no-prompt" {
			bad(`state "no-prompt" needs prompt "none"`)
		}
		if len(f.Request) == 0 || f.HookEnv == nil {
			bad(`prompt "request" needs request and hook_env`)
		}
		oneOf("origin", f.Origin, Origins)
		if f.Support == "real" && f.Origin != "captured" {
			bad(`a real fixture's request is captured from yolo approve, never hand-written`)
		}
		if f.Support == "synthetic" && f.Origin != "hand-written" {
			bad(`a synthetic fixture's request is hand-written; yolo cannot file it`)
		}
		if f.Origin == "captured" && strings.TrimSpace(f.CapturedWith) == "" {
			bad("a captured fixture names the command it was captured with")
		}
		if f.Origin == "hand-written" && f.CapturedWith != "" {
			bad("a hand-written fixture was captured with nothing")
		}
		if len(f.Request) != 0 {
			var r requestCore
			if err := json.Unmarshal(f.Request, &r); err != nil {
				bad("request: %v", err)
			} else {
				if r.Schema != 1 {
					bad("request schema is %d; fixtures hold `yolo approve --json` schema 1", r.Schema)
				}
				if r.ID == "" || r.Source == "" || r.Headline == "" || r.State == "" {
					bad("request lacks id, source, headline or state")
				}
				if !sanitizedWorkspace.MatchString(r.Workspace) {
					bad("request workspace %q is not a sanitized /home/dev/code/<name> path", r.Workspace)
				}
				if r.Repo != "" && !sanitizedRepo.MatchString(r.Repo) {
					bad("request repo %q is not a sanitized example-org/<name> repository", r.Repo)
				}
				if f.HookEnv != nil && f.HookEnv["YOLO_REQUEST_ID"] != r.ID {
					bad("hook_env YOLO_REQUEST_ID %q is not the request's id %q", f.HookEnv["YOLO_REQUEST_ID"], r.ID)
				}
			}
		}
	}
	if f.Decision != nil {
		if len(f.Decision.Argv) == 0 || f.Decision.Argv[0] != "approve" {
			bad(`decision argv starts with "approve"`)
		}
		switch f.Decision.Exit {
		case 0, 1, 2, 3, 4, 5, 6:
		default:
			bad("decision exit %d is not a yolo approve exit code", f.Decision.Exit)
		}
	}

	platforms := map[string]bool{}
	for i, e := range f.Evidence {
		at := fmt.Sprintf("evidence[%d]", i)
		oneOf(at+".platform", e.Platform, Platforms)
		oneOf(at+".kind", e.Kind, Kinds)
		platforms[e.Platform] = true
		if strings.TrimSpace(e.Renderer) == "" {
			bad("%s.renderer is empty", at)
		}
		if e.Tested != "" {
			oneOf(at+".tested", e.Tested, Tests)
		}
		switch e.Kind {
		case "native-capture":
			if e.Platform == "browser" {
				bad("%s: a browser rendering is a preview, never a native capture", at)
			}
			if e.File == "" || e.Tested == "" {
				bad("%s: a native capture names its file and what it tested", at)
			}
			for _, k := range ProvenanceKeys {
				if strings.TrimSpace(e.Provenance[k]) == "" {
					bad("%s: a native capture records provenance %q", at, k)
				}
			}
		case "preview":
			if e.Tested != "" && e.Tested != "visual" {
				bad("%s: a preview shows how it looks and tests no action or lifecycle", at)
			}
		case "missing":
			if e.File != "" || e.Tested != "" {
				bad("%s: missing evidence has no file and tested nothing", at)
			}
			if strings.TrimSpace(e.Note) == "" {
				bad("%s: missing evidence says why it is missing", at)
			}
		}
		if e.File != "" && (strings.HasPrefix(e.File, "/") || strings.Contains(e.File, "..")) {
			bad("%s.file %q is not a relative path inside the gallery", at, e.File)
		}
	}
	for _, p := range []string{"linux", "macos"} {
		if !platforms[p] {
			bad("no %s evidence entry: list it as missing rather than leave it out", p)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}
