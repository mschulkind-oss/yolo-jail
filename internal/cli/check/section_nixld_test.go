package check

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type nixLDTestOpts struct {
	InJail       bool
	Node         string
	MiseBinaries []string
	MergedGlibc  string
	Exec         func(argv []string) ExecResult
}

func runNixLDSectionWithOptions(t *testing.T, opts nixLDTestOpts) string {
	t.Helper()
	var out bytes.Buffer
	getenv := func(k string) string {
		if k == "YOLO_VERSION" && opts.InJail {
			return "9.9.9-test"
		}
		return ""
	}
	execFn := func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		if opts.Exec != nil {
			return opts.Exec(argv)
		}
		return ExecResult{Ran: false}
	}
	o := &Options{
		Getenv:      getenv,
		MiseNode:    func() string { return opts.Node },
		Exec:        execFn,
		Stdout:      &out,
		IsTTYStdout: func() bool { return false },
	}
	fillDefaults(o)
	// fillDefaults installs real seams for nil fields; re-pin what we control
	// so the real glob/env don't leak in.
	o.Getenv = getenv
	o.MiseNode = func() string { return opts.Node }
	o.MiseBinaries = func() []string { return opts.MiseBinaries }
	o.MergedGlibc = func() string { return opts.MergedGlibc }
	o.Exec = execFn

	r := newReporter(&out, false)
	o.sectionNixLD(r)
	return out.String()
}

// runNixLDSection wires a minimal Options for the nix-ld section alone: in-jail
// (YOLO_VERSION set) unless overridden, an injected MiseNode, and an Exec stub
// that returns the given result for all exec probes.
func runNixLDSection(t *testing.T, inJail bool, node string, probe ExecResult) string {
	t.Helper()
	return runNixLDSectionWithOptions(t, nixLDTestOpts{
		InJail: inJail,
		Node:   node,
		Exec:   func([]string) ExecResult { return probe },
	})
}

func TestNixLDSectionEnvFreePass(t *testing.T) {
	got := runNixLDSection(t, true, "/mise/installs/node/22.20.0/bin/node",
		ExecResult{Ran: true, RC: 0, Stdout: "v22.20.0\n"})
	if !strings.Contains(got, "FHS loader (nix-ld)") {
		t.Fatalf("expected section header, got:\n%s", got)
	}
	if !strings.Contains(got, "runs env-free: v22.20.0 (nix-ld OK)") {
		t.Errorf("expected env-free PASS line, got:\n%s", got)
	}
}

func TestNixLDSectionRegressionFails(t *testing.T) {
	got := runNixLDSection(t, true, "/mise/installs/node/22.20.0/bin/node",
		ExecResult{Ran: true, RC: 1,
			Stderr: "node: error while loading shared libraries: libstdc++.so.6: cannot open shared object file: No such file or directory\n"})
	if !strings.Contains(got, "fails under a scrubbed environment") {
		t.Errorf("expected regression FAIL line, got:\n%s", got)
	}
	if !strings.Contains(got, "libstdc++.so.6") {
		t.Errorf("expected the loader error detail in the message, got:\n%s", got)
	}
	if !strings.Contains(got, "nix-ld") {
		t.Errorf("expected a nix-ld remedy note, got:\n%s", got)
	}
}

func TestNixLDSectionAmbientRegressionFails(t *testing.T) {
	// Env-free passes, but ambient environment probe fails
	got := runNixLDSectionWithOptions(t, nixLDTestOpts{
		InJail: true,
		Node:   "/mise/installs/node/22.20.0/bin/node",
		Exec: func(argv []string) ExecResult {
			if len(argv) >= 2 && argv[0] == "env" && argv[1] == "-i" {
				return ExecResult{Ran: true, RC: 0, Stdout: "v22.20.0\n"}
			}
			return ExecResult{
				Ran:    true,
				RC:     1,
				Stderr: "node: symbol lookup error: /lib/libc.so.6: undefined symbol: __pointer_chk_guard, version GLIBC_PRIVATE\n",
			}
		},
	})
	if !strings.Contains(got, "runs env-free: v22.20.0 (nix-ld OK)") {
		t.Errorf("expected env-free PASS line, got:\n%s", got)
	}
	if !strings.Contains(got, "fails under the jail's actual environment") {
		t.Errorf("expected ambient FAIL line, got:\n%s", got)
	}
	if !strings.Contains(got, "GLIBC_PRIVATE") {
		t.Errorf("expected GLIBC_PRIVATE in detail, got:\n%s", got)
	}
}

func TestNixLDSectionMiseBinaryGlibcMismatchWarns(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	binPath := writeSyntheticELF(t, dir, "staticcheck",
		"/nix/store/8kvxvr3pmsypxiypq4g8zy13glnfr7nx-glibc-2.42-67/lib/ld-linux-x86-64.so.2")

	got := runNixLDSectionWithOptions(t, nixLDTestOpts{
		InJail:       true,
		Node:         "/mise/installs/node/22.20.0/bin/node",
		MiseBinaries: []string{binPath},
		MergedGlibc:  "/nix/store/h4wfwic161kxrr74jlzla5lsm28hgary-glibc-2.44-25",
		Exec: func(argv []string) ExecResult {
			for _, a := range argv {
				if strings.HasSuffix(a, "node") {
					return ExecResult{Ran: true, RC: 0, Stdout: "v22.20.0\n"}
				}
			}
			return ExecResult{Ran: true, RC: 0, Stdout: "staticcheck 2026.1\n"}
		},
	})

	if !strings.Contains(got, "runs env-free: v22.20.0 (nix-ld OK)") {
		t.Errorf("expected env-free PASS line, got:\n%s", got)
	}
	if !strings.Contains(got, "mise staticcheck interpreter glibc differs from merged tree: glibc-2.42-67 (tool) vs glibc-2.44-25 (image)") {
		t.Errorf("expected glibc skew warning, got:\n%s", got)
	}
	if strings.Contains(got, "fails in jail environment") {
		t.Errorf("expected tool not to fail when exec succeeds, got:\n%s", got)
	}
}

