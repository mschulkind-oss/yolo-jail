package entrypoint

import "strings"

// npmspec.go owns one thing: turning a pack's `package` string into the two DIFFERENT
// values the npm launcher needs — the package NAME and the argument handed to
// `npm install -g`.
//
// They were the same value until 2026-08-17, and that is the whole defect: the launcher
// template appended a literal `@latest` to the declared string, so `foo@1.2.3` became
// `foo@1.2.3@latest` and npm resolved nothing. A version was therefore not merely
// unpinnable but INEXPRESSIBLE — which is why docs/design/trust-paths.md §1 lists
// "program via npm" as the top row where a pin would change an outcome and then notes the
// row cannot even be attempted until this parsing exists.
//
// The two values cannot be collapsed back together: the name alone is what indexes
// node_modules (`$NPM_CONFIG_PREFIX/lib/node_modules/$PKG/package.json`) and what
// `npm view <pkg> version` accepts. A spec in either position names something that does
// not exist.

// splitNpmSpec splits an npm package string into its package name and its optional
// version selector.
//
// The one rule that makes this more than a strings.Cut: npm's SCOPED packages
// (`@scope/name`) begin with an `@` that is part of the name, not a separator. The
// separator is therefore the first `@` at a NON-ZERO index — `@scope/name` has no version,
// `@scope/name@1.2.3` has one, and both spellings have to work because the shipped packs
// use the scoped form (`@anthropic-ai/claude-code`, `@openai/codex`).
//
// The selector is returned VERBATIM and is deliberately not validated or normalized: npm
// accepts an exact version (`1.2.3`), a dist-tag (`next`), and a range (`^1.0.0`) in the
// same position, and every one of them is a legitimate thing for a pack to declare. Which
// of those it is only matters for how much we trust it to stay put, and this function
// makes no such judgement — see npmSpecIsPinned.
//
// A trailing `@` with nothing after it (`foo@`) is treated as no version at all rather
// than as an empty selector, because `npm install foo@` is an error and the author's
// evident intent is the unversioned package.
func splitNpmSpec(spec string) (name, version string) {
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

// npmInstallSpec renders the argument for `npm install -g`.
//
// An unversioned declaration still resolves to `@latest`, exactly as it did before this
// file existed: that is the shipped behaviour of every pack in the tree and changing it
// would be a policy decision, not a bug fix. This is the ONE place that `@latest` is
// spelled, so the decision is reviewable rather than buried in a shell template.
func npmInstallSpec(name, version string) string {
	if version == "" {
		return name + "@latest"
	}
	return name + "@" + version
}

// npmSpecIsPinned reports whether the declaration named a version at all.
//
// "Pinned" here means "the pack chose the selector", not "the selector is immutable" — a
// dist-tag and a range both move. That is still the right line for the launcher's update
// poll, because the poll asks `npm view <pkg> version`, i.e. "what is the registry's
// `latest` dist-tag?", and that answer is meaningless against ANY explicit selector:
// honouring it overrides the declaration, and ignoring it makes the network round-trip
// pure cost. Worse, for a tag or a range the comparison can never come out equal, so the
// pre-fix code would have reinstalled once an hour, forever, for a package the author
// pinned precisely to stop that.
func npmSpecIsPinned(version string) bool { return version != "" }
