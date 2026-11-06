package entrypoint

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// PrivateHTTPClientsEnv carries a fresh launch's bound selected private HTTP clients into the
// container (docs/design/pack-brokers-http-plan.md, Task 2 first slice). The keeper sets it at
// `podman run`, from the immutable entrypoint record it published beside the prepared
// generation, so a running jail keeps the bindings it booted with.
const PrivateHTTPClientsEnv = "YOLO_PRIVATE_HTTP_CLIENTS"

// PrivateClientBootstrap is the absolute shipped bootstrap every selected private client starts
// through: the generated launcher, the top-level fresh argv and the attach argv. It is the
// read-only install prefix's own binary, never a PATH lookup or a workspace prefix.
const PrivateClientBootstrap = paths.JailPrefixDir + "/bin/yolo"

// PrivateClientExecVerb is the bootstrap's hidden `yolo internal` verb.
const PrivateClientExecVerb = "private-client-exec"

// PrivateClientStore is the jail's read-only capture store, from which a bound client runs in
// place: the run pipeline's read-only bind of the host store.
const PrivateClientStore = paths.ContextCapturesDir

// PrivateClientBinding binds one selected private client's bin and prepared generation to the
// promoted entrypoint identity in an admitted capture entry. Every field is nonsecret.
type PrivateClientBinding struct {
	Bin        string `json:"bin"`
	Generation string `json:"generation"`
	CaptureKey string `json:"capture_key"`
	// RuntimeRoot is the entry-tree-relative directory whose regular files, links and modes
	// RuntimeFiles digests; Entrypoint is inside it.
	RuntimeRoot      string `json:"runtime_root"`
	Entrypoint       string `json:"entrypoint"`
	EntrypointSHA256 string `json:"entrypoint_sha256"`
	RuntimeFiles     string `json:"runtime_files"`
}

func validHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func validSHA256Ref(s string) bool {
	return strings.HasPrefix(s, "sha256:") && validHex(strings.TrimPrefix(s, "sha256:"), 64)
}

func validTreePath(p string) bool {
	return p != "" && p != "." && !strings.Contains(p, "\\") && path.Clean(p) == p && filepath.IsLocal(p)
}

// Valid reports whether b is a complete binding: a bin name, a 32-hex generation, a capture key,
// local slash paths with the entrypoint inside the runtime root, and two sha256 identities.
func (b PrivateClientBinding) Valid() error {
	switch {
	case !packdecl.ValidBinName(b.Bin):
		return errors.New("private client binding names no valid bin")
	case !validHex(b.Generation, 32):
		return errors.New("private client binding has no prepared generation")
	case !validHex(b.CaptureKey, 16):
		return errors.New("private client binding has no capture key")
	case !validTreePath(b.RuntimeRoot) || !validTreePath(b.Entrypoint) || !strings.HasPrefix(b.Entrypoint, b.RuntimeRoot+"/"):
		return errors.New("private client binding has no confined entrypoint")
	case !validSHA256Ref(b.EntrypointSHA256) || !validSHA256Ref(b.RuntimeFiles):
		return errors.New("private client binding has no entrypoint identity")
	}
	return nil
}

// PrivateClientArgv is the ONE argv every caller starts a bound client with, before the client's
// own arguments: the absolute bootstrap, its verb, the binding, and `--`.
func PrivateClientArgv(b PrivateClientBinding) []string {
	return []string{PrivateClientBootstrap, "internal", PrivateClientExecVerb,
		"--bin=" + b.Bin, "--generation=" + b.Generation, "--capture=" + b.CaptureKey,
		"--runtime-root=" + b.RuntimeRoot, "--entrypoint=" + b.Entrypoint,
		"--entrypoint-sha256=" + b.EntrypointSHA256, "--runtime-files=" + b.RuntimeFiles, "--"}
}

// ParsePrivateClientArgs is PrivateClientArgv's inverse over the arguments after the verb. It
// accepts exactly that shape and returns the client's own arguments.
func ParsePrivateClientArgs(args []string) (PrivateClientBinding, []string, error) {
	var b PrivateClientBinding
	fields := []struct {
		flag string
		dst  *string
	}{{"--bin=", &b.Bin}, {"--generation=", &b.Generation}, {"--capture=", &b.CaptureKey},
		{"--runtime-root=", &b.RuntimeRoot}, {"--entrypoint=", &b.Entrypoint},
		{"--entrypoint-sha256=", &b.EntrypointSHA256}, {"--runtime-files=", &b.RuntimeFiles}}
	if len(args) < len(fields)+1 || args[len(fields)] != "--" {
		return b, nil, errors.New("private client bootstrap takes exactly its bound argv")
	}
	for i, f := range fields {
		value, ok := strings.CutPrefix(args[i], f.flag)
		if !ok {
			return b, nil, errors.New("private client bootstrap takes exactly its bound argv")
		}
		*f.dst = value
	}
	if err := b.Valid(); err != nil {
		return b, nil, err
	}
	return b, args[len(fields)+1:], nil
}

