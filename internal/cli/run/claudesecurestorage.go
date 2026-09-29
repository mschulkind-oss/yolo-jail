package run

// claudesecurestorage.go is the launch's half of CL-D22's bridge
// (docs/design/claude-login-without-interception.md): until the credential view replaces the
// interception, a launch that links Claude's credential into the machine-scope shared directory
// also points Claude's credential store at that directory (claudeview.SecureStorageEnv), so the
// real `.credentials.json` and Claude's own refresh and write locks sit in one directory with no
// symlink in the credential path.
//
// ONE DECLARATION, TWO VEHICLES. The directory is not written here: it is the `at` of the
// selected pack's `shared_credentials` hook for Claude's credential path, the same declaration
// that names the directory every backend mounts or lays (claudeSharedCredentialDir). The jail
// home it sits under is the backend's, so each launch arm renders the one value with its own
// home: both container backends through claudeSecureStorageEnvArgs, on the argv every one of their
// processes inherits, and macos-user through its launch env, which becomes the sandbox session's
// env file. Neither is IS_SANDBOX=1's shape (a container `-e` alone), which macos-user never sees.
//
// WHY NOT THE PACK'S `env` CONTRIBUTION. An env value is a literal (packdecl.KindEnv), and the
// path differs by backend (/home/agent against the sandbox account's home); and the pack env
// fold also reaches `yolo host`, whose Claude keeps the user's own login (OQ-NC7) and must not be
// moved to a directory yolo owns. Neither holds for a launcher-side value.
//
// TEMPORARY, like the view's switch: deleted with the shared directory and the hook (CL-D7).

import (
	"path"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/claudeview"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// claudeSharedCredentialDir returns the home-relative machine-scope directory a selected pack's
// `shared_credentials` hook links Claude's credential into (the hook whose `from` is
// claudeview.ViewRel, the path the in-jail entrypoint's view skip recognizes too), or "" when no
// selected pack gives Claude the machine's shared login. The directory must be one the same
// pack declares at machine scope, which is the entrypoint's own condition for honoring the hook
// (declaresSharedDir), so a declaration the jail would refuse points Claude nowhere.
func claudeSharedCredentialDir(packs []*packload.Pack) string {
	for _, p := range packs {
		if p == nil || p.Decl == nil {
			continue
		}
		for _, h := range p.Decl.HookContributions() {
			if h.Name != entrypoint.HookSharedCredentials || h.SharedDir == "" ||
				path.Clean(filepath.ToSlash(h.File)) != claudeview.ViewRel {
				continue
			}
			for _, d := range p.Decl.SharedDirContributions() {
				if d == h.SharedDir {
					return path.Clean(filepath.ToSlash(h.SharedDir))
				}
			}
		}
	}
	return ""
}

// claudeSecureStorageDir is the bridge's one predicate and its value: the absolute path, under
// the jail home `home`, that this launch points Claude's credential store at, or "" when it does
// not bridge.
//
// MUTUALLY EXCLUSIVE WITH THE VIEW, through the view's own predicate (claudeCredentialView). A
// view launch hands Claude a regular file at ~/.claude/.credentials.json that the host broker
// writes, without a refresh token; pointing the store at the shared directory would put the
// machine's refresh token back in front of Claude, which is the one thing the view exists not to
// do. So the bridge is off exactly when the view is on, and on for every other launch whose
// selected packs link Claude's credential into the machine tier, whether or not the broker
// loophole runs (a Bedrock user's claude still shares one login).
func (o *Options) claudeSecureStorageDir(rt string, cfg *jsonx.OrderedMap, packs []*packload.Pack,
	home string) string {
	if o.claudeCredentialView(rt, cfg) {
		return ""
	}
	dir := claudeSharedCredentialDir(packs)
	if dir == "" || home == "" {
		return ""
	}
	return path.Join(filepath.ToSlash(home), dir)
}

// claudeSecureStorageEnvArgs is the `-e CLAUDE_SECURESTORAGE_CONFIG_DIR=/home/agent/<dir>` both
// container backends put on the argv: a container env var, so the entrypoint and every process an
// attach starts inherit it. Nothing when the launch does not bridge.
func (o *Options) claudeSecureStorageEnvArgs(rt string, cfg *jsonx.OrderedMap,
	packs []*packload.Pack) []string {
	dir := o.claudeSecureStorageDir(rt, cfg, packs, jailHome)
	if dir == "" {
		return nil
	}
	return []string{"-e", claudeview.SecureStorageEnv + "=" + dir}
}
