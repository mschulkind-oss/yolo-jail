package loopholedecl

// binaries.go is the `binaries` key: an EXECUTABLE A LOOPHOLE SHIPS AS A DECLARED DOWNLOAD,
// pinned by a mandatory sha256 (docs/design/broker-as-a-pack.md BP-D1), and the two tokens an
// argv names one with.
//
// # Why a download and not a file in the pack tree
//
// A file committed to the tree reaches a jail with its exec bit only from a pack configured BY
// PATH (packstage.copyFile carries 0o111). An EMBEDDED pack, the shape every official pack has,
// cannot carry one at all: `embed.FS` reads every file back 0444, and the leased per-build tree
// is sealed read-only (broker-as-a-pack.md §3.1 option A, measured with packs/hello-daemon). So
// the bytes live OUTSIDE every pack tree. `yolo pack install` fetches each build this machine
// needs, verifies it against the digest written here, and caches it by digest with the exec bit
// set (internal/packbin); the launch reads the cache and never fetches. The manifest pins the
// bytes and the pack's commit pins the manifest, so a pinned pack pins everything that runs
// (§3.1's P4).
//
// # The declaration
//
//	"binaries": {
//	  "<name>": {
//	    "<goos>/<goarch>": {"url": "https://…", "sha256": "<64 lowercase hex digits>"},
//	    …
//	  }
//	}
//
// The platform key is EXACT, both halves, in Go's spelling and against the same closed lists
// `platforms` uses: a compiled file is built for one machine, so a GOOS-only key would name a
// build that runs on every architecture, which no binary does.
//
// # Two tokens, for the module dir's reason
//
// `{binary:<name>}` is legal in the fields that run on the HOST (`host_daemon.cmd`, `doctor_cmd`)
// and resolves to the cached build for this machine's GOOS/GOARCH. `{jail_binary:<name>}` is
// legal in `jail_daemon.cmd` and resolves to JailBinaryPath, where the launch mounts the build
// for the JAIL's platform read-only (JailPlatform). They differ in platform as well as in path —
// on an Apple Silicon Mac the host build is darwin/arm64 and the jail build linux/arm64 — so one
// token with two resolutions would be the asymmetry TokenLoopholeDir's comment refuses, twice
// over. Each is refused in the other half, naming the fix; both are refused in every other
// field, where nothing substitutes them; every token must name a declared binary; and every
// declared binary must be named by a token, since one nothing names would be fetched and never
// run.
//
// # The skew shape, named
//
// A build that predates this key reads it TOLERANTLY: `binaries` is skipped and reported, and a
// token in an argv reaches the daemon literally, so the spawn fails with the token in its
// error. That is only possible for a FETCHED pack's manifest, which can be newer than the binary
// reading it; an embedded pack rides in the binary that reads it.

import (
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/pytext"
)

const (
	keyBinaries = "binaries"
	keyURL      = "url"
	keySHA256   = "sha256"
)

// binaryBuildKeys is the census for one `binaries.<name>.<platform>` build.
var binaryBuildKeys = []string{keyURL, keySHA256}

// JailBinaryRoot is the CONTAINER directory under which the launch mounts every jail binary,
// one file per (loophole, binary): JailBinaryPath. Outside JailLoopholeDir on purpose, because
// that directory is the module dir's read-only bind, and a file mount nested inside a read-only
// bind needs a mountpoint the bind cannot provide.
const JailBinaryRoot = "/etc/yolo-jail/loophole-binaries/"

// JailBinaryPath is what `{jail_binary:<name>}` resolves to for the loophole `loophole`: the
// container path its build is mounted read-only at.
func JailBinaryPath(loophole, name string) string { return JailBinaryRoot + loophole + "/" + name }

// JailPlatform is the platform a jail binary is built for on a machine of architecture goarch:
// linux, on the machine's own architecture, because the jail image is Linux for THIS machine's
// arch whatever the host OS is (internal/cli/run's jailprefix.go states the same rule for the
// binaries yolo mounts itself).
func JailPlatform(goarch string) string { return "linux/" + goarch }

// TokenBinary is the host-side token naming the binary `name`.
func TokenBinary(name string) string { return "{binary:" + name + "}" }

// TokenJailBinary is the jail-side token naming the binary `name`.
func TokenJailBinary(name string) string { return "{jail_binary:" + name + "}" }

// binaryTokenRE matches both spellings. The name group is anything up to the closing brace, so
// a malformed name is caught by the name check rather than by the token going unrecognized and
// reaching a daemon literally.
var binaryTokenRE = regexp.MustCompile(`\{(jail_)?binary:([^{}]*)\}`)