// MarshalPrivateClients is PrivateHTTPClientsEnv's value: the bindings, sorted by bin.
func MarshalPrivateClients(bindings []PrivateClientBinding) (string, error) {
	sorted := slices.Clone(bindings)
	slices.SortFunc(sorted, func(a, b PrivateClientBinding) int { return strings.Compare(a.Bin, b.Bin) })
	for i, b := range sorted {
		if err := b.Valid(); err != nil {
			return "", err
		}
		if i > 0 && sorted[i-1].Bin == b.Bin {
			return "", fmt.Errorf("private client %q is bound twice", b.Bin)
		}
	}
	data, err := json.Marshal(sorted)
	return string(data), err
}

// ParsePrivateClients reads PrivateHTTPClientsEnv's value strictly: an empty value is no
// bindings, and anything else must be a sorted, duplicate-free list of valid bindings.
func ParsePrivateClients(value string) ([]PrivateClientBinding, error) {
	if value == "" {
		return nil, nil
	}
	dec := json.NewDecoder(strings.NewReader(value))
	dec.DisallowUnknownFields()
	var out []PrivateClientBinding
	if err := dec.Decode(&out); err != nil || dec.More() {
		return nil, errors.New("private client bindings are malformed")
	}
	again, err := MarshalPrivateClients(out)
	if err != nil {
		return nil, err
	}
	if again != value {
		return nil, errors.New("private client bindings are not in their canonical form")
	}
	return out, nil
}

