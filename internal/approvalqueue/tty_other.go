//go:build !(linux || darwin || freebsd || netbsd || openbsd || dragonfly)

package approvalqueue

import "os"

// isTerminal is false where there is no way to ask: the queue then refuses to start, which is
// the safe answer.
func isTerminal(f *os.File) bool { return false }
