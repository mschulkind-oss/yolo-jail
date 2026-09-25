package run

// retire_bench_test.go is MEASUREMENT ONLY, for OQ-WP9
// (docs/design/workspace-path-mirroring.md): is reading the ELF program interpreter
// (PT_INTERP) of the Python a venv's pyvenv.cfg names cheap enough to add to
// retireJailMadeVenv, which runs on every fresh-container launch? Nothing here is wired
// into production — the readers below live in this file on purpose, so the benchmark
// ships no behavior change and the ruling stays the maintainer's.
//
// Three benchmarks, so the answer is a MARGINAL cost against a measured baseline rather
// than an absolute number with nothing beside it:
//
//	BenchmarkRetireJailMadeVenvToday   the production function, whole, on a fixture
//	                                   workspace: what every fresh launch pays now
//	BenchmarkVenvELFInterpMinimal      the added probe with a hand-rolled reader: the ELF
//	                                   header, the program headers, the interpreter string
//	BenchmarkVenvELFInterpDebugELF     the same probe through the standard library's
//	                                   debug/elf, which also reads the section headers
//
// The fixture interpreter is a synthetic ELF64 by default, so the benchmark is hermetic.
// YOLO_BENCH_ELF=<path> points the fixture's `home` at a real binary instead (the doc's
// numbers come from the jail's own mise Pythons, whose section headers sit at the END of
// a 52–114 MB file). Page cache is warm in every run; a cold-cache number needs root to
// drop caches and is recorded in the doc as not measured.

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// syntheticInterp is the interpreter the synthetic fixture requests: the generic x86-64
// loader path, which is also what the jail's mise-installed (python-build-standalone)
// Pythons request.
const syntheticInterp = "/lib64/ld-linux-x86-64.so.2"

// syntheticELF returns a minimal little-endian ELF64 executable whose only program header
// is a PT_INTERP naming interp. No sections, so debug/elf has nothing extra to read — the
// real-binary run is where the section-header cost shows.
func syntheticELF(interp string) []byte {
	const ehsize, phentsize = 64, 56
	le := binary.LittleEndian
	interpOff := uint64(ehsize + phentsize)
	body := append([]byte(interp), 0)
	buf := make([]byte, int(interpOff)+len(body))
	copy(buf, []byte{0x7f, 'E', 'L', 'F', 2 /*ELFCLASS64*/, 1 /*LE*/, 1 /*EV_CURRENT*/})
	le.PutUint16(buf[16:], 2)  // e_type ET_EXEC
	le.PutUint16(buf[18:], 62) // e_machine EM_X86_64
	le.PutUint32(buf[20:], 1)  // e_version
	le.PutUint64(buf[32:], ehsize)
	le.PutUint16(buf[52:], ehsize)
	le.PutUint16(buf[54:], phentsize)
	le.PutUint16(buf[56:], 1) // e_phnum
	le.PutUint16(buf[58:], 64)
	ph := buf[ehsize:]
	le.PutUint32(ph[0:], 3) // PT_INTERP
	le.PutUint32(ph[4:], 4) // PF_R
	le.PutUint64(ph[8:], interpOff)
	le.PutUint64(ph[32:], uint64(len(body)))
	le.PutUint64(ph[40:], uint64(len(body)))
	le.PutUint64(ph[48:], 1)
	copy(buf[interpOff:], body)
	return buf
}

var errNoInterp = errors.New("no PT_INTERP program header")

// elfInterpMinimal reads PT_INTERP with the fewest reads the format allows: the 64-byte
// identification+header, the program-header table (at e_phoff, right after it in every
// binary measured), and the interpreter string. The string is NOT always near the front:
// in the jail's mise Python 3.11.14 it sits 52 MB into the file, so a cold cache costs
// two page reads, not one.
func elfInterpMinimal(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	var hdr [64]byte
	if n, err := f.ReadAt(hdr[:], 0); n < 52 {
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		return "", err
	}
	if !bytes.Equal(hdr[:4], []byte{0x7f, 'E', 'L', 'F'}) {
		return "", errors.New("not an ELF file")
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
		phoff = uint64(order.Uint32(hdr[28:]))
		phentsize, phnum = order.Uint16(hdr[42:]), order.Uint16(hdr[44:])
	case 2:
		is64 = true
		phoff = order.Uint64(hdr[32:])
		phentsize, phnum = order.Uint16(hdr[54:]), order.Uint16(hdr[56:])
	default:
		return "", errors.New("unknown ELF class")
	}
	if phnum == 0 || phentsize < 32 {
		return "", errNoInterp
	}
	table := make([]byte, int(phentsize)*int(phnum))
	if _, err := f.ReadAt(table, int64(phoff)); err != nil {
		return "", err
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
		if _, err := f.ReadAt(s, int64(off)); err != nil {
			return "", err
		}
		return strings.TrimRight(string(s), "\x00"), nil
	}
	return "", errNoInterp
}

