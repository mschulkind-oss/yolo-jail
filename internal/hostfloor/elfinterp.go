package hostfloor

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

// elfinterp.go asks one question of a program before the floor serves it: does this machine have
// the dynamic loader the program asks for? (host-tool-provisioning.md HP-D15)
//
// A Linux program linked against glibc names its loader, its PT_INTERP, by absolute path. Node's
// official build asks for /lib64/ld-linux-x86-64.so.2 on x86-64 and /lib/ld-linux-aarch64.so.1 on
// arm64 (MEASURED for 24.21.0), and the captured claude and agy ask for the x86-64 one. A machine
// with no file at that path cannot start the program: exec fails, and the floor's launcher exits
// 127. Two kinds of Linux host are like that: NixOS without nix-ld, and a musl system such as
// Alpine. On NixOS it is harder to see, because its default `environment.stub-ld` puts a STUB at the
// loader's path: a program that prints a pointer to nix.dev/permalink/stub-ld and exits 127. So a
// check that only asks whether the loader exists misses NixOS's own default.
//
// Such a program has NO FLOOR ENTRY on that machine, so a launch refuses it and says why, with the
// step that changes it (host-notch-readiness.md HNR-D2, which replaced OQ-HE11 (a)'s PATH copy).
// The floor reads PT_INTERP itself: the header, the program headers and the string, and not
// debug/elf, which also reads the section headers at the far end of the file. It resolves the loader under Floor.Root as the kernel would,
// an absolute link target read from that root too.
//
// It checks only on a Linux floor on a Linux machine, or under a Root a test chose: a darwin
// machine testing a Linux floor has its own loaders, which say nothing about the floor's.

// elfMagic starts every ELF file.
var elfMagic = []byte{0x7f, 'E', 'L', 'F'}

// errNotELF is elfInterp's answer for a file that is not ELF: a script, a Mach-O.
var errNotELF = errors.New("not an ELF file")

// maxLinkHops bounds the links followed while resolving a loader: the Linux kernel's own limit
// (MAXSYMLINKS).
const maxLinkHops = 40

// ElfInterp returns the dynamic loader an ELF file asks for: its PT_INTERP, "" for one without (a
// static build). A file that is not ELF is errNotELF, and one too short or malformed to read is
// another error. Neither says the program needs a loader.
func ElfInterp(file string) (string, error) {
	return elfInterp(file)
}

func elfInterp(file string) (string, error) {
	fh, err := os.Open(file)
	if err != nil {
		return "", err
	}
	defer fh.Close()
	var hdr [64]byte
	n, _ := fh.ReadAt(hdr[:], 0)
	if n < len(elfMagic) || !bytes.Equal(hdr[:len(elfMagic)], elfMagic) {
		return "", errNotELF
	}
	var order binary.ByteOrder
	switch hdr[5] {
	case 1:
		order = binary.LittleEndian
	case 2:
		order = binary.BigEndian
	default:
		return "", errors.New("unknown ELF data encoding")
	}
	var phoff uint64
	var phentsize, phnum uint16
	is64 := false
	switch hdr[4] {
	case 1:
		if n < 52 {
			return "", errors.New("truncated ELF header")
		}
		phoff = uint64(order.Uint32(hdr[28:]))
		phentsize, phnum = order.Uint16(hdr[42:]), order.Uint16(hdr[44:])
		if phentsize < 32 {
			return "", fmt.Errorf("implausible program header size %d", phentsize)
		}
	case 2:
		if n < 64 {
			return "", errors.New("truncated ELF header")
		}
		is64 = true
		phoff = order.Uint64(hdr[32:])
		phentsize, phnum = order.Uint16(hdr[54:]), order.Uint16(hdr[56:])
		if phentsize < 56 {
			return "", fmt.Errorf("implausible program header size %d", phentsize)
		}
	default:
		return "", errors.New("unknown ELF class")
	}
	switch {
	case phnum == 0:
		return "", nil
	case phnum == 0xffff:
		// PN_XNUM: the real count is in the first section header, which a program the floor runs
		// has no reason to need.
		return "", errors.New("too many program headers to read")
	}
	table := make([]byte, int(phentsize)*int(phnum))
	if _, err := fh.ReadAt(table, int64(phoff)); err != nil {
		return "", fmt.Errorf("reading the program headers: %w", err)
	}
	for i := 0; i < int(phnum); i++ {
		ph := table[i*int(phentsize):]
		if order.Uint32(ph[0:]) != 3 { // PT_INTERP
			continue
		}
		var off, size uint64
		if is64 {
			off, size = order.Uint64(ph[8:]), order.Uint64(ph[32:])
		} else {
			off, size = uint64(order.Uint32(ph[4:])), uint64(order.Uint32(ph[16:]))
		}
		if size == 0 || size > 4096 {
			return "", fmt.Errorf("implausible PT_INTERP size %d", size)
		}
		s := make([]byte, size)
		if _, err := fh.ReadAt(s, int64(off)); err != nil {
			return "", fmt.Errorf("reading PT_INTERP: %w", err)
		}
		return strings.TrimRight(string(s), "\x00"), nil
	}
	return "", nil
}