// PrivateClientFileDigest is one regular file's identity, opened without following a link.
func PrivateClientFileDigest(p string) (string, error) {
	f, err := os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("private client file is not a regular file")
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// noLinkComponents refuses a symbolic link (or a non-directory) at any component of rel under
// base, so a verified path is the bytes it names.
func noLinkComponents(base, rel string, leafRegular bool) error {
	cur := base
	parts := strings.Split(rel, "/")
	for i, part := range parts {
		cur = filepath.Join(cur, part)
		info, err := os.Lstat(cur)
		if err != nil {
			return err
		}
		last := i == len(parts)-1
		if info.Mode()&fs.ModeSymlink != 0 || !last && !info.IsDir() || last && leafRegular && !info.Mode().IsRegular() || last && !leafRegular && !info.IsDir() {
			return fmt.Errorf("private client path %s is not a plain %s", rel, map[bool]string{true: "file", false: "directory"}[leafRegular])
		}
	}
	return nil
}

// PrivateClientRuntimeDigest is the identity of every entry under tree/runtimeRoot: each
// directory's mode, each regular file's mode and sha256, and each link's target, in lexical
// order. Any other file type is refused.
func PrivateClientRuntimeDigest(tree, runtimeRoot string) (string, error) {
	if !validTreePath(runtimeRoot) {
		return "", errors.New("private client runtime root is not confined")
	}
	if err := noLinkComponents(tree, runtimeRoot, false); err != nil {
		return "", err
	}
	root := filepath.Join(tree, filepath.FromSlash(runtimeRoot))
	h := sha256.New()
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			fmt.Fprintf(h, "l %q %q\n", rel, target)
		case d.IsDir():
			fmt.Fprintf(h, "d %q %o\n", rel, info.Mode().Perm())
		case d.Type().IsRegular():
			sum, err := PrivateClientFileDigest(p)
			if err != nil {
				return err
			}
			fmt.Fprintf(h, "f %q %o %s\n", rel, info.Mode().Perm(), sum)
		default:
			return fmt.Errorf("private client runtime file %s has an unsupported type", rel)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// VerifyPrivateClient checks b against the admitted entry in store: no link along the runtime
// root or the entrypoint, the entrypoint's bytes, and every runtime file's identity. It returns
// the entrypoint's absolute path.
func VerifyPrivateClient(store string, b PrivateClientBinding) (string, error) {
	if err := b.Valid(); err != nil {
		return "", err
	}
	if !filepath.IsAbs(store) {
		return "", errors.New("private client capture store is not absolute")
	}
	tree := filepath.Join(store, "entries", b.CaptureKey, "tree")
	if info, err := os.Lstat(tree); err != nil || !info.IsDir() {
		return "", fmt.Errorf("private client %s has no admitted capture %s", b.Bin, b.CaptureKey)
	}
	if err := noLinkComponents(tree, b.Entrypoint, true); err != nil {
		return "", fmt.Errorf("private client %s entrypoint: %w", b.Bin, err)
	}
	entry := filepath.Join(tree, filepath.FromSlash(b.Entrypoint))
	sum, err := PrivateClientFileDigest(entry)
	if err != nil || sum != b.EntrypointSHA256 {
		return "", fmt.Errorf("private client %s entrypoint does not match its bound identity", b.Bin)
	}
	runtimeSum, err := PrivateClientRuntimeDigest(tree, b.RuntimeRoot)
	if err != nil || runtimeSum != b.RuntimeFiles {
		return "", fmt.Errorf("private client %s runtime files do not match their bound identity", b.Bin)
	}
	return entry, nil
}

// privateClientLauncher is the generated launcher of a bound private client: the shared argv
// and the caller's arguments, with no install, refresh or PATH lookup. A binding that does not
// validate gets a launcher that refuses, never the ordinary npm launcher.
func privateClientLauncher(b PrivateClientBinding) string {
	if err := b.Valid(); err != nil {
		return "#!/bin/sh\necho " + shquote.Quote(b.Bin+": this launch's private client binding is invalid ("+err.Error()+") — stop the jail and rerun yolo to reprepare it") + " >&2\nexit 1\n"
	}
	return "#!/bin/sh\n# A selected private HTTP client: started only through the shipped bootstrap,\n# bound to its prepared generation and verified capture entrypoint.\nexec " +
		shquote.Join(PrivateClientArgv(b)) + " \"$@\"\n"
}

// privateClientBindings is this boot's PrivateHTTPClientsEnv, keyed by bin.
func privateClientBindings(e *Env) (map[string]PrivateClientBinding, error) {
	bindings, err := ParsePrivateClients(e.Getenv(PrivateHTTPClientsEnv))
	if err != nil {
		return nil, fmt.Errorf("yolo-entrypoint: %s: %w — stop the jail and rerun yolo to reprepare its private clients", PrivateHTTPClientsEnv, err)
	}
	out := make(map[string]PrivateClientBinding, len(bindings))
	for _, b := range bindings {
		out[b.Bin] = b
	}
	return out, nil
}

// privateLaunchersWritten is nil when every bound bin's launcher, in the launch dir and the
// private dir alike, is exactly its bootstrap, and
// otherwise the boot's refusal naming each bin that is not: a bound client whose name no selected
// program wrote would run whatever else provides that name, an ordinary unverified copy.
func privateLaunchersWritten(launcherDir, privateDir string, bound map[string]PrivateClientBinding) error {
	var missing []string
	for bin, b := range bound {
		for _, dir := range []string{launcherDir, privateDir} {
			got, err := os.ReadFile(filepath.Join(dir, bin))
			if err != nil || string(got) != privateClientLauncher(b) {
				missing = append(missing, bin)
				break
			}
		}
	}
	if len(missing) == 0 {
		return nil
	}
	slices.Sort(missing)
	return fmt.Errorf("yolo-entrypoint: private HTTP client %s is bound for this jail, but no selected pack's program wrote its private launcher, so it would run an ordinary copy; stop the jail and rerun yolo to reprepare its private clients (drop the client's env contribution naming the front if its pack no longer declares the program)", strings.Join(missing, ", "))
}

// privateClientPathShell is the shell text a bound session runs after mise's activation, or "" in
// a jail with no bound client (so an ordinary jail's activation is unchanged byte for byte). It
// moves PrivateClientDir to the PATH's front, which `mise env` and `mise activate` would otherwise
// have put their tool dirs ahead of, and then fails the shell, with its next step, unless every
// bound bin resolves to its bootstrap there. This is position, not a census of mise's names: a
// project's own mise.toml, or a tool shipping the bin under another key, is covered the same way.
//
// With hooks (the .bashrc's interactive shells) it also runs after mise's own prompt and chpwd
// hooks, which re-prepend mise's dirs on every prompt and every cd.
func privateClientPathShell(e *Env, hooks bool) string {
	bindings, err := ParsePrivateClients(e.Getenv(PrivateHTTPClientsEnv))
	if err != nil || len(bindings) == 0 {
		return ""
	}
	dir := shquote.Quote(e.PrivateClientDir())
	bins := make([]string, 0, len(bindings))
	for _, b := range bindings {
		bins = append(bins, shquote.Quote(b.Bin))
	}
	s := `_yolo_private_first() { local d=` + dir + ` p; p=":$PATH:"; p="${p//":$d:"/:}"; p="${p#:}"; PATH="$d:${p%:}"; export PATH; }; _yolo_private_first; ` +
		`for _yolo_b in ` + strings.Join(bins, " ") + `; do [ "$(command -v -- "$_yolo_b")" = ` + dir + `/"$_yolo_b" ] || ` +
		`{ echo "yolo: private HTTP client $_yolo_b does not resolve to its bootstrap launcher in this shell; stop the jail and rerun yolo to reprepare its private clients" >&2; exit 1; }; done; unset _yolo_b; `
	if hooks {
		s += `if [[ $- == *i* ]]; then ` +
			`if [[ "$(declare -p PROMPT_COMMAND 2>/dev/null)" == "declare -a"* ]]; then PROMPT_COMMAND+=(_yolo_private_first); ` +
			`else PROMPT_COMMAND="${PROMPT_COMMAND:+$PROMPT_COMMAND;}_yolo_private_first"; fi; ` +
			`chpwd_functions+=(_yolo_private_first); fi`
	}
	return s
}