// binaryNameRE is a binary's name: it becomes a file name in the cache and in the container,
// and the last element of the argv's path.
var binaryNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]*$`)

// sha256RE is a sha256 digest as `sha256sum` prints it.
var sha256RE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Binary is one `binaries` entry: an executable, and the build of it for each platform it has
// one for.
type Binary struct {
	Name string
	// Builds are in declaration order, one per platform.
	Builds []BinaryBuild
}

// BinaryBuild is one platform's build of a binary.
type BinaryBuild struct {
	// Platform is "<goos>/<goarch>".
	Platform string
	// URL is where `yolo pack install` fetches it: https, with no credentials in it.
	URL string
	// SHA256 is the build's digest, 64 lowercase hex digits. It is the pin, and the cache key.
	SHA256 string
}

// BuildFor returns the build for platform, if the binary has one.
func (b Binary) BuildFor(platform string) (BinaryBuild, bool) {
	for _, bb := range b.Builds {
		if bb.Platform == platform {
			return bb, true
		}
	}
	return BinaryBuild{}, false
}

// BuildPlatforms returns the platforms the binary has a build for, sorted.
func (b Binary) BuildPlatforms() []string {
	out := make([]string, 0, len(b.Builds))
	for _, bb := range b.Builds {
		out = append(out, bb.Platform)
	}
	sort.Strings(out)
	return out
}

// BinaryRefs is which declared binaries the manifest's argvs name, and on which side. Decoded
// from the RAW fields, because resolving a record substitutes the tokens away.
type BinaryRefs struct {
	// Host are the names `{binary:<name>}` names in `host_daemon.cmd` or `doctor_cmd`, in the
	// order first named.
	Host []string
	// Jail are the names `{jail_binary:<name>}` names in `jail_daemon.cmd`, in the order first
	// named.
	Jail []string
}

// BinaryNeed is one build a machine needs to run a loophole: a binary, on one side, and that
// side's platform.
type BinaryNeed struct {
	// Binary is the binary's name.
	Binary string
	// Jail is true for a `{jail_binary:}` reference (the build runs in the jail), false for a
	// `{binary:}` one (it runs on the host).
	Jail bool
	// Platform is the platform the build must be for: the machine's own for a host reference,
	// JailPlatform for a jail one.
	Platform string
	// Build is the declared build for Platform, valid when HasBuild.
	Build    BinaryBuild
	HasBuild bool
}

// Where says where a need's build runs, for a message.
func (n BinaryNeed) Where() string {
	if n.Jail {
		return "in the jail"
	}
	return "on this machine"
}

// BinariesNeeded returns every build a machine of goos/goarch needs to run this loophole: one per
// (binary, side) the argvs name, host references first, each in the order first named. A need
// with HasBuild false is a binary with no build for that platform, which makes the loophole
// unsupported there (internal/loopholes' SupportedHere).
//
// PURE, and passed the pair, for SupportsPlatform's reasons: every combination is testable from
// one process, and this package reads nothing about the machine.
func (m *Manifest) BinariesNeeded(goos, goarch string) []BinaryNeed {
	return BinaryNeedsFor(m.Binaries, m.BinaryRefs, goos, goarch)
}

// BinaryNeedsFor is BinariesNeeded over the two fields, for a caller holding a resolved record
// rather than a manifest.
func BinaryNeedsFor(binaries []Binary, refs BinaryRefs, goos, goarch string) []BinaryNeed {
	byName := map[string]Binary{}
	for _, b := range binaries {
		byName[b.Name] = b
	}
	var out []BinaryNeed
	add := func(name string, jail bool, platform string) {
		n := BinaryNeed{Binary: name, Jail: jail, Platform: platform}
		n.Build, n.HasBuild = byName[name].BuildFor(platform)
		out = append(out, n)
	}
	for _, name := range refs.Host {
		add(name, false, goos+"/"+goarch)
	}
	for _, name := range refs.Jail {
		add(name, true, JailPlatform(goarch))
	}
	return out
}

// ReplaceBinaryTokens returns args with every binary token of one side replaced: the jail
// spelling when jail is true, the host one otherwise. path answers the substitution for a name,
// and a name it answers false for keeps its token, which is what a need with no build does (the
// loophole is then unsupported, so nothing runs the argv). A new slice; args is not changed.
func ReplaceBinaryTokens(args []string, jail bool, path func(name string) (string, bool)) []string {
	out := make([]string, len(args))
	for i, s := range args {
		out[i] = binaryTokenRE.ReplaceAllStringFunc(s, func(tok string) string {
			sub := binaryTokenRE.FindStringSubmatch(tok)
			if (sub[1] != "") != jail {
				return tok
			}
			if p, ok := path(sub[2]); ok {
				return p
			}
			return tok
		})
	}
	return out
}

// parseBinaries validates the optional `binaries` mapping. nil when absent.
func parseBinaries(manifestPath string, raw any) ([]Binary, error) {
	if raw == nil {
		return nil, nil
	}
	m, ok := raw.(*jsonx.OrderedMap)
	if !ok {
		return nil, Errorf("%s: 'binaries' must be a mapping of binary name to its builds, "+
			"e.g. {\"mytool\": {\"linux/amd64\": {\"url\": \"https://…\", \"sha256\": \"…\"}}}",
			manifestPath)
	}
	out := []Binary{}
	for _, name := range m.Keys() {
		field := "binaries." + name
		if err := refuseControlChars(manifestPath, "a 'binaries' name", name); err != nil {
			return nil, err
		}
		if !binaryNameRE.MatchString(name) || len(name) > 128 {
			return nil, Errorf("%s: binaries name %s is not a file name yolo can cache and mount —"+
				" use letters, digits, '.', '_', '+' and '-', starting with a letter or digit",
				manifestPath, pytext.Repr(name))
		}
		v, _ := m.Get(name)
		builds, isMap := v.(*jsonx.OrderedMap)
		if !isMap {
			return nil, Errorf("%s: '%s' must be a mapping of '<goos>/<goarch>' to "+
				"{\"url\": …, \"sha256\": …}", manifestPath, field)
		}
		if builds.Len() == 0 {
			return nil, Errorf("%s: '%s' declares no build — name at least one platform, e.g. "+
				"\"linux/amd64\"", manifestPath, field)
		}
		b := Binary{Name: name}
		for _, platform := range builds.Keys() {
			bb, err := parseBinaryBuild(manifestPath, field, platform, getOrNil(builds, platform))
			if err != nil {
				return nil, err
			}
			b.Builds = append(b.Builds, bb)
		}
		out = append(out, b)
	}
	return out, nil
}

// parseBinaryBuild validates one `binaries.<name>.<platform>` build.
func parseBinaryBuild(manifestPath, field, platform string, raw any) (BinaryBuild, error) {
	field += "." + platform
	if err := refuseControlChars(manifestPath, "a '"+keyBinaries+"' platform", platform); err != nil {
		return BinaryBuild{}, err
	}
	goos, goarch, hasArch := strings.Cut(platform, "/")
	if !hasArch || goarch == "" || strings.Contains(goarch, "/") {
		return BinaryBuild{}, Errorf("%s: '%s': a build's platform is '<goos>/<goarch>', both "+
			"halves — a compiled file runs on one architecture", manifestPath, field)
	}
	if !inList(goos, knownGOOS) {
		return BinaryBuild{}, Errorf("%s: '%s' names %s, which is not a known GOOS — spell it "+
			"as Go does, one of %s", manifestPath, field, pytext.Repr(goos),
			sortedListRepr(knownGOOS))
	}
	if !inList(goarch, knownGOARCH) {
		return BinaryBuild{}, Errorf("%s: '%s' names %s, which is not a known GOARCH — spell it "+
			"as Go does, one of %s", manifestPath, field, pytext.Repr(goarch),
			sortedListRepr(knownGOARCH))
	}
	m, isMap := raw.(*jsonx.OrderedMap)
	if !isMap {
		return BinaryBuild{}, Errorf("%s: '%s' must be a mapping {\"url\": …, \"sha256\": …}",
			manifestPath, field)
	}
	rawURL, _ := getOrNil(m, keyURL).(string)
	if rawURL == "" {
		return BinaryBuild{}, Errorf("%s: '%s.url' must be a non-empty string", manifestPath, field)
	}
	if err := refuseControlChars(manifestPath, "'"+field+".url'", rawURL); err != nil {
		return BinaryBuild{}, err
	}
	u, err := url.Parse(rawURL)
	switch {
	case err != nil || u.Host == "":
		return BinaryBuild{}, Errorf("%s: '%s.url' %s is not an absolute URL", manifestPath,
			field, pytext.Repr(rawURL))
	case u.Scheme != "https":
		return BinaryBuild{}, Errorf("%s: '%s.url' %s must be https — the sha256 pins the "+
			"bytes, and https keeps what was asked for private on the way", manifestPath, field,
			pytext.Repr(rawURL))
	case u.User != nil:
		return BinaryBuild{}, Errorf("%s: '%s.url' carries credentials — every footprint of "+
			"this pack prints the URL, so a credential in it is published; serve the file "+
			"without one", manifestPath, field)
	}
	sum, isStr := getOrNil(m, keySHA256).(string)
	if !isStr || sum == "" {
		return BinaryBuild{}, Errorf("%s: '%s.sha256' is required — a download without a "+
			"digest leaves the pack pinned and the program it runs unpinned (a release asset "+
			"can be re-uploaded under the same URL); write the file's sha256", manifestPath, field)
	}
	if !sha256RE.MatchString(sum) {
		return BinaryBuild{}, Errorf("%s: '%s.sha256' %s is not a sha256 digest — 64 lowercase "+
			"hex digits, as `sha256sum` prints it", manifestPath, field, pytext.Repr(sum))
	}
	return BinaryBuild{Platform: platform, URL: rawURL, SHA256: sum}, nil
}

// binaryRefField is one argv-valued field and the side its binary tokens resolve on.
type binaryRefField struct {
	name string
	args []string
	// side is "host" or "jail" for a field that resolves tokens, "" for one that resolves none.
	side string
}

// resolveBinaryRefs checks every binary token against its field and the declarations, and
// returns which binaries each side names. See the file doc for the four rules.
func resolveBinaryRefs(manifestPath string, binaries []Binary, fields []binaryRefField) (BinaryRefs, error) {
	declared := map[string]bool{}
	for _, b := range binaries {
		declared[b.Name] = true
	}
	var refs BinaryRefs
	seen := map[string]bool{}
	named := map[string]bool{}
	for _, f := range fields {
		for _, s := range f.args {
			for _, sub := range binaryTokenRE.FindAllStringSubmatch(s, -1) {
				tok, jail, name := sub[0], sub[1] != "", sub[2]
				switch {
				case f.side == "":
					return BinaryRefs{}, Errorf("%s: %s names '%s', which nothing resolves in "+
						"this field — '{binary:<name>}' is resolved in 'host_daemon.cmd' and "+
						"'doctor_cmd', and '{jail_binary:<name>}' in 'jail_daemon.cmd'",
						manifestPath, f.name, tok)
				case jail && f.side == "host":
					return BinaryRefs{}, Errorf("%s: %s names '%s', which resolves to the build "+
						"mounted in the CONTAINER — this field runs on the HOST; write '%s'",
						manifestPath, f.name, tok, TokenBinary(name))
				case !jail && f.side == "jail":
					return BinaryRefs{}, Errorf("%s: %s names '%s', which resolves to the build "+
						"cached on the HOST — this field runs in the container; write '%s'",
						manifestPath, f.name, tok, TokenJailBinary(name))
				case !declared[name]:
					return BinaryRefs{}, Errorf("%s: %s names '%s', but 'binaries' declares no "+
						"binary %s — declare its builds, or fix the name", manifestPath, f.name,
						tok, pytext.Repr(name))
				}
				named[name] = true
				key := f.side + "\x00" + name
				if seen[key] {
					continue
				}
				seen[key] = true
				if jail {
					refs.Jail = append(refs.Jail, name)
				} else {
					refs.Host = append(refs.Host, name)
				}
			}
		}
	}
	for _, b := range binaries {
		if !named[b.Name] {
			return BinaryRefs{}, Errorf("%s: 'binaries' declares %s, but no argv names it — "+
				"write '%s' in 'host_daemon.cmd' or 'doctor_cmd', or '%s' in 'jail_daemon.cmd'; "+
				"a binary nothing runs would be downloaded for nothing", manifestPath,
				pytext.Repr(b.Name), TokenBinary(b.Name), TokenJailBinary(b.Name))
		}
	}
	return refs, nil
}

// binaryRefFields lists every string field a binary token could be written in, with the side
// each resolves on. The ones that resolve none are listed so a token there is refused rather
// than reaching a child, a mount or a probe literally.
func binaryRefFields(doctorCmd []string, hostDaemon *HostDaemon, jailDaemon *JailDaemon,
	jailEnv *EnvMap, caCert string, binds []HostBindMount, req Requires,
	stateFiles, devices []string) []binaryRefField {
	fields := []binaryRefField{{name: "'doctor_cmd'", args: doctorCmd, side: "host"}}
	if hostDaemon != nil {
		fields = append(fields, binaryRefField{name: "'host_daemon.cmd'", args: hostDaemon.Cmd, side: "host"},
			binaryRefField{name: "'host_daemon.env'", args: envValues(hostDaemon.Env)})
	}
	if jailDaemon != nil {
		fields = append(fields, binaryRefField{name: "'jail_daemon.cmd'", args: jailDaemon.Cmd, side: "jail"},
			binaryRefField{name: "'jail_daemon.host_cmd'", args: jailDaemon.HostCmd})
	}
	other := []string{caCert, req.CommandOnPath, req.FileExists}
	for _, bm := range binds {
		other = append(other, bm.Host, bm.Container)
	}
	other = append(other, stateFiles...)
	other = append(other, devices...)
	other = append(other, envValues(jailEnv)...)
	return append(fields, binaryRefField{name: "a path or environment field", args: other})
}

func envValues(env *EnvMap) []string {
	if env == nil {
		return nil
	}
	var out []string
	for _, k := range env.Keys() {
		v, _ := env.Get(k)
		out = append(out, v)
	}
	return out
}
