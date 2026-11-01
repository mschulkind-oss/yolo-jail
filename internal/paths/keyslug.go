package paths

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
)

// KeySlug is the file or directory name one build key "<pack>/<bin>[#upstream]" takes: readable,
// one path component, and unique by a hash of the key, since a key holds a "/". Every per-key file
// below is named by it, so a renamed pack's files move to the slug of the new key
// (configmigrate's keyed-state move), never by a plain rename.
func KeySlug(key string) string {
	sum := sha256.Sum256([]byte(key))
	return strings.ReplaceAll(key, "/", "--") + "-" + hex.EncodeToString(sum[:])[:8]
}

// The names a key's background-advance state takes under BackgroundAdvanceDir: the lock a
// background advance holds while it runs the key, the lock each outcome transition holds, the
// outcome record, the outcome the next launch has reported, and the prefix of a claimed outcome
// ("<prefix><slug>-<unique>").
const (
	BackgroundKeyLockSuffix      = ".lock"
	BackgroundArtifactLockSuffix = ".artifacts.lock"
	BackgroundOutcomeSuffix      = ".json"
	BackgroundSaidSuffix         = ".said"
	BackgroundClaimPrefix        = ".claimed-"
)

// BackgroundAdvanceKeyFile is key's file of the given suffix under BackgroundAdvanceDir.
func BackgroundAdvanceKeyFile(key, suffix string) string {
	return filepath.Join(BackgroundAdvanceDir(), KeySlug(key)+suffix)
}

// BuildLogName is a key's build log: under GlobalStorage's logs/ for a build with no workspace,
// and under the workspace's .yolo for one a launch ran.
func BuildLogName(key string) string { return "build-" + KeySlug(key) + ".log" }

// TreeBuildLogName is a key's tree-build log under GlobalStorage's logs/, appended to by every build
// of the key and moved aside to TreeBuildLogName+".1" past its size bound.
func TreeBuildLogName(key string) string { return "tree-build-" + KeySlug(key) + ".log" }

// LogsDir is GlobalStorage's logs/, where the host's per-key build logs live.
func LogsDir() string { return filepath.Join(GlobalStorage(), "logs") }
