package packdecl

import "strings"

// npmpackage.go owns one thing: turning a pack's `package` string into the two DIFFERENT
// values an npm install needs — the package NAME and the argument handed to `npm install -g`.
//
// It lives in this dependency-free package because TWO installers read it: the jail's
// generated npm launcher (internal/entrypoint, npmspec.go, which delegates here) and the host
// agent floor (internal/hostfloor, docs/design/host-tool-provisioning.md). Two parsers of one
// declaration would be two answers to "is this pinned?", which decides whether a program is
// ever updated.
//
// They were the same value until 2026-08-17, and that is the whole defect: the launcher
// template appended a literal `@latest` to the declared string, so `foo@1.2.3` became
// `foo@1.2.3@latest` and npm resolved nothing. The name alone is what indexes node_modules
// (`<prefix>/lib/node_modules/<name>/package.json`) and what `npm view <name> version`
// accepts; the spec is what is installed. A spec in either name position names something
// that does not exist.

// SplitNpmSpec splits an npm package string into its package name and its optional version
// selector.
//
// The one rule that makes this more than a strings.Cut: npm's SCOPED packages (`@scope/name`)
// begin with an `@` that is part of the name, not a separator. The separator is therefore the
// first `@` at a NON-ZERO index — `@scope/name` has no version, `@scope/name@1.2.3` has one.
//
// The selector is returned VERBATIM and is deliberately not validated or normalized: npm
// accepts an exact version (`1.2.3`), a dist-tag (`next`), and a range (`^1.0.0`) in the same
// position. A trailing `@` with nothing after it (`foo@`) is treated as no version at all,
// because `npm install foo@` is an error and the author's evident intent is the unversioned
// package.
func SplitNpmSpec(spec string) (name, version string) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return "", ""
	}
	// Skip index 0 so a scope's leading @ is never read as a separator.
	at := strings.Index(spec[1:], "@")
	if at < 0 {
		return spec, ""
	}
	name, version = spec[:at+1], spec[at+2:]
	if version == "" {
		return name, ""
	}
	return name, version
}

// NpmInstallSpec renders the argument for `npm install -g`.
//
// An unversioned declaration resolves to `@latest`: that is the shipped behaviour of every
// pack in the tree, and this is the ONE place `@latest` is spelled, so the decision is
// reviewable rather than buried in a shell template.
func NpmInstallSpec(name, version string) string {
	if version == "" {
		return name + "@latest"
	}
	return name + "@" + version
}

// NpmSpecIsPinned reports whether the declaration named a version at all.
//
// "Pinned" means "the pack chose the selector", not "the selector is immutable" — a dist-tag
// and a range both move. It is still the right line for an update poll, because the poll asks
// `npm view <pkg> version`, the registry's `latest`, and that answer is meaningless against ANY
// explicit selector: honouring it overrides the declaration, and for a tag or a range the
// comparison never comes out equal, so a poll would reinstall once an hour, forever.
func NpmSpecIsPinned(version string) bool { return version != "" }
