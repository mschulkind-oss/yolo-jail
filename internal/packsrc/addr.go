// Package packsrc parses and resolves pack SOURCE addresses (C5).
//
// It is deliberately dependency-free on the rest of the repo: address parsing is
// pure string work worth testing in isolation, and `yolo pack` needs it without
// dragging the config loader in.
//
// Grammar, Terraform's in substance: `//` splits the repository from the in-repo
// path, and query parameters come after the subdirectory segment.
//
//	git+ssh://git@github.com/acme/mono//tools/agent-pack?ref=alice/rust-review
//	git+https://gitlab.acme.internal/eng/mono//agents/pack?ref=v2.1.0
//	file:///home/matt/code/acme-mono/tools/agent-pack
//
// `git+scheme://` (nix/pip style) rather than Terraform's `git::` — it parses
// identically and reads better.
//
// `ref` IS MANDATORY for git sources. An unpinned float is the top-ranked
// anti-pattern in the precedent survey and the specific way kustomize bases and
// chezmoi git-repo externals go wrong: the pack you audited is not the pack you get
// next week. Requiring it means a moving target has to be asked for by name
// (`?ref=main`), not acquired by omission.
package packsrc

import (
	"fmt"
	"net/url"
	"strings"
)

// Kind distinguishes the source transports.
type Kind int

const (
	// KindFile is a local directory (file://), fetched by doing nothing.
	KindFile Kind = iota
	// KindGit is a git repository (git+ssh://, git+https://).
	KindGit
)

// Addr is a parsed, validated pack address.
type Addr struct {
	Kind Kind
	// Raw is the address exactly as written, for error messages and the lockfile's
	// record of what the user asked for.
	Raw string
	// Repo is the git clone URL with the `git+` prefix stripped and any subpath and
	// query removed. Empty for KindFile.
	Repo string
	// Path is the in-repo subdirectory (slash-separated, no leading or trailing
	// slash). Empty means the repository root. For KindFile it is the local path.
	Path string
	// Ref is the git ref: a branch, tag, or full commit SHA. Always non-empty for
	// KindGit; empty for KindFile.
	Ref string
}

// IsLocal reports whether this address needs no fetch.
func (a Addr) IsLocal() bool { return a.Kind == KindFile }

// Parse parses and validates a pack address.
//
// Every rejection names what is wrong and what to write instead: an address is
// hand-written config, so a bare "invalid" would send the user to the source.
func Parse(raw string) (Addr, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return Addr{}, fmt.Errorf("empty pack address")
	}
	scheme, rest, ok := strings.Cut(s, "://")
	if !ok {
		return Addr{}, fmt.Errorf("pack address %q has no scheme — write "+
			"file:///abs/path or git+ssh://git@host/org/repo//subdir?ref=main", raw)
	}

	switch {
	case scheme == "file":
		return parseFile(raw, rest)
	case strings.HasPrefix(scheme, "git+"):
		return parseGit(raw, strings.TrimPrefix(scheme, "git+"), rest)
	default:
		return Addr{}, fmt.Errorf("pack address %q: unsupported scheme %q "+
			"(expected file://, git+ssh:// or git+https://)", raw, scheme)
	}
}

// parseFile handles file:///abs/path. A local path carries no ref: it is whatever is
// on disk, which is the point of using one during authoring.
func parseFile(raw, rest string) (Addr, error) {
	// A query on a file:// address is almost certainly a copy-paste of a git address
	// and would be silently ignored, so name it.
	if path, q, hasQ := strings.Cut(rest, "?"); hasQ {
		return Addr{}, fmt.Errorf("pack address %q: file:// takes no query parameters "+
			"(got %q) — a local pack is whatever is on disk; use %s",
			raw, q, "file://"+path)
	}
	path := "/" + strings.TrimPrefix(rest, "/")
	if strings.Contains(rest, "//") {
		return Addr{}, fmt.Errorf("pack address %q: file:// has no repo/subdir split, "+
			"so `//` is not meaningful — write the full path", raw)
	}
	if err := checkNoDotDot(path, "path"); err != nil {
		return Addr{}, fmt.Errorf("pack address %q: %w", raw, err)
	}
	return Addr{Kind: KindFile, Raw: raw, Path: strings.TrimRight(path, "/")}, nil
}

