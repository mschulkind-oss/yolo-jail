package run

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The host paths a launch bind-mounts to give the jail the nix store: the daemon socket
// (read-write) and the store itself (:ro). Spelled once because TWO decisions read them —
// the mount the assembler emits, and C4/C5's store-delivery eligibility — and those two
// disagreeing would produce the one failure store delivery must not be able to have: an
// image built without its packages, launched without the store they were left in.
const (
	hostNixSocket = "/nix/var/nix/daemon-socket"
	hostNixStore  = "/nix/store"
)

// hostNixMounted answers "will this launch bind-mount the host nix store?" for both
// readers, from the one predicate below.
func (o *Options) hostNixMounted(rt string) bool {
	return shouldMountHostNix(rt, o.PathExists(hostNixSocket), o.PathExists(hostNixStore),
		o.IsMacOS, o.Getenv("YOLO_NIX_HOST_DAEMON"), o.Getenv(nixHostStoreLinuxEnv))
}

// nixHostStoreLinuxEnv is the macOS-only SECOND claim the delegation mount needs, and it
// exists because the first one does not imply it.
//
// YOLO_NIX_HOST_DAEMON means "my runtime VM shares /nix" — a statement about whether a
// bind SOURCE under /nix is reachable at all. That is what prefixUnreachableFromVM needs,
// and it is all it needs. It says nothing about whether the host's store is a usable
// /nix/store FOR THE JAIL, which is a different fact about a different machine: the jail
// is Linux and a Mac's store holds darwin paths.
//
// THE TWO WERE ONE VARIABLE AND IT BRICKED EVERY JAIL THAT SET IT. flake.nix builds the
// image's /bin as symlinks into the store (`ln -s ${imagePkgs.bashInteractive}/bin/bash
// $out/bin/bash`, and the same for sh/awk/sed/grep/find), so /nix/store is where every
// baked binary actually lives. Bind-mounting the host's store on top REPLACES that view,
// and the image's closure is present in the host store only if this host realized it.
// Measured on the 2026-09-13 macOS nightly (run 34778464086), whose image was built on an
// ubuntu runner and shipped as a tar: every launch died as
//
//	yolo-entrypoint: exec: "bash": executable file not found in $PATH
//
// with rg missing from the same jail and pid1 itself running fine — it is bind-mounted
// from /opt/yolo-jail/bin and never went through /nix/store.
//
// storepackages.go already made this exact argument one function over, and refuses macOS
// outright on it: "a macOS podman runs in a VM that shares no /nix/store with the host,
// and the jail's packages are Linux builds". It was never applied to the mount.
//
// SO WHY A DIAL AND NOT AN UNCONDITIONAL SKIP. A Mac CAN hold Linux store paths — a
// linux-builder VM or a substituter puts them there — and on that Mac the mount is sound
// and nested Nix in the jail works. This is the operator asserting that, which is the only
// way a launch can know it: "does the host store hold the image's closure" is not a
// question the launcher can ask cheaply, and the image it runs may have arrived as a tar
// with no store path to check (internal/image/stockimage.go).
const nixHostStoreLinuxEnv = "YOLO_NIX_HOST_STORE_LINUX"

// shouldMountHostNix decides whether run() should
// bind-mount the host's Nix daemon socket + store. Linux: mount when both paths
// exist and the runtime supports it — the host store IS the jail's system there, and it
// holds the image's closure because that host built it. macOS: skip unless the operator
// makes BOTH claims (see nixHostStoreLinuxEnv) — the VM shares /nix, and the store it
// shares holds Linux paths. Apple Container can't share Unix sockets via -v regardless.
func shouldMountHostNix(rt string, nixSocketExists, nixStoreExists, isMacOS bool, optInEnv, storeLinuxEnv string) bool {
	if !(nixSocketExists && nixStoreExists) {
		return false
	}
	if rt == "container" {
		return false
	}
	if !isMacOS {
		return true
	}
	// One spelling of "the operator said yes" across every launcher dial (envTruthy,
	// storepackages.go): two dials that disagree about what counts as true turn "I set
	// the variable and nothing happened" into a legitimate bug report.
	return envTruthy(optInEnv) && envTruthy(storeLinuxEnv)
}

