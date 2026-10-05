package hostfloor

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// node.go is the floor's OWN Node (OQ-HP4, ruled (a)): the official release tarball for this
// platform, verified against the release's published sha256, at the version yolo ships raised to
// the highest `node_floor` a selected npm program declares. It lives entirely inside the prefix,
// is never on any PATH but an installer's own, and an npm agent is started by its absolute path —
// so neither the ambient PATH nor mise decides what interpreter a floor agent runs on
// (HP-DIR2 item 2).
//
// # Where the expected digest comes from
//
// For the SHIPPED release it is compiled into yolo (ShippedNodeSHA256): the tarball is checked
// against a value that did not arrive over the same connection as the bytes. For a release a
// pack's node_floor raises the floor to, yolo cannot have shipped a digest, so it reads that
// release's published SHASUMS256.txt from the same distribution and checks against it — the
// "verified against the release's published checksums" of OQ-HP4's option (a).

// ShippedNodeVersion is the Node release yolo ships for the floor: the current LTS line's release
// when it was pinned (Node 24, "Krypton", which the jail image also runs), without the `v`.
const ShippedNodeVersion = "24.21.0"

// ShippedNodeSHA256 is the published sha256 of each ShippedNodeVersion tarball the floor can use,
// by Node's own platform name. Copied from https://nodejs.org/dist/v24.21.0/SHASUMS256.txt.
var ShippedNodeSHA256 = map[string]string{
	"linux-x64":    "6e1db87ef58b8819e5d5402eff1536491b18edd8eb7bee5ef7897876e88dc5ff",
	"linux-arm64":  "724282c3b43aec998aa9527380465b45d229e021b58035f5f4f63095eabfe5d5",
	"darwin-x64":   "1462cb3b3046b815cf8ea436d3da450ec1a9f11dac7e5a46b0ada5305d7e8097",
	"darwin-arm64": "bed7eea5325e1108f32ce5228ddd6a5f0f08a499ee42aa7442aea583702f6057",
}

// officialNodeLoader is the dynamic loader Node's official build for each Linux platform asks for
// (its PT_INTERP; MEASURED for ShippedNodeVersion's tarballs). It is compiled in so the floor can
// tell before any download whether this machine can start that build at all (HP-D15, noEntryReason),
// and an install checks the extracted node against it (ensureNode). A darwin build asks for none
// that yolo checks: Mach-O names dyld, which every Mac has.
var officialNodeLoader = map[string]string{
	"linux-x64":   "/lib64/ld-linux-x86-64.so.2",
	"linux-arm64": "/lib/ld-linux-aarch64.so.1",
}

// DefaultNodeDistURL is Node's official release distribution.
const DefaultNodeDistURL = "https://nodejs.org/dist"

// NodeDist is where the floor's Node comes from. The zero value is the official distribution
// and the shipped release.
type NodeDist struct {
	// BaseURL is the distribution root, the directory holding v<version>/. "" =>
	// DefaultNodeDistURL. A test points it at a local server.
	BaseURL string
	// Client fetches from it. nil => an http.Client with a generous timeout.
	Client *http.Client
	// Shipped is the release yolo ships ("" => ShippedNodeVersion), and Pinned its digests
	// (nil => ShippedNodeSHA256). A test replaces both together, with a tarball of its own.
	Shipped string
	Pinned  map[string]string
}

func (d NodeDist) baseURL() string {
	if d.BaseURL != "" {
		return strings.TrimRight(d.BaseURL, "/")
	}
	return DefaultNodeDistURL
}

func (d NodeDist) shipped() string {
	if d.Shipped != "" {
		return strings.TrimPrefix(d.Shipped, "v")
	}
	return ShippedNodeVersion
}

func (d NodeDist) pinned() map[string]string {
	if d.Pinned != nil {
		return d.Pinned
	}
	return ShippedNodeSHA256
}

func (d NodeDist) client() *http.Client {
	if d.Client != nil {
		return d.Client
	}
	return &http.Client{Timeout: 10 * time.Minute}
}

// nodePlatform is Node's name for a Go platform, and whether Node publishes a build for it.
func nodePlatform(goos, goarch string) (string, bool) {
	var os string
	switch goos {
	case "linux", "darwin":
		os = goos
	default:
		return "", false
	}
	switch goarch {
	case "amd64":
		return os + "-x64", true
	case "arm64":
		return os + "-arm64", true
	}
	return "", false
}