// parseGit handles git+<transport>://host/repo[//subdir]?ref=...
func parseGit(raw, transport, rest string) (Addr, error) {
	// `file` is accepted as a GIT transport (git+file:///path/to/repo.git), distinct
	// from a plain file:// directory: it clones a local repository, so a ref means
	// something. It is what makes this path testable against real git without a
	// network, and it is genuinely useful for a repo on a shared mount.
	switch transport {
	case "ssh", "https", "http", "file":
	default:
		return Addr{}, fmt.Errorf("pack address %q: unsupported git transport %q "+
			"(expected git+ssh://, git+https:// or git+file://)", raw, transport)
	}

	body, query, _ := strings.Cut(rest, "?")

	// `//` splits repo from in-repo path. Split on the LAST occurrence so a
	// transport that legitimately contains `//` cannot confuse it — in practice the
	// body never does, but being explicit costs nothing.
	repoPart, subPath := body, ""
	if i := strings.Index(body, "//"); i >= 0 {
		repoPart, subPath = body[:i], strings.Trim(body[i+2:], "/")
	}
	repoPart = strings.TrimRight(repoPart, "/")
	if repoPart == "" {
		return Addr{}, fmt.Errorf("pack address %q: missing host/repository", raw)
	}
	if err := checkNoDotDot(subPath, "subdirectory"); err != nil {
		return Addr{}, fmt.Errorf("pack address %q: %w", raw, err)
	}

	ref, err := refFromQuery(raw, query)
	if err != nil {
		return Addr{}, err
	}

	// Normalize before this value is ever compared or used as a cache key: a
	// trailing .git and a redundant trailing slash name the SAME repository, and two
	// spellings of one repo would otherwise fetch and store twice.
	repoURL := transport + "://" + strings.TrimSuffix(repoPart, ".git")
	if transport == "file" {
		// git wants an absolute path for a local clone; the parse above stripped the
		// scheme, so restore the leading slash the URL form implies.
		repoURL = "file://" + "/" + strings.TrimLeft(strings.TrimSuffix(repoPart, ".git"), "/")
	}
	if _, err := url.Parse(repoURL); err != nil {
		return Addr{}, fmt.Errorf("pack address %q: not a valid URL: %w", raw, err)
	}
	return Addr{Kind: KindGit, Raw: raw, Repo: repoURL, Path: subPath, Ref: ref}, nil
}

// refFromQuery extracts the MANDATORY ref.
func refFromQuery(raw, query string) (string, error) {
	if query == "" {
		return "", fmt.Errorf("pack address %q: missing required ?ref= — a git pack "+
			"must be pinned (an unpinned source silently changes under you; write "+
			"?ref=main if you really want to follow a branch)", raw)
	}
	values, err := url.ParseQuery(query)
	if err != nil {
		return "", fmt.Errorf("pack address %q: bad query %q: %w", raw, query, err)
	}
	for key := range values {
		if key != "ref" {
			return "", fmt.Errorf("pack address %q: unknown query parameter %q "+
				"(only ?ref= is supported)", raw, key)
		}
	}
	ref := strings.TrimSpace(values.Get("ref"))
	if ref == "" {
		return "", fmt.Errorf("pack address %q: empty ?ref= — a git pack must be pinned", raw)
	}
	if err := checkNoDotDot(ref, "ref"); err != nil {
		return "", fmt.Errorf("pack address %q: %w", raw, err)
	}
	// A ref is interpolated into git argv. Refuse anything that could be read as an
	// option rather than a ref; git itself would reject most of these, but failing
	// here names the problem instead of surfacing a git usage error.
	if strings.HasPrefix(ref, "-") {
		return "", fmt.Errorf("pack address %q: ref %q must not start with '-'", raw, ref)
	}
	return ref, nil
}

// checkNoDotDot rejects a `..` path segment.
//
// For the subdirectory this is a traversal guard: `//..%2f..` must not reach outside
// the pack. For the REF it guards a different thing — git's `a..b` range syntax —
// which would turn a ref into a revision range and check out something the address
// does not name.
func checkNoDotDot(s, what string) error {
	if s == "" {
		return nil
	}
	if s == ".." || strings.Contains(s, "..") {
		return fmt.Errorf("%s %q must not contain '..'", what, s)
	}
	return nil
}

// CacheKey is the stable identity of a fetched source: repo + ref + subpath. Used
// for the on-disk store layout and for lockfile comparison, which is why Parse
// normalizes before this is built.
func (a Addr) CacheKey() string {
	if a.IsLocal() {
		return "file:" + a.Path
	}
	return a.Repo + "@" + a.Ref + "//" + a.Path
}