// nixDelegationSkipNotice is the sentence for the ONE way this launch can disappoint
// somebody, or "" when it cannot.
//
// It is narrow on purpose. A macOS launch that never asked for nested Nix is told nothing —
// that is the default and always was. The launch that gets a line is the one that said
// YOLO_NIX_HOST_DAEMON and could reasonably have expected the mounts that used to come with
// it, because until this split they did. That is precisely the "I set the variable and
// nothing happened" report the envTruthy comment above refuses to cause, so the variable
// whose meaning NARROWED is the variable that has to say so.
//
// Not a warning: nothing is wrong. A jail without the host store is the working
// configuration — it is the one whose /bin/bash resolves.
func (o *Options) nixDelegationSkipNotice(rt string) string {
	if !o.IsMacOS || rt == "container" {
		return ""
	}
	if !envTruthy(o.Getenv("YOLO_NIX_HOST_DAEMON")) {
		return ""
	}
	if envTruthy(o.Getenv(nixHostStoreLinuxEnv)) {
		// Both claims made; the mount was emitted. Nothing skipped.
		return ""
	}
	if !(o.PathExists(hostNixSocket) && o.PathExists(hostNixStore)) {
		// Absent for a reason that has nothing to do with this split.
		return ""
	}
	return "[dim]Not mounting the host /nix/store into this jail: YOLO_NIX_HOST_DAEMON says " +
		"your VM shares /nix, which is what lets a store-path install prefix be mounted, " +
		"but this Mac's store holds darwin paths and the jail is Linux — mounting it would " +
		"hide the image's own store, where /bin/bash lives. If your store really does hold " +
		"the jail's Linux closure (a linux-builder VM, or a substituter that served it), " +
		"say so with " + nixHostStoreLinuxEnv + "=1 to get NIX_REMOTE=daemon back.[/dim]"
}

// gpuHostAvailable probes whether NVIDIA GPU
// passthrough will work. Returns (ok, reason); reason is a one-line phrase when
// not ok.
func (o *Options) gpuHostAvailable(rt string) (bool, string) {
	if o.IsMacOS || rt == "container" {
		return false, "runtime does not support NVIDIA passthrough"
	}
	if rt != "podman" {
		return false, "unsupported runtime: " + rt
	}
	smi, ok := o.LookPath("nvidia-smi")
	if !ok {
		return false, "nvidia-smi not found on host"
	}
	res := o.Exec([]string{smi, "-L"}, "", nil, 5*time.Second)
	if !res.Ran {
		return false, "nvidia-smi failed to run"
	}
	if res.Timeout || res.RC != 0 {
		return false, "nvidia-smi reported no GPUs"
	}
	if !o.PathExists("/etc/cdi/nvidia.yaml") && !o.PathExists("/var/run/cdi/nvidia.yaml") {
		return false, "no CDI spec at /etc/cdi/nvidia.yaml"
	}
	return true, ""
}

// rocmHostAvailable probes whether AMD ROCm
// passthrough will work (amdgpu module + /dev/kfd + a render node; in `mode: "cdi"` an AMD
// CDI spec; functional rocminfo when present). mode is the config's gpu.mode.
//
// A missing spec declines passthrough rather than refusing the launch: the
// warn-and-start-without-GPU every other unavailable-GPU verdict takes, NVIDIA's missing
// spec included (gpuHostAvailable). The reason carries the command that writes the spec.
func (o *Options) rocmHostAvailable(rt, mode string) (bool, string) {
	if o.IsMacOS || rt == "container" {
		return false, "runtime does not support ROCm/AMD passthrough"
	}
	if rt != "podman" {
		return false, "unsupported runtime: " + rt
	}
	if !o.PathExists("/sys/module/amdgpu") {
		return false, "amdgpu kernel module not loaded"
	}
	if !o.PathExists("/dev/kfd") {
		return false, "no /dev/kfd on host"
	}
	if !hasRenderNode(driDir) {
		return false, "no /dev/dri render node on host"
	}
	if mode == "cdi" {
		if found, searched := FindAMDCDISpec(cdiRoot, o.Getenv); found == "" {
			return false, "gpu.mode is \"cdi\" and there is no AMD CDI spec (kind " + AMDCDIKind +
				") in " + strings.Join(searched, " or ") + " (write one with `" + AMDCDISpecGenerate +
				"`, or set gpu.mode to \"devices\", which needs none)"
		}
	}
	if rocminfo, ok := o.LookPath("rocminfo"); ok {
		res := o.Exec([]string{rocminfo}, "", nil, 5*time.Second)
		if !res.Ran {
			return false, "rocminfo failed to run"
		}
		if res.Timeout || res.RC != 0 {
			return false, "rocminfo reported no GPUs"
		}
	}
	return true, ""
}

// driDir is where the launch's probe looks for render nodes. A variable only so a test can
// point the probe at a fixture directory; production never assigns it.
var driDir = "/dev/dri"

// hasRenderNode reports whether driDir has any renderD* node (glob renderD*).
func hasRenderNode(driDir string) bool {
	matches, err := filepath.Glob(filepath.Join(driDir, "renderD*"))
	return err == nil && len(matches) > 0
}

var _ = os.Stat
