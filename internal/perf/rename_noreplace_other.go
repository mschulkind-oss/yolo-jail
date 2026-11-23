//go:build !linux && !darwin

package perf

func renameNoReplaceAt(dirfd int, old, new string) error {
	return traceDeclined()
}