// elfInterpDebugELF is the same question through debug/elf. elf.Open parses the section
// header table as well, which for a large binary is a second read at the far end of the
// file.
func elfInterpDebugELF(path string) (string, error) {
	f, err := elf.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	for _, p := range f.Progs {
		if p.Type != elf.PT_INTERP {
			continue
		}
		s := make([]byte, p.Filesz)
		if _, err := p.ReadAt(s, 0); err != nil {
			return "", err
		}
		return strings.TrimRight(string(s), "\x00"), nil
	}
	return "", errNoInterp
}

// venvInterpreter is the probe's front half: pyvenv.cfg's `home =` (parsed exactly as
// retireJailMadeVenv parses it) joined with the interpreter's name. uv records no
// `executable =`, so the name is guessed: python3, then python.
func venvInterpreter(venvDir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(venvDir, "pyvenv.cfg"))
	if err != nil {
		return "", err
	}
	home := ""
	for _, line := range strings.Split(string(data), "\n") {
		key, val, found := strings.Cut(line, "=")
		if found && strings.TrimSpace(key) == "home" {
			home = strings.TrimSpace(val)
			break
		}
	}
	if home == "" {
		return "", errors.New("pyvenv.cfg has no home")
	}
	for _, name := range []string{"python3", "python"} {
		if p := filepath.Join(home, name); fileExists(p) {
			return p, nil
		}
	}
	return "", errors.New("no python3 or python under " + home)
}

// retireBenchFixture builds a workspace with one .venv whose `home` is a jail-flavored
// directory that EXISTS — under $HOME/.local/share/mise, one of retireJailMadeVenv's own
// prefixes — so the production function runs every step (read, parse, prefix, stat) and
// then keeps the venv, which is the path a healthy workspace takes on every launch. The
// interpreter in it is the synthetic ELF, or a symlink to YOLO_BENCH_ELF.
func retireBenchFixture(tb testing.TB) (o *Options, venvDir string) {
	tb.Helper()
	home := tb.TempDir()
	tb.Setenv("HOME", home)
	bin := filepath.Join(home, ".local", "share", "mise", "installs", "python", "3.13", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		tb.Fatal(err)
	}
	interp := filepath.Join(bin, "python3")
	if real := os.Getenv("YOLO_BENCH_ELF"); real != "" {
		if err := os.Symlink(real, interp); err != nil {
			tb.Fatal(err)
		}
	} else if err := os.WriteFile(interp, syntheticELF(syntheticInterp), 0o755); err != nil {
		tb.Fatal(err)
	}
	ws := tb.TempDir()
	venvDir = filepath.Join(ws, ".venv")
	if err := os.MkdirAll(venvDir, 0o755); err != nil {
		tb.Fatal(err)
	}
	cfg := "home = " + bin + "\nimplementation = CPython\nuv = 0.11.27\nversion_info = 3.13.14\n" +
		"include-system-site-packages = false\n"
	if err := os.WriteFile(filepath.Join(venvDir, "pyvenv.cfg"), []byte(cfg), 0o644); err != nil {
		tb.Fatal(err)
	}
	o = &Options{
		Workspace: ws,
		Stdout:    io.Discard,
		Stderr:    io.Discard,
		Getenv:    func(string) string { return "" }, // not in a jail, so the function runs
	}
	return o, venvDir
}