// resolveUnder returns where the absolute path p leads on the filesystem rooted at root, following
// every link on the way as the kernel does: a relative target from the link's own directory, an
// absolute one from root, and `..` never above root. An error is the lookup's: a missing component
// is fs.ErrNotExist, a file where a directory should be ENOTDIR, and too many links errLinkLoop.
func resolveUnder(root, p string) (string, error) {
	root = filepath.Clean(root)
	rest := strings.Split(filepath.ToSlash(p), "/")
	cur := root
	hops := 0
	for len(rest) > 0 {
		c := rest[0]
		rest = rest[1:]
		switch c {
		case "", ".":
			continue
		case "..":
			if cur != root {
				cur = filepath.Dir(cur)
			}
			continue
		}
		next := filepath.Join(cur, c)
		fi, err := os.Lstat(next)
		if err != nil {
			return "", err
		}
		if fi.Mode()&fs.ModeSymlink == 0 {
			cur = next
			continue
		}
		if hops++; hops > maxLinkHops {
			return "", errLinkLoop
		}
		target, err := os.Readlink(next)
		if err != nil {
			return "", err
		}
		if strings.HasPrefix(target, "/") {
			cur = root
		}
		rest = append(strings.Split(target, "/"), rest...)
	}
	return cur, nil
}

// errLinkLoop is resolveUnder's answer for a chain of links longer than the kernel follows.
var errLinkLoop = errors.New("too many levels of symbolic links")

// probesLoaders reports whether this floor checks loaders at all: a Linux floor, on a Linux machine
// or under a Root a caller chose. A Mac testing a Linux floor would otherwise read its own /lib64.
func (f *Floor) probesLoaders() bool {
	return f.GOOS == "linux" && (runtime.GOOS == "linux" || f.Root != "")
}

// root is the filesystem the loader check resolves under: Root, or "/".
func (f *Floor) root() string {
	if f.Root != "" {
		return f.Root
	}
	return "/"
}

// nixLDStep is the next step a loader problem's reason ends with: what lets the machine run the
// floor's copy, and that the next launch then does.
const nixLDStep = "on NixOS, set `programs.nix-ld.enable = true;` and rebuild, and the next `yolo host` " +
	"launch runs yolo's own copy"

// loaderProblem says why this machine cannot start a program that asks for the dynamic loader
// interp, as a clause that follows the program's name ("needs the dynamic loader …"), or "" when
// it can, when interp is "" (a static build), or when this floor checks no loaders. A loader that
// cannot be looked up for another reason (a permission) is not a problem: only one that is known
// missing, or known to be NixOS's stub, takes a floor entry away.
func (f *Floor) loaderProblem(interp string) string {
	if interp == "" || !f.probesLoaders() {
		return ""
	}
	final, err := resolveUnder(f.root(), interp)
	if err == nil {
		if fi, serr := os.Stat(final); serr != nil || fi.IsDir() {
			err = fs.ErrNotExist
		}
	}
	switch {
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, syscall.ENOTDIR), errors.Is(err, errLinkLoop):
		return "needs the dynamic loader " + interp + ", and this machine has no file there (a NixOS host " +
			"without nix-ld, or a musl system) — " + nixLDStep
	case err != nil:
		return ""
	}
	// NixOS's stub-ld: the store file its `environment.stub-ld` module builds is named
	// <hash>-stub-ld, and /lib64/ld-linux-x86-64.so.2 links to it while nix-ld is off.
	if strings.HasSuffix(filepath.Base(final), "-stub-ld") {
		return "needs the dynamic loader " + interp + ", and on this machine that is NixOS's stub loader (" +
			final + "), which starts no program — " + nixLDStep
	}
	return ""
}

// programLoaderProblem is loaderProblem for the program file at file, its links resolved: "" for
// one that is not ELF, that cannot be read, or that asks for no loader.
func (f *Floor) programLoaderProblem(file string) string {
	if !f.probesLoaders() {
		return ""
	}
	real, err := filepath.EvalSymlinks(file)
	if err != nil {
		return ""
	}
	interp, err := elfInterp(real)
	if err != nil {
		return ""
	}
	return f.loaderProblem(interp)
}