func TestNixLDSectionMiseBinaryAmbientExecutionFails(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	binPath := writeSyntheticELF(t, dir, "staticcheck",
		"/nix/store/8kvxvr3pmsypxiypq4g8zy13glnfr7nx-glibc-2.42-67/lib/ld-linux-x86-64.so.2")

	got := runNixLDSectionWithOptions(t, nixLDTestOpts{
		InJail:       true,
		Node:         "/mise/installs/node/22.20.0/bin/node",
		MiseBinaries: []string{binPath},
		MergedGlibc:  "/nix/store/h4wfwic161kxrr74jlzla5lsm28hgary-glibc-2.44-25",
		Exec: func(argv []string) ExecResult {
			for _, a := range argv {
				if strings.HasSuffix(a, "node") {
					return ExecResult{Ran: true, RC: 0, Stdout: "v22.20.0\n"}
				}
			}
			return ExecResult{
				Ran:    true,
				RC:     127,
				Stderr: "staticcheck: symbol lookup error: /lib/libc.so.6: undefined symbol: __pointer_chk_guard, version GLIBC_PRIVATE\n",
			}
		},
	})

	if !strings.Contains(got, "mise staticcheck interpreter glibc differs from merged tree") {
		t.Errorf("expected glibc skew warning, got:\n%s", got)
	}
	if !strings.Contains(got, "mise staticcheck fails in jail environment: staticcheck: symbol lookup error") {
		t.Errorf("expected symbol lookup FAIL line, got:\n%s", got)
	}
}

func TestNixStorePackageDir(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"/nix/store/h4wfwic161kxrr74jlzla5lsm28hgary-glibc-2.44-25/lib/ld-linux-x86-64.so.2", "/nix/store/h4wfwic161kxrr74jlzla5lsm28hgary-glibc-2.44-25"},
		{"/nix/store/8kvxvr3pmsypxiypq4g8zy13glnfr7nx-glibc-2.42-67", "/nix/store/8kvxvr3pmsypxiypq4g8zy13glnfr7nx-glibc-2.42-67"},
		{"/lib64/ld-linux-x86-64.so.2", ""},
		{"/usr/lib/libc.so.6", ""},
		{"", ""},
	}
	for _, tc := range cases {
		got := nixStorePackageDir(tc.in)
		if got != tc.want {
			t.Errorf("nixStorePackageDir(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNixLDSectionSkippedOnHost(t *testing.T) {
	got := runNixLDSection(t, false, "/mise/installs/node/22.20.0/bin/node",
		ExecResult{Ran: true, RC: 0, Stdout: "v22.20.0\n"})
	if strings.Contains(got, "FHS loader (nix-ld)") {
		t.Errorf("section must be skipped on the host, got:\n%s", got)
	}
}

func TestNixLDSectionSkippedWithoutMiseNode(t *testing.T) {
	got := runNixLDSection(t, true, "", ExecResult{Ran: false})
	if strings.Contains(got, "FHS loader (nix-ld)") {
		t.Errorf("section must be skipped when no mise node is installed, got:\n%s", got)
	}
}

func TestNixLDSectionTimeout(t *testing.T) {
	got := runNixLDSection(t, true, "/mise/installs/node/22.20.0/bin/node",
		ExecResult{Ran: true, Timeout: true})
	if !strings.Contains(got, "timed out") {
		t.Errorf("expected a timeout FAIL line, got:\n%s", got)
	}
}

func writeSyntheticELF(t *testing.T, dir, name, interp string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	ehsize := 64
	phentsize := 56
	phnum := 1
	if interp != "" {
		phnum = 2
	}
	interpOff := ehsize + phnum*phentsize
	body := append([]byte(interp), 0)
	buf := make([]byte, interpOff+len(body))
	order := binary.LittleEndian

	copy(buf, []byte{0x7f, 'E', 'L', 'F', 2 /*ELFCLASS64*/, 1 /*ELFDATA2LSB*/, 1 /*EV_CURRENT*/})
	order.PutUint16(buf[16:], 2)              // ET_EXEC
	order.PutUint16(buf[18:], 62)             // EM_X86_64
	order.PutUint32(buf[20:], 1)              // EV_CURRENT
	order.PutUint64(buf[32:], uint64(ehsize)) // e_phoff
	order.PutUint16(buf[52:], uint16(ehsize))
	order.PutUint16(buf[54:], uint16(phentsize))
	order.PutUint16(buf[56:], uint16(phnum))

	ph := func(i int, typ uint32, off, size uint64) {
		p := buf[ehsize+i*phentsize:]
		order.PutUint32(p[0:], typ)
		order.PutUint32(p[4:], 4) // PF_R
		order.PutUint64(p[8:], off)
		order.PutUint64(p[32:], size)
		order.PutUint64(p[40:], size)
		order.PutUint64(p[48:], 1)
	}
	ph(0, 1 /*PT_LOAD*/, 0, uint64(len(buf)))
	if interp != "" {
		ph(1, 3 /*PT_INTERP*/, uint64(interpOff), uint64(len(body)))
	}
	copy(buf[interpOff:], body)

	if err := os.WriteFile(p, buf, 0o755); err != nil {
		t.Fatalf("writeSyntheticELF: %v", err)
	}
	return p
}
