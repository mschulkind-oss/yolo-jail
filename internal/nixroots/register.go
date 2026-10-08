package nixroots

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// DefaultStoreDir is the nix store a jail sees: the host's, bind-mounted read-only at its
// own path.
const DefaultStoreDir = "/nix/store"

// ErrUntranslatable is a link the map cannot spell for the host: under no mount the
// launcher named, or under a mask. Nothing is created and nothing is sent, which leaves the
// link exactly as unrooted as it is with no translated root at all (the design's §4).
var ErrUntranslatable = errors.New("the link is under no mount the launcher can spell for the host")

// Registrar registers translated roots from inside a jail.
type Registrar struct {
	// Map is the launcher's HostMap, as MapEnv carried it.
	Map HostMap
	// Socket is the daemon's socket; "" is DefaultSocket.
	Socket string
	// StoreDir is the store a root may point into; "" is DefaultStoreDir.
	StoreDir string
	// Timeout bounds the daemon exchange; 0 is DefaultTimeout.
	Timeout time.Duration
}

// Root makes link a symlink to storePath and registers it with the host's nix daemon under
// the host's spelling of link, which it returns. It is what `nix-store --add-root` does on
// the host, with the one string changed.
//
// The order is translate, then link, then send. A link the map cannot translate is
// ErrUntranslatable before anything is written, so a jail without a usable map creates no
// link it cannot root. storePath must be a store path — a direct child of the store
// directory, present in this jail's view of the store — because the daemon roots a link only
// when its target is one: anything else would be a second hop, which roots nothing
// (§2.1).
//
// The link is REPLACED, not updated in place: a fresh symlink renamed over the old one, the
// way nix replaces its own. That keeps the swap atomic for a reader, and it renews the link's
// mtime on every call, which the image roots' age policy reads (prune.PruneOrphanImageRoots).
//
// A *RejectedError is the daemon refusing; every other error means only that no root was
// made, and a caller treats it as today's in-jail state.
func (r Registrar) Root(link, storePath string) (string, error) {
	storeDir := r.StoreDir
	if storeDir == "" {
		storeDir = DefaultStoreDir
	}
	if !filepath.IsAbs(storePath) || filepath.Dir(filepath.Clean(storePath)) != filepath.Clean(storeDir) {
		return "", fmt.Errorf("%q is not a path in the store %s", storePath, storeDir)
	}
	if _, err := os.Lstat(storePath); err != nil {
		return "", fmt.Errorf("store path %s is not in this jail's view of the store: %w", storePath, err)
	}
	if !filepath.IsAbs(link) {
		return "", fmt.Errorf("GC-root link %q is not absolute", link)
	}
	// The link's DIRECTORY is resolved, not the link: the daemon lstats the link itself, and
	// the link is about to be replaced anyway, while a symlink anywhere above it would put the
	// bytes under a different mount than its spelling names.
	dir, err := filepath.EvalSymlinks(filepath.Dir(link))
	if err != nil {
		return "", err
	}
	host, ok := r.Map.Translate(filepath.Join(dir, filepath.Base(link)))
	if !ok {
		return "", ErrUntranslatable
	}
	if err := replaceSymlink(storePath, link); err != nil {
		return "", err
	}
	if err := AddIndirectRoot(r.Socket, host, r.Timeout); err != nil {
		return "", err
	}
	return host, nil
}

// replaceSymlink points link at target by renaming a fresh symlink over it.
func replaceSymlink(target, link string) error {
	tmp := link + ".tmp-" + strconv.Itoa(os.Getpid())
	_ = os.Remove(tmp)
	if err := os.Symlink(target, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, link); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// Register registers an EXISTING link, which must point into the store, under the host's
// spelling, and returns that spelling. It is Root without the link-making half, for a caller
// that made the link itself (the workspace registry, which makes its links beneath an
// os.Root).
func (r Registrar) Register(link string) (string, error) {
	if !filepath.IsAbs(link) {
		return "", fmt.Errorf("GC-root link %q is not absolute", link)
	}
	dir, err := filepath.EvalSymlinks(filepath.Dir(link))
	if err != nil {
		return "", err
	}
	host, ok := r.Map.Translate(filepath.Join(dir, filepath.Base(link)))
	if !ok {
		return "", ErrUntranslatable
	}
	if err := AddIndirectRoot(r.Socket, host, r.Timeout); err != nil {
		return "", err
	}
	return host, nil
}

// Pin is a temp root on target, held on a connection of its own until Close: the HANDOFF
// FENCE of in-jail-nix-roots.md §2.4 and §4. A caller pins the store path the moment it
// learns of it, makes and registers its managed link through Pin.Register on the same
// connection, and only then closes. Register re-adds the temp root after the permanent root
// is acknowledged, so a GC already running — whose permanent-root scan may predate the new
// link — still receives the pin through its synchronization before the connection closes.
type Pin struct {
	r      Registrar
	conn   *Conn
	target string
}

// Pin dials the daemon and adds a temp root on target.
func (r Registrar) Pin(target string) (*Pin, error) {
	conn, err := Dial(r.Socket, r.Timeout)
	if err != nil {
		return nil, err
	}
	if err := conn.AddTempRoot(target); err != nil {
		conn.Close()
		return nil, err
	}
	return &Pin{r: r, conn: conn, target: target}, nil
}

// Register is Registrar.Register on the pin's connection, followed by the fence.
func (p *Pin) Register(link string) (string, error) {
	if !filepath.IsAbs(link) {
		return "", fmt.Errorf("GC-root link %q is not absolute", link)
	}
	dir, err := filepath.EvalSymlinks(filepath.Dir(link))
	if err != nil {
		return "", err
	}
	host, ok := p.r.Map.Translate(filepath.Join(dir, filepath.Base(link)))
	if !ok {
		return "", ErrUntranslatable
	}
	if err := p.conn.AddIndirectRoot(host); err != nil {
		return "", err
	}
	if err := p.conn.AddTempRoot(p.target); err != nil {
		return "", err
	}
	return host, nil
}

// Close releases the temp root.
func (p *Pin) Close() error { return p.conn.Close() }
