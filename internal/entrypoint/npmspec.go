package entrypoint

import "github.com/mschulkind-oss/yolo-jail/internal/packdecl"

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
//
// THE RULE ITSELF IS packdecl's (npmpackage.go), and these three are its names in this
// package. It moved there because the host agent floor (internal/hostfloor) installs the same
// declarations, and two parsers of one `package` string would be two answers to "is this
// pinned?" — the question that decides whether a program is ever updated.

// splitNpmSpec splits an npm package string into its package name and its optional
// version selector (packdecl.SplitNpmSpec).
func splitNpmSpec(spec string) (name, version string) { return packdecl.SplitNpmSpec(spec) }

// npmInstallSpec renders the argument for `npm install -g` (packdecl.NpmInstallSpec).
func npmInstallSpec(name, version string) string { return packdecl.NpmInstallSpec(name, version) }

// npmSpecIsPinned reports whether the declaration named a version at all
// (packdecl.NpmSpecIsPinned).
func npmSpecIsPinned(version string) bool { return packdecl.NpmSpecIsPinned(version) }