// NodeVersion is the release the floor installs npm programs on now: the shipped one, raised to
// NodeFloor when a selected pack declares a higher floor. A floor shorter than three parts is
// padded with zeros ("22.19" -> "22.19.0"), the lowest release that meets it — the minimal
// raise, and one whose SHASUMS256.txt exists.
func (f *Floor) NodeVersion() string {
	v := f.Node.shipped()
	if f.NodeFloor != "" && packdecl.CompareVersions(f.NodeFloor, v) > 0 {
		parts := strings.Split(strings.TrimPrefix(f.NodeFloor, "v"), ".")
		for len(parts) < 3 {
			parts = append(parts, "0")
		}
		v = strings.Join(parts, ".")
	}
	return v
}

// nodeDir is where release v is extracted.
func (f *Floor) nodeDir(v string) string { return filepath.Join(f.nodeRoot(), "v"+v) }

// nodeBin is the release's bin/: node and npm, on an installer's PATH only.
func (f *Floor) nodeBin(v string) string { return filepath.Join(f.nodeDir(v), "bin") }

// NodeReady reports whether release v is extracted and complete in the prefix.
func (f *Floor) NodeReady(v string) bool {
	_, err := os.Stat(filepath.Join(f.nodeDir(v), completeMarker))
	return err == nil
}

// ensureNode puts release v in the prefix, once per machine: downloaded, verified, extracted into
// a scratch directory and renamed into place, so a reader sees a complete release or none.
func (f *Floor) ensureNode(ctx context.Context, v string) (string, error) {
	if f.NodeReady(v) {
		return f.nodeBin(v), nil
	}
	plat, ok := nodePlatform(f.GOOS, f.GOARCH)
	if !ok {
		return "", fmt.Errorf("no official Node build is published for %s/%s", f.GOOS, f.GOARCH)
	}
	if err := f.ensureDir("node", "downloads", "locks"); err != nil {
		return "", err
	}
	lk, err := acquire(f.lockPath("node"), true, func(pid int) {
		f.say("waiting for pid %d, which is fetching Node v%s into yolo's floor", pid, v)
	})
	if err != nil {
		return "", err
	}
	defer lk.release()
	if f.NodeReady(v) {
		return f.nodeBin(v), nil
	}
	name := "node-v" + v + "-" + plat + ".tar.gz"
	url := f.Node.baseURL() + "/v" + v + "/" + name
	want, source, err := f.expectedNodeDigest(ctx, v, name)
	if err != nil {
		return "", err
	}
	f.say("fetching Node v%s (%s) for yolo's floor from %s", v, plat, url)
	tarball, got, err := f.download(ctx, url)
	if tarball != "" {
		defer os.Remove(tarball)
	}
	if err != nil {
		return "", fmt.Errorf("fetching %s: %w", url, err)
	}
	if got != want {
		return "", fmt.Errorf("%s has sha256 %s, but %s says %s — refusing to install it",
			name, got, source, want)
	}
	f.say("verified %s against %s", name, source)
	scratch, err := os.MkdirTemp(f.nodeRoot(), ".v"+v+".*.tmp")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(scratch)
	if err := extractTarGz(tarball, scratch, 1); err != nil {
		return "", fmt.Errorf("unpacking %s: %w", name, err)
	}
	for _, need := range []string{"node", "npm"} {
		if _, err := os.Stat(filepath.Join(scratch, "bin", need)); err != nil {
			return "", fmt.Errorf("%s holds no bin/%s", name, need)
		}
	}
	// THE LOADER IT ASKS FOR (HP-D15), read from the node just extracted: the one compiled in, or
	// none, or the check that kept a machine without it from downloading this build checked the
	// wrong file. A fork's Node script reaches here with no such check first (execRecord), so the
	// loader itself is asked about too.
	if interp, err := elfInterp(filepath.Join(scratch, "bin", "node")); err == nil {
		if want, ok := officialNodeLoader[plat]; ok && interp != "" && interp != want {
			return "", fmt.Errorf("%s's node asks for the dynamic loader %q, not %s as this yolo expects of "+
				"Node's official %s build, so this yolo cannot tell whether the machine can start it — refusing "+
				"to install it; `yolo update` brings a yolo that knows, and if none does, it is a yolo bug to report",
				name, interp, want, plat)
		}
		if why := f.loaderProblem(interp); why != "" {
			return "", &noEntryError{reason: "Node's official " + plat + " build " + why}
		}
	}
	if err := os.WriteFile(filepath.Join(scratch, completeMarker), nil, 0o600); err != nil {
		return "", err
	}
	dest := f.nodeDir(v)
	_ = os.RemoveAll(dest) // an incomplete one, since NodeReady said no under the lock
	if err := os.Rename(scratch, dest); err != nil {
		return "", err
	}
	return f.nodeBin(v), nil
}

