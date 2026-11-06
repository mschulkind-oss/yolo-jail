//go:build !linux

package run

import "os"

// mcpOutcomeInodeGeneration has no generation to read off Linux: darwin reports st_gen as
// zero to an unprivileged caller, and APFS does not hand a freed inode number to the next
// file, so the device and inode numbers stay the identity there.
func mcpOutcomeInodeGeneration(*os.File) (uint64, bool) { return 0, false }