// TestRetireBenchFixtureIsMeasuringTheRealPath keeps the benchmarks honest: the readers
// agree with each other and return the fixture's interpreter, a non-ELF file is refused
// rather than read as an empty interpreter, and the production function keeps the fixture
// venv — so BenchmarkRetireJailMadeVenvToday times the full keep path and never an early
// return or a deletion. The keep alone cannot prove "never an early return" (a function
// that returned at once would keep it too), so a control case follows: the same fixture,
// its `home` repointed at a jail-prefixed path that does NOT exist, must be deleted — which
// only happens if the function gets past its in-jail guard and reaches the prefix-and-stat
// step under this fixture's HOME and Getenv.
func TestRetireBenchFixtureIsMeasuringTheRealPath(t *testing.T) {
	o, venv := retireBenchFixture(t)
	py, err := venvInterpreter(venv)
	if err != nil {
		t.Fatalf("venvInterpreter: %v", err)
	}
	minimal, err := elfInterpMinimal(py)
	if err != nil {
		t.Fatalf("elfInterpMinimal(%s): %v", py, err)
	}
	viaStd, err := elfInterpDebugELF(py)
	if err != nil {
		t.Fatalf("elfInterpDebugELF(%s): %v", py, err)
	}
	if minimal != viaStd {
		t.Errorf("readers disagree: minimal %q, debug/elf %q", minimal, viaStd)
	}
	if os.Getenv("YOLO_BENCH_ELF") == "" && minimal != syntheticInterp {
		t.Errorf("interpreter = %q, want %q", minimal, syntheticInterp)
	}

	script := filepath.Join(t.TempDir(), "python3")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexec python3 \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := elfInterpMinimal(script); err == nil {
		t.Errorf("elfInterpMinimal read %q out of a shell script; want an error", got)
	}
	if got, err := elfInterpDebugELF(script); err == nil {
		t.Errorf("elfInterpDebugELF read %q out of a shell script; want an error", got)
	}

	o.retireJailMadeVenv(nil)
	if _, err := os.Stat(filepath.Join(venv, "pyvenv.cfg")); err != nil {
		t.Fatalf("retireJailMadeVenv removed the fixture venv (its home exists), so the "+
			"baseline benchmark would time a deletion: %v", err)
	}

	// Control: the same fixture with a missing jail-prefixed home must be retired.
	cfgPath := filepath.Join(venv, "pyvenv.cfg")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Dir(py)
	missing := home + "-missing"
	if !strings.Contains(string(data), "home = "+home+"\n") {
		t.Fatalf("fixture pyvenv.cfg does not record home %q:\n%s", home, data)
	}
	repointed := strings.Replace(string(data), "home = "+home+"\n", "home = "+missing+"\n", 1)
	if err := os.WriteFile(cfgPath, []byte(repointed), 0o644); err != nil {
		t.Fatal(err)
	}
	o.retireJailMadeVenv(nil)
	if _, err := os.Stat(venv); !os.IsNotExist(err) {
		t.Fatalf("retireJailMadeVenv kept a venv whose jail-prefixed home %s does not exist "+
			"(stat err %v): under this fixture it returns before its prefix-and-stat step, "+
			"so the baseline benchmark would time an early return", missing, err)
	}
}

func BenchmarkRetireJailMadeVenvToday(b *testing.B) {
	o, venv := retireBenchFixture(b)
	b.ReportAllocs()
	for b.Loop() {
		o.retireJailMadeVenv(nil)
	}
	b.StopTimer()
	if _, err := os.Stat(filepath.Join(venv, "pyvenv.cfg")); err != nil {
		b.Fatalf("the fixture venv was deleted mid-benchmark: %v", err)
	}
}

func benchmarkVenvELFInterp(b *testing.B, read func(string) (string, error)) {
	_, venv := retireBenchFixture(b)
	b.ReportAllocs()
	for b.Loop() {
		py, err := venvInterpreter(venv)
		if err != nil {
			b.Fatal(err)
		}
		interp, err := read(py)
		if err != nil || interp == "" {
			b.Fatalf("read %s: %q, %v", py, interp, err)
		}
	}
}

func BenchmarkVenvELFInterpMinimal(b *testing.B) { benchmarkVenvELFInterp(b, elfInterpMinimal) }

func BenchmarkVenvELFInterpDebugELF(b *testing.B) { benchmarkVenvELFInterp(b, elfInterpDebugELF) }