// expectedNodeDigest is the sha256 the tarball must have, and where that value came from.
func (f *Floor) expectedNodeDigest(ctx context.Context, v, name string) (sum, source string, err error) {
	plat := strings.TrimSuffix(strings.TrimPrefix(name, "node-v"+v+"-"), ".tar.gz")
	if v == f.Node.shipped() {
		if sum, ok := f.Node.pinned()[plat]; ok {
			return sum, "the digest yolo ships for Node v" + v, nil
		}
	}
	url := f.Node.baseURL() + "/v" + v + "/SHASUMS256.txt"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", "", err
	}
	resp, err := f.Node.client().Do(req)
	if err != nil {
		return "", "", fmt.Errorf("fetching Node v%s's published checksums: %w", v, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("fetching %s: %s", url, resp.Status)
	}
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 2 && fields[1] == name {
			return strings.ToLower(fields[0]), url, nil
		}
	}
	return "", "", fmt.Errorf("%s lists no %s", url, name)
}

// download streams url into a file under downloads/, hashing as it goes.
func (f *Floor) download(ctx context.Context, url string) (file, sum string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", "", err
	}
	resp, err := f.Node.client().Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("%s", resp.Status)
	}
	out, err := os.CreateTemp(f.downloads(), "node-*.tar.gz.part")
	if err != nil {
		return "", "", err
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(out, h), resp.Body); err != nil {
		out.Close()
		return out.Name(), "", err
	}
	if err := out.Close(); err != nil {
		return out.Name(), "", err
	}
	return out.Name(), hex.EncodeToString(h.Sum(nil)), nil
}

// extractTarGz unpacks a .tar.gz into dest, dropping the first strip path components (a Node
// release wraps everything in node-v<version>-<platform>/).
//
// It refuses rather than repairs anything that would write outside dest: an absolute name, a
// `..` segment, and a link whose target leaves the tree. Only directories, regular files,
// symlinks and hard links are made; a device or a fifo in a language runtime's tarball is not
// something to reproduce.
func extractTarGz(file, dest string, strip int) error {
	fh, err := os.Open(file)
	if err != nil {
		return err
	}
	defer fh.Close()
	gz, err := gzip.NewReader(fh)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		rel, ok := stripName(hdr.Name, strip)
		if !ok {
			continue
		}
		if !safeRel(rel) {
			return fmt.Errorf("refusing %q: it leaves the archive's tree", hdr.Name)
		}
		target := filepath.Join(dest, filepath.FromSlash(rel))
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			mode := os.FileMode(hdr.Mode).Perm() &^ 0o022
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode|0o200)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			if err := out.Close(); err != nil {
				return err
			}
			if err := os.Chmod(target, mode); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if path.IsAbs(hdr.Linkname) || !safeRel(path.Join(path.Dir(rel), hdr.Linkname)) {
				return fmt.Errorf("refusing link %q -> %q: it leaves the archive's tree",
					hdr.Name, hdr.Linkname)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return err
			}
		case tar.TypeLink:
			linkRel, ok := stripName(hdr.Linkname, strip)
			if !ok || !safeRel(linkRel) {
				return fmt.Errorf("refusing hard link %q -> %q: it leaves the archive's tree",
					hdr.Name, hdr.Linkname)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.Link(filepath.Join(dest, filepath.FromSlash(linkRel)), target); err != nil {
				return err
			}
		}
	}
}

// stripName drops the first n slash-separated components of a tar name, reporting false for a
// name that has nothing left (the wrapping directory itself).
func stripName(name string, n int) (string, bool) {
	name = strings.TrimPrefix(path.Clean("/"+name), "/")
	parts := strings.Split(name, "/")
	if len(parts) <= n {
		return "", false
	}
	return strings.Join(parts[n:], "/"), true
}

// safeRel reports whether a slash-separated relative path stays inside its root.
func safeRel(rel string) bool {
	if rel == "" || path.IsAbs(rel) {
		return false
	}
	clean := path.Clean(rel)
	return clean != ".." && !strings.HasPrefix(clean, "../")
}

// nodeVersionOf reads a release directory's version back from its name ("v24.21.0").
func nodeVersionOf(dirName string) (string, bool) {
	v, ok := strings.CutPrefix(dirName, "v")
	if !ok {
		return "", false
	}
	for _, p := range strings.Split(v, ".") {
		if _, err := strconv.Atoi(p); err != nil {
			return "", false
		}
	}
	return v, true
}
