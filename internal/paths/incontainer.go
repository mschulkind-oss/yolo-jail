package paths

// InsideContainer reports whether THIS process runs inside a Linux container — a podman
// jail marks itself with /run/.containerenv, a Docker one with /.dockerenv. It is the one
// spelling of that probe: the run assembler reads it to force `--net=host` on a nested
// jail (internal/cli/run's inContainer), and the Linux builder reads it to pick a nested
// container's network (internal/containerbuilder), so the two cannot disagree about
// whether they are nested. exists is the caller's file-existence seam.
//
// It does not know the host OS. Neither file exists on macOS, but the callers gate on
// the OS themselves, because a macOS process is never "nested" in the sense both mean.
func InsideContainer(exists func(path string) bool) bool {
	return exists("/run/.containerenv") || exists("/.dockerenv")
}
