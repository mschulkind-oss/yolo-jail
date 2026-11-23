package perf

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/testtmp"
)

func traceStoreFixture(t *testing.T) (*TraceStore, string, string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root, private := filepath.Join(base, "traces"), filepath.Join(base, "private")
	s, err := OpenTraceStore(root, private)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, root, private
}

func storeMeta(n int, start int64) TraceMetadata {
	return TraceMetadata{Trace: fmt.Sprintf("%032x", n), Kind: "launch", Start: start, Workspace: "ab12", Version: "test", Commit: "test"}
}

func storeMint(t *testing.T, s *TraceStore, n int, start int64) *TraceClaim {
	t.Helper()
	c, err := s.Mint(storeMeta(n, start))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func storeWrite(t *testing.T, c *TraceClaim, name string, p []byte) *TraceWriter {
	t.Helper()
	w, err := c.CreateWriter(name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	if n, err := w.Write(p); err != nil || n != len(p) {
		t.Fatalf("write %d/%d: %v", n, len(p), err)
	}
	return w
}

func assertTraceStoreClosePrivacy(t *testing.T, err error, roots ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("failed owned descriptor close was reported successful")
	}
	for _, root := range roots {
		if strings.Contains(err.Error(), root) {
			t.Error("close error exposed an owned fixture locator")
		}
	}
	var pathError *os.PathError
	if errors.As(err, &pathError) {
		t.Error("close error retained an extractable PathError")
	}
	if errors.Unwrap(err) != nil {
		t.Error("close error retained an underlying cause")
	}
	if _, ok := err.(interface{ Unwrap() []error }); ok {
		t.Error("close error retained joined underlying causes")
	}
	if err.Error() != traceDeclined().Error() {
		t.Error("close error omitted the bounded store category and actionable next step")
	}
}

func TestTraceStorePendingAdmissionAndPhysicalReservation(t *testing.T) {
	t.Run("global_exact_boundary", func(t *testing.T) {
		s, root, _ := traceStoreFixture(t)
		m := storeMeta(1, 123)
		c, err := s.MintPending(m.Trace, m.Start)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		usage, err := s.Usage()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, ".charged-control"), bytes.Repeat([]byte("x"), int(MaxStoreContentBytes-usage.Bytes)), 0o600); err != nil {
			t.Fatal(err)
		}
		w, err := c.CreateWriter("keeper-1.jsonl")
		if err != nil {
			t.Fatal(err)
		}
		defer w.Close()
		if _, err := w.Write([]byte("x")); err == nil {
			t.Fatal("global cap exceeded")
		}
		if _, err := c.PublishPending(m); err != nil {
			t.Fatal("reserved publication at global cap", err)
		}
		usage, err = s.Usage()
		if err != nil || usage.Bytes != MaxStoreContentBytes {
			t.Fatal("physical global cap", usage, err)
		}
	})
	s, root, _ := traceStoreFixture(t)
	meta := storeMeta(1, 123)
	c, err := s.MintPending(meta.Trace, meta.Start)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	dir := filepath.Join(root, meta.Trace)
	for name, size := range map[string]int64{".meta-slot": 452, ".publication-seal": 33} {
		i, err := os.Stat(filepath.Join(dir, name))
		if err != nil || i.Size() != size {
			t.Fatalf("%s: %v %v", name, i, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "meta.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("pending published metadata")
	}
	control, err := pendingControl(c.dir, c.ID())
	if err != nil || control.Start != meta.Start || control.Trace != meta.Trace || !control.LauncherLane || control.MetaSlotBytes != 452 {
		t.Fatal("immutable pending registration", control, err)
	}
	for name, identity := range map[string][2]uint64{".meta-slot": control.MetaSlot, ".publication-seal": control.SealSlot} {
		i, err := c.dir.Lstat(name)
		if err != nil || inode(i) != identity {
			t.Fatal("registered control inode", name, err)
		}
	}
	usage, err := c.Usage()
	if err != nil {
		t.Fatal(err)
	}
	physical, err := storeTreeBytes(c.dir)
	if err != nil || usage.Bytes != physical {
		t.Fatalf("usage %v physical %d: %v", usage, physical, err)
	}
	joined, err := s.Join(meta.Trace)
	if err != nil {
		t.Fatal(err)
	}
	defer joined.Close()
	if _, err := joined.PublishPending(meta); err == nil {
		t.Fatal("joined publisher")
	}
	if _, err := joined.CreateLauncherWriter(); err == nil {
		t.Fatal("joined launcher")
	}
	if _, err := c.CreateWriter(TraceFileName("launcher", os.Getpid())); err == nil {
		t.Fatal("generic launcher")
	}
	w, err := c.CreateWriter("keeper-1.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if _, err := w.Write(bytes.Repeat([]byte("x"), int(MaxTraceContentBytes-usage.Bytes))); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("x")); err == nil {
		t.Fatal("slot consumed at exact boundary")
	}
	if _, err := c.PublishPending(meta); err != nil {
		t.Fatal("reserved publication at cap", err)
	}
	usage, err = c.Usage()
	if err != nil || usage.Bytes != MaxTraceContentBytes {
		t.Fatalf("published cap: %v %v", usage, err)
	}
}

func TestTraceStorePendingPublicationOnceAndConflict(t *testing.T) {
	for _, kind := range []string{"launch", "attach", "incomplete"} {
		t.Run(kind, func(t *testing.T) {
			s, root, _ := traceStoreFixture(t)
			m := storeMeta(1, 123)
			m.Kind = kind
			c, err := s.MintPending(m.Trace, m.Start)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			result, err := c.PublishPending(m)
			if err != nil || result.State != TracePublished {
				t.Fatalf("publish: %v %v", result, err)
			}
			path := filepath.Join(root, m.Trace, "meta.json")
			before, err := os.ReadFile(path)
			if err != nil || len(before) != 452 {
				t.Fatal(err, len(before))
			}
			info, _ := os.Stat(path)
			result, err = c.PublishPending(m)
			if err != nil || result.State != TraceAlreadyPublished {
				t.Fatalf("repeat %v %v", result, err)
			}
			m.Kind = "attach"
			if kind == "attach" {
				m.Kind = "launch"
			}
			result, err = c.PublishPending(m)
			if err == nil || result.State != TracePublicationConflict {
				t.Fatalf("conflict %v %v", result, err)
			}
			after, _ := os.ReadFile(path)
			now, _ := os.Stat(path)
			if !bytes.Equal(before, after) || !os.SameFile(info, now) {
				t.Fatal("metadata changed")
			}
		})
	}
	t.Run("rename_no_overwrite", func(t *testing.T) {
		s, root, _ := traceStoreFixture(t)
		m := storeMeta(1, 123)
		c, err := s.MintPending(m.Trace, m.Start)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		path := filepath.Join(root, m.Trace, "meta.json")
		calls := 0
		c.publicationObserve = func(stage string, _ *os.File) {
			if stage == "rename" {
				calls++
				if err := os.WriteFile(path, []byte("sentinel"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
		}
		if _, err := c.PublishPending(m); err == nil {
			t.Fatal("atomic rename overwrote target")
		}
		if _, err := c.PublishPending(m); err == nil || calls != 1 {
			t.Fatal("conflicting rename retried")
		}
		got, err := os.ReadFile(path)
		if err != nil || string(got) != "sentinel" {
			t.Fatal("rename changed existing object")
		}
		if _, err := s.Usage(); err == nil {
			t.Fatal("coexisting slot and public metadata adopted")
		}
	})
	t.Run("no_overwrite", func(t *testing.T) {
		s, root, _ := traceStoreFixture(t)
		m := storeMeta(1, 123)
		c, err := s.MintPending(m.Trace, m.Start)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		path := filepath.Join(root, m.Trace, "meta.json")
		if err := os.WriteFile(path, []byte("sentinel"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := c.PublishPending(m); err == nil {
			t.Fatal("overwrote conflict")
		}
		got, _ := os.ReadFile(path)
		if string(got) != "sentinel" {
			t.Fatal("changed conflict")
		}
	})
}

func TestTraceStorePendingReplaceCannotAllocateReservedLauncher(t *testing.T) {
	for _, actor := range []string{"original", "joined"} {
		for _, lane := range []string{"absent", "closed"} {
			for _, state := range []string{"pending", "published"} {
				t.Run(actor+"/"+lane+"/"+state, func(t *testing.T) {
					s, root, _ := traceStoreFixture(t)
					m := storeMeta(1, 123)
					owner, err := s.MintPending(m.Trace, m.Start)
					if err != nil {
						t.Fatal(err)
					}
					defer owner.Close()
					joined, err := s.Join(m.Trace)
					if err != nil {
						t.Fatal(err)
					}
					defer joined.Close()
					name := TraceFileName("launcher", os.Getpid())
					if lane == "closed" {
						w, err := owner.CreateLauncherWriter()
						if err != nil {
							t.Fatal(err)
						}
						if _, err := w.Write([]byte("original lane\n")); err != nil {
							t.Fatal(err)
						}
						if err := w.Close(); err != nil {
							t.Fatal(err)
						}
					}
					if state == "published" {
						if _, err := owner.PublishPending(m); err != nil {
							t.Fatal(err)
						}
					}
					snapshot := func() string {
						entries, err := os.ReadDir(filepath.Join(root, m.Trace))
						if err != nil {
							t.Fatal(err)
						}
						var out strings.Builder
						for _, entry := range entries {
							path := filepath.Join(root, m.Trace, entry.Name())
							info, err := os.Lstat(path)
							if err != nil {
								t.Fatal(err)
							}
							p, err := os.ReadFile(path)
							if err != nil {
								t.Fatal(err)
							}
							fmt.Fprintf(&out, "%s:%v:%x;", entry.Name(), inode(info), p)
						}
						return out.String()
					}
					before := snapshot()
					usage, err := s.Usage()
					if err != nil {
						t.Fatal(err)
					}
					claim := owner
					if actor == "joined" {
						claim = joined
					}
					if err := claim.Replace(name, []byte("unauthorized replacement\n")); err == nil {
						t.Error("public Replace granted reserved launcher authority")
					}
					afterUsage, err := s.Usage()
					if err != nil || afterUsage != usage || snapshot() != before {
						t.Error("refused Replace changed content, inode, allocation or charge", err)
					}
					entries, err := os.ReadDir(filepath.Join(root, m.Trace))
					if err != nil {
						t.Fatal(err)
					}
					for _, entry := range entries {
						if strings.HasPrefix(entry.Name(), ".replace-") {
							t.Error("refused Replace allocated temporary content")
						}
					}
					if lane == "absent" {
						w, err := owner.CreateLauncherWriter()
						if err != nil {
							t.Fatal("refused Replace consumed owner allocation", err)
						}
						if err := w.Close(); err != nil {
							t.Fatal(err)
						}
					}
					if err := joined.ReplacePrivate(name, []byte("private snapshot")); err != nil {
						t.Fatal("private replacement compatibility", err)
					}
				})
			}
		}
	}
	t.Run("immediate_mint", func(t *testing.T) {
		s, root, _ := traceStoreFixture(t)
		c := storeMint(t, s, 1, 123)
		name := TraceFileName("launcher", os.Getpid())
		for _, body := range []string{"first", "second"} {
			if err := c.Replace(name, []byte(body)); err != nil {
				t.Fatal("immediate replacement compatibility", err)
			}
			p, err := os.ReadFile(filepath.Join(root, c.ID(), name))
			if err != nil || string(p) != body {
				t.Fatal("immediate replacement content", err)
			}
		}
	})
}

func TestTraceStorePendingPublicationRetainsOriginalRegistration(t *testing.T) {
	for _, binding := range []string{"seal", "metadata"} {
		t.Run(binding, func(t *testing.T) {
			s, root, _ := traceStoreFixture(t)
			m := storeMeta(1, 123)
			c, err := s.MintPending(m.Trace, m.Start)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			original := *c.pendingControl
			dir := filepath.Join(root, m.Trace)
			calls := 0
			var beforeSwap int64
			c.publicationObserve = func(stage string, _ *os.File) {
				if stage != "directory.close" {
					return
				}
				calls++
				beforeSwap, err = storeTreeBytes(c.dir)
				if err != nil {
					t.Fatal(err)
				}
				name := ".publication-seal"
				if binding == "metadata" {
					name = "meta.json"
				}
				path := filepath.Join(dir, name)
				p, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(path, filepath.Join(dir, ".retained-original")); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, p, 0o600); err != nil {
					t.Fatal(err)
				}
				info, err := os.Lstat(path)
				if err != nil {
					t.Fatal(err)
				}
				control := original
				if binding == "seal" {
					control.SealSlot = inode(info)
					if control.SealSlot == original.SealSlot {
						t.Fatal("fixture did not substitute seal inode")
					}
				} else {
					control.MetaSlot = inode(info)
					if control.MetaSlot == original.MetaSlot {
						t.Fatal("fixture did not substitute metadata inode")
					}
				}
				p, err = json.Marshal(control)
				if err != nil || os.WriteFile(filepath.Join(dir, ".pending"), p, 0o600) != nil {
					t.Fatal("fixture registration update", err)
				}
			}
			result, err := c.PublishPending(m)
			if err == nil || result.State != TracePublicationUnavailable {
				t.Error("publication adopted substituted original binding", result, err)
			}
			public, err := os.ReadFile(filepath.Join(dir, "meta.json"))
			if err != nil || len(public) != metadataSlotBytes {
				t.Fatal("uncertain public bytes were removed", err)
			}
			publicInfo, err := os.Lstat(filepath.Join(dir, "meta.json"))
			if err != nil {
				t.Fatal(err)
			}
			seal, err := os.ReadFile(filepath.Join(dir, ".publication-seal"))
			if err != nil {
				t.Fatal(err)
			}
			control, err := os.ReadFile(filepath.Join(dir, ".pending"))
			if err != nil {
				t.Fatal(err)
			}
			physical, err := storeTreeBytes(c.dir)
			if err != nil || physical <= beforeSwap {
				t.Fatal("uncertain old/new objects lost physical charge", err)
			}
			usage, err := s.Usage()
			if err != nil || usage.Bytes != physical {
				t.Fatal("replacement binding bytes not charged", err)
			}
			result, err = c.PublishPending(m)
			if err == nil || result.State != TracePublicationUnavailable || calls != 1 {
				t.Fatal("substituted registration retried", result, err, calls)
			}
			after, err := os.ReadFile(filepath.Join(dir, "meta.json"))
			afterInfo, statErr := os.Lstat(filepath.Join(dir, "meta.json"))
			afterSeal, sealErr := os.ReadFile(filepath.Join(dir, ".publication-seal"))
			afterControl, controlErr := os.ReadFile(filepath.Join(dir, ".pending"))
			afterPhysical, physicalErr := storeTreeBytes(c.dir)
			if err != nil || statErr != nil || sealErr != nil || controlErr != nil || physicalErr != nil || !os.SameFile(publicInfo, afterInfo) || !bytes.Equal(public, after) || !bytes.Equal(seal, afterSeal) || !bytes.Equal(control, afterControl) || physical != afterPhysical {
				t.Fatal("frozen unavailable publication rewrote or cleaned uncertain objects")
			}
		})
	}
}

func TestTraceStorePendingPublicationFailurePhases(t *testing.T) {
	for _, phase := range []string{".meta-slot.write", ".meta-slot.close", ".publication-seal.write", ".publication-seal.close", "rename", "directory.close"} {
		t.Run(phase, func(t *testing.T) {
			s, root, _ := traceStoreFixture(t)
			m := storeMeta(1, 123)
			c, err := s.MintPending(m.Trace, m.Start)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			calls := 0
			c.publicationObserve = func(stage string, f *os.File) {
				if stage == phase {
					calls++
					_ = f.Close()
				}
			}
			result, err := c.PublishPending(m)
			if phase == "directory.close" {
				if err != nil || result.State != TracePublished {
					t.Fatal("exact snapshot not reconciled", result, err)
				}
			} else if err == nil || result.State != TracePublicationUnavailable {
				t.Fatal("failed phase accepted", result, err)
			}
			before, _ := storeTreeBytes(c.dir)
			result, err = c.PublishPending(m)
			after, _ := storeTreeBytes(c.dir)
			if calls != 1 || before != after {
				t.Fatal("repeated physical attempt", calls, before, after)
			}
			if phase != "directory.close" && err == nil {
				t.Fatal("unavailable retry")
			}
			if phase != "directory.close" {
				if _, err := os.Stat(filepath.Join(root, c.ID(), "meta.json")); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("failed exposure")
				}
			}
			_, inventoryErr := s.Usage()
			if phase == ".meta-slot.write" || phase == "directory.close" {
				if inventoryErr != nil {
					t.Fatal("valid state declined", inventoryErr)
				}
			} else if inventoryErr == nil {
				t.Fatal("uncertain state adopted")
			}
		})
	}
}

func TestTraceStorePendingCrashInventoryAndPrune(t *testing.T) {
	t.Run("independent_live_join", func(t *testing.T) {
		s, _, _ := traceStoreFixture(t)
		owner, err := s.MintPending(storeMeta(1, 1).Trace, 1)
		if err != nil {
			t.Fatal(err)
		}
		joined, err := s.Join(owner.ID())
		if err != nil {
			t.Fatal(err)
		}
		defer joined.Close()
		if err := owner.Close(); err != nil {
			t.Fatal(err)
		}
		for n := 2; n <= MaxRetainedTraces+1; n++ {
			c, err := s.MintPending(storeMeta(n, int64(n)).Trace, int64(n))
			if err != nil {
				t.Fatal(err)
			}
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := joined.Usage(); err != nil {
			t.Fatal("live joined pending pruned", err)
		}
		if _, err := s.Join(storeMeta(2, 2).Trace); err == nil {
			t.Fatal("inactive ordering ignored")
		}
	})
	s, root, _ := traceStoreFixture(t)
	for n := 1; n <= MaxRetainedTraces; n++ {
		c, err := s.MintPending(storeMeta(n, 123).Trace, 123)
		if err != nil {
			t.Fatal(err)
		}
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}
	c, err := s.MintPending(storeMeta(51, 123).Trace, 123)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := s.Join(storeMeta(1, 123).Trace); err == nil {
		t.Fatal("recreated pruned trace")
	}
	if _, err := os.Stat(filepath.Join(root, storeMeta(1, 123).Trace)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("oldest retained")
	}
	if err := os.WriteFile(filepath.Join(root, c.ID(), ".publication-seal"), []byte("torn"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Usage(); err == nil {
		t.Fatal("torn state adopted")
	}
	if _, err := s.MintPending(storeMeta(52, 123).Trace, 123); err == nil {
		t.Fatal("unknown state pruned")
	}
}

func TestTraceStorePendingControlIdentityAndCrashState(t *testing.T) {
	for _, state := range []string{"unknown_version", "unknown_field", "torn_control", "missing_control", "slot_inode", "seal_inode", "slot_partial", "seal_partial", "slot_mode", "slot_link", "published_seal", "published_inode", "control_array_length", "identity_unknown", "identity_array_length"} {
		t.Run(state, func(t *testing.T) {
			s, root, _ := traceStoreFixture(t)
			m := storeMeta(1, 123)
			c, err := s.MintPending(m.Trace, m.Start)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			dir := filepath.Join(root, m.Trace)
			if strings.HasPrefix(state, "published_") {
				if _, err := c.PublishPending(m); err != nil {
					t.Fatal(err)
				}
			}
			controlPath := filepath.Join(dir, ".pending")
			control, err := os.ReadFile(controlPath)
			if err != nil {
				t.Fatal(err)
			}
			switch state {
			case "unknown_version":
				err = os.WriteFile(controlPath, bytes.Replace(control, []byte(`"version":1`), []byte(`"version":2`), 1), 0o600)
			case "unknown_field":
				err = os.WriteFile(controlPath, append(control[:len(control)-1], []byte(`,"unknown":true}`)...), 0o600)
			case "control_array_length", "identity_array_length", "identity_unknown":
				path := controlPath
				key := "meta_slot"
				if strings.HasPrefix(state, "identity_") {
					path = filepath.Join(dir, ".identity")
					key = "root"
				}
				p, readErr := os.ReadFile(path)
				if readErr != nil {
					t.Fatal(readErr)
				}
				var fields map[string]json.RawMessage
				if json.Unmarshal(p, &fields) != nil {
					t.Fatal("fixture control parse")
				}
				if state == "identity_unknown" {
					fields["unknown"] = json.RawMessage("true")
				} else {
					pair := fields[key]
					fields[key] = append(pair[:len(pair)-1], []byte(",1]")...)
				}
				p, err = json.Marshal(fields)
				if err == nil {
					err = os.WriteFile(path, p, 0o600)
				}
			case "torn_control":
				err = os.WriteFile(controlPath, control[:len(control)/2], 0o600)
			case "missing_control":
				err = os.Remove(controlPath)
			case "slot_inode", "seal_inode", "published_inode":
				name := ".meta-slot"
				if state == "seal_inode" {
					name = ".publication-seal"
				}
				if state == "published_inode" {
					name = "meta.json"
				}
				p, readErr := os.ReadFile(filepath.Join(dir, name))
				if readErr != nil {
					t.Fatal(readErr)
				}
				if err = os.Rename(filepath.Join(dir, name), filepath.Join(dir, ".old-object")); err == nil {
					err = os.WriteFile(filepath.Join(dir, name), p, 0o600)
				}
			case "slot_partial":
				err = os.WriteFile(filepath.Join(dir, ".meta-slot"), []byte("torn"), 0o600)
			case "seal_partial", "published_seal":
				err = os.WriteFile(filepath.Join(dir, ".publication-seal"), []byte("torn"), 0o600)
			case "slot_mode":
				err = os.Chmod(filepath.Join(dir, ".meta-slot"), 0o644)
			case "slot_link":
				err = os.Link(filepath.Join(dir, ".meta-slot"), filepath.Join(dir, ".slot-alias"))
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.Usage(); err == nil {
				t.Fatal("uncertain state adopted")
			}
			if _, err := s.MintPending(storeMeta(2, 124).Trace, 124); err == nil {
				t.Fatal("recording admitted around unknown state")
			}
			if _, err := os.Stat(dir); err != nil {
				t.Fatal("unknown state removed")
			}
		})
	}
}

func TestTraceStoreClosePrivacy(t *testing.T) {
	t.Run("repeated_writer", func(t *testing.T) {
		s, root, private := traceStoreFixture(t)
		c := storeMint(t, s, 1, 1)
		w := storeWrite(t, c, "sentinel-lane.jsonl", []byte("owned bytes"))
		if err := w.Close(); err != nil {
			t.Fatal("first writer close failed")
		}
		if err := w.Close(); err != nil {
			assertTraceStoreClosePrivacy(t, err, root, private, filepath.Dir(root))
		}
	})
	t.Run("failed_writer_still_closes_claim", func(t *testing.T) {
		s, root, private := traceStoreFixture(t)
		c := storeMint(t, s, 1, 1)
		w := storeWrite(t, c, "sentinel-lane.jsonl", []byte("owned bytes"))
		if err := w.file.Close(); err != nil {
			t.Fatal("owned fault setup failed")
		}
		assertTraceStoreClosePrivacy(t, w.Close(), root, private, filepath.Dir(root))
		if _, err := w.claim.lock.Stat(); !errors.Is(err, os.ErrClosed) {
			t.Error("writer close stopped before closing its claim lock")
		}
		if _, err := w.claim.dir.Stat("."); !errors.Is(err, os.ErrClosed) {
			t.Error("writer close stopped before closing its claim directory")
		}
	})
	t.Run("failed_claim_still_closes_directory", func(t *testing.T) {
		s, root, private := traceStoreFixture(t)
		c := storeMint(t, s, 1, 1)
		if err := c.lock.Close(); err != nil {
			t.Fatal("owned fault setup failed")
		}
		assertTraceStoreClosePrivacy(t, c.Close(), root, private, filepath.Dir(root))
		if _, err := c.dir.Stat("."); !errors.Is(err, os.ErrClosed) {
			t.Error("claim close stopped before closing its directory")
		}
		if err := c.Close(); err != nil {
			assertTraceStoreClosePrivacy(t, err, root, private, filepath.Dir(root))
		}
	})
	t.Run("root_handles_both_attempted", func(t *testing.T) {
		for _, first := range []string{"public", "private"} {
			t.Run(first, func(t *testing.T) {
				s, root, private := traceStoreFixture(t)
				fault := s.root
				if first == "private" {
					fault = s.private
				}
				if err := fault.Close(); err != nil {
					t.Fatal("owned root fault setup failed")
				}
				// os.Root.Close may already be idempotent. Any failure must still be safe.
				if err := s.Close(); err != nil {
					assertTraceStoreClosePrivacy(t, err, root, private, filepath.Dir(root))
				}
				for _, handle := range []*os.Root{s.root, s.private} {
					if _, err := handle.Stat("."); !errors.Is(err, os.ErrClosed) {
						t.Error("store close did not attempt both root handles")
					}
				}
			})
		}
	})
}

func TestTraceStoreFreshMint(t *testing.T) {
	s, root, _ := traceStoreFixture(t)
	c := storeMint(t, s, 1, 1)
	storeWrite(t, c, "launcher-1.jsonl", []byte("visible now\n"))
	p, err := os.ReadFile(filepath.Join(root, c.ID(), "launcher-1.jsonl"))
	if err != nil || string(p) != "visible now\n" {
		t.Fatalf("immediate append: %q %v", p, err)
	}
	p, err = os.ReadFile(filepath.Join(root, c.ID(), "meta.json"))
	var meta map[string]any
	if err != nil || json.Unmarshal(p, &meta) != nil || len(meta) != 7 {
		t.Fatalf("seven metadata keys: %q %v", p, err)
	}
	if _, err := c.CreateWriter("launcher-1.jsonl"); err == nil {
		t.Fatal("second lane owner accepted")
	}
	if err := c.Diagnostic("capacity"); err != nil {
		t.Fatal(err)
	}
	if err := c.Diagnostic("again"); err == nil {
		t.Fatal("diagnostic reserve used twice")
	}
}

func TestTraceStoreCountAndStartIDTie(t *testing.T) {
	s, root, _ := traceStoreFixture(t)
	for n := 1; n <= 50; n++ {
		c := storeMint(t, s, n, 10)
		c.Close()
	}
	storeMint(t, s, 51, 9).Close()
	if _, err := os.Stat(filepath.Join(root, storeMeta(1, 0).Trace)); !os.IsNotExist(err) {
		t.Fatalf("oldest equal-start ID not removed: %v", err)
	}
	storeMint(t, s, 52, 20)
	if _, err := os.Stat(filepath.Join(root, storeMeta(51, 0).Trace)); !os.IsNotExist(err) {
		t.Fatalf("start ordering not used: %v", err)
	}
	st, err := s.Usage()
	if err != nil || st.Traces != 50 {
		t.Fatalf("count: %+v %v", st, err)
	}
}

func TestTraceStoreLiveAndUnknownLiveness(t *testing.T) {
	s, root, _ := traceStoreFixture(t)
	for n := 1; n <= 50; n++ {
		storeMint(t, s, n, int64(n))
	}
	if _, err := s.Mint(storeMeta(51, 51)); err == nil {
		t.Fatal("live traces pruned")
	}
	if err := os.Remove(filepath.Join(root, storeMeta(1, 0).Trace, ".identity")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Mint(storeMeta(52, 52)); err == nil {
		t.Fatal("unknown ownership/liveness accepted")
	}
}

func TestTraceStorePerTraceExactBoundary(t *testing.T) {
	s, _, _ := traceStoreFixture(t)
	c := storeMint(t, s, 1, 1)
	u, err := c.Usage()
	if err != nil {
		t.Fatal(err)
	}
	w := storeWrite(t, c, "lane.jsonl", bytes.Repeat([]byte{'x'}, int(MaxTraceContentBytes-u.Bytes)))
	if _, err := w.Write([]byte("x")); err == nil {
		t.Fatal("trace over limit admitted")
	}
	u, err = c.Usage()
	if err != nil || u.Bytes != MaxTraceContentBytes {
		t.Fatalf("boundary: %+v %v", u, err)
	}
}

func TestTraceStoreGlobalExactBoundaryAndUnknownPrivate(t *testing.T) {
	s, _, private := traceStoreFixture(t)
	c := storeMint(t, s, 1, 1)
	u, err := s.Usage()
	if err != nil {
		t.Fatal(err)
	}
	// Unknown private content remains charged machine-wide, never silently reclaimed.
	if err := os.WriteFile(filepath.Join(private, "orphan"), bytes.Repeat([]byte{'x'}, int(MaxStoreContentBytes-u.Bytes-1)), 0o600); err != nil {
		t.Fatal(err)
	}
	w := storeWrite(t, c, "lane.jsonl", []byte("x"))
	if _, err := w.Write([]byte("x")); err == nil {
		t.Fatal("machine cap exceeded")
	}
	u, err = s.Usage()
	if err != nil || u.Bytes != MaxStoreContentBytes {
		t.Fatalf("machine boundary: %+v %v", u, err)
	}
	if _, err := s.Mint(storeMeta(2, 2)); err == nil {
		t.Fatal("mint at machine cap accepted")
	}
}

func TestTraceStorePrivateControlCrashLeftAndOverBudget(t *testing.T) {
	s, root, private := traceStoreFixture(t)
	c := storeMint(t, s, 1, 1)
	if err := c.ReplacePrivate("registry", []byte("locator")); err != nil {
		t.Fatal(err)
	}
	u, err := c.Usage()
	if err != nil {
		t.Fatal(err)
	}
	for path, n := range map[string]int{filepath.Join(root, c.ID(), ".crash-left"): 17, filepath.Join(private, c.ID(), "control"): 13} {
		if err := os.WriteFile(path, bytes.Repeat([]byte{'x'}, n), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	after, err := c.Usage()
	if err != nil || after.Bytes != u.Bytes+30 {
		t.Fatalf("private/control/crash accounting: %+v -> %+v %v", u, after, err)
	}
	if err := os.WriteFile(filepath.Join(root, "unknown-control"), bytes.Repeat([]byte{'x'}, int(MaxStoreContentBytes)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Mint(storeMeta(2, 2)); err == nil {
		t.Fatal("over-budget legacy store shrunk or admitted")
	}
	if _, err := os.Stat(filepath.Join(root, "unknown-control")); err != nil {
		t.Fatal("unknown content removed")
	}
}

func TestTraceStoreReplacementPeakAndFailedPublication(t *testing.T) {
	s, root, _ := traceStoreFixture(t)
	c := storeMint(t, s, 1, 1)
	old := []byte("last good snapshot")
	if err := c.Replace("jail.jsonl", old); err != nil {
		t.Fatal(err)
	}
	u, _ := c.Usage()
	storeWrite(t, c, "lane.jsonl", bytes.Repeat([]byte{'x'}, int(MaxTraceContentBytes-u.Bytes)))
	if err := c.Replace("jail.jsonl", []byte("new")); err == nil {
		t.Fatal("replacement discounted old copy")
	}
	got, _ := os.ReadFile(filepath.Join(root, c.ID(), "jail.jsonl"))
	if !bytes.Equal(got, old) {
		t.Fatal("last good snapshot lost")
	}
	// A substituted nonregular publication target cannot be followed or removed.
	if err := os.Symlink("jail.jsonl", filepath.Join(root, c.ID(), "bad")); err != nil {
		t.Fatal(err)
	}
	if err := c.Replace("bad", []byte("x")); err == nil {
		t.Fatal("unsafe publication accepted")
	}
	got, _ = os.ReadFile(filepath.Join(root, c.ID(), "jail.jsonl"))
	if !bytes.Equal(got, old) {
		t.Fatal("failed publication changed old data")
	}
}

func TestTraceStoreReplacementCannotUnlinkLiveWriter(t *testing.T) {
	s, root, _ := traceStoreFixture(t)
	c := storeMint(t, s, 1, 1)
	w := storeWrite(t, c, "lane.jsonl", []byte("owned lane"))
	if err := c.Replace("lane.jsonl", []byte("replacement")); err == nil {
		t.Fatal("replacement unlinked a live writer's physical content")
	}
	if n, err := w.Write([]byte("!")); err != nil || n != 1 {
		t.Fatalf("refused replacement broke writer: %d %v", n, err)
	}
	p, _ := os.ReadFile(filepath.Join(root, c.ID(), "lane.jsonl"))
	if string(p) != "owned lane!" {
		t.Fatalf("original content not retained: %q", p)
	}
}

func TestTraceStoreDiagnosticReserveAcrossClaims(t *testing.T) {
	s, _, _ := traceStoreFixture(t)
	c := storeMint(t, s, 1, 1)
	if err := c.Diagnostic("capacity"); err != nil {
		t.Fatal(err)
	}
	child, err := s.Join(c.ID())
	if err != nil {
		t.Fatal(err)
	}
	defer child.Close()
	if err := child.Diagnostic("again"); err == nil {
		t.Fatal("second claim replaced admitted diagnostic evidence")
	}
}

func TestTraceStoreIncompatibleIdentityDeclinesBeforeCapacity(t *testing.T) {
	s, root, _ := traceStoreFixture(t)
	c := storeMint(t, s, 1, 1)
	c.Close()
	if err := os.Remove(filepath.Join(root, c.ID(), ".identity")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Mint(storeMeta(2, 2)); err == nil {
		t.Fatal("uncertain identity admitted new recording below capacity")
	}
	if _, err := os.Stat(filepath.Join(root, c.ID())); err != nil {
		t.Fatal("uncertain trace pruned")
	}
}

func TestTraceStoreMetadataRejectsPrivatePayloads(t *testing.T) {
	s, _, _ := traceStoreFixture(t)
	meta := storeMeta(1, 1)
	meta.Kind = "/private/path"
	if _, err := s.Mint(meta); err == nil {
		t.Fatal("path became public metadata")
	}
}

func TestTraceStoreOrphanPrivateTraceOverBudget(t *testing.T) {
	s, _, private := traceStoreFixture(t)
	id := storeMeta(99, 1).Trace
	if err := os.Mkdir(filepath.Join(private, id), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(private, id, "registry"), bytes.Repeat([]byte{'x'}, int(MaxTraceContentBytes+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Mint(storeMeta(1, 1)); err == nil {
		t.Fatal("orphan attributable trace already exceeds its cap")
	}
}

func TestTraceStoreFailedAppendDisablesLane(t *testing.T) {
	s, root, _ := traceStoreFixture(t)
	c := storeMint(t, s, 1, 1)
	w := storeWrite(t, c, "lane.jsonl", []byte("last good"))
	// Fault only this owned fixture descriptor, then drive the production write entry.
	w.file.Close()
	if n, err := w.Write([]byte("broken")); err == nil || n != 0 {
		t.Fatalf("failed append reported stored: %d %v", n, err)
	}
	if n, err := w.Write([]byte("retry")); err == nil || n != 0 {
		t.Fatalf("disabled lane retried: %d %v", n, err)
	}
	p, _ := os.ReadFile(filepath.Join(root, c.ID(), "lane.jsonl"))
	if string(p) != "last good" {
		t.Fatalf("failed append lost admitted bytes: %q", p)
	}
}

func TestTraceStoreReplacedLockAndDirectoryIdentity(t *testing.T) {
	for _, object := range []string{"lock", "directory", "admission"} {
		t.Run(object, func(t *testing.T) {
			s, root, _ := traceStoreFixture(t)
			c := storeMint(t, s, 1, 1)
			path := filepath.Join(root, c.ID(), "lock")
			if object == "directory" {
				path = filepath.Join(root, c.ID())
			} else if object == "admission" {
				path = filepath.Join(root, ".admission")
			}
			if err := os.Rename(path, path+"-old"); err != nil {
				t.Fatal(err)
			}
			if object == "directory" {
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(path, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Join(c.ID()); err == nil {
				t.Fatal("replacement identity joined")
			}
			if _, err := c.CreateWriter("late.jsonl"); err == nil {
				t.Fatal("old claim wrote into replacement")
			}
		})
	}
}

func TestTraceStoreLateJoinAfterPrune(t *testing.T) {
	s, root, _ := traceStoreFixture(t)
	c := storeMint(t, s, 1, 1)
	id := c.ID()
	c.Close()
	for n := 2; n <= 51; n++ {
		storeMint(t, s, n, int64(n))
	}
	if _, err := s.Join(id); err == nil {
		t.Fatal("late join recreated trace")
	}
	if _, err := os.Stat(filepath.Join(root, id)); !os.IsNotExist(err) {
		t.Fatalf("pruned directory recreated: %v", err)
	}
}

func TestTraceStoreInabilityAccountAndContention(t *testing.T) {
	s, root, private := traceStoreFixture(t)
	c := storeMint(t, s, 1, 1)
	f, err := os.OpenFile(filepath.Join(root, ".admission"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateWriter("busy.jsonl"); err == nil {
		t.Fatal("contended admission did not decline")
	}
	syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	if err := os.Symlink(root, filepath.Join(private, "unsafe")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Mint(storeMeta(2, 2)); err == nil {
		t.Fatal("unaccountable symlink accepted")
	}
}

// Re-exec helpers use this binary's TestMain isolation and inherited fixture TMPDIR.
func TestTraceStoreProcessHelper(t *testing.T) {
	mode := os.Getenv("TRACE_STORE_TEST_MODE")
	if mode == "" {
		t.Skip("owned re-exec helper only")
	}
	s, err := OpenTraceStore(os.Getenv("TRACE_STORE_TEST_ROOT"), os.Getenv("TRACE_STORE_TEST_PRIVATE"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	fmt.Println("ready")
	if _, err := bufio.NewReader(os.Stdin).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	var c *TraceClaim
	if mode == "join" {
		c, err = s.Join(os.Getenv("TRACE_STORE_TEST_ID"))
	} else {
		var n int
		fmt.Sscan(os.Getenv("TRACE_STORE_TEST_N"), &n)
		c, err = s.Mint(storeMeta(n, int64(n)))
	}
	if err != nil {
		fmt.Println("declined")
		return
	}
	defer c.Close()
	fmt.Println("claimed")
	if _, err := bufio.NewReader(os.Stdin).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
}

func storeChild(t *testing.T, root, private, mode, id string, n int) (*exec.Cmd, io.WriteCloser, *bufio.Reader) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestTraceStoreProcessHelper$", "-test.timeout=45m")
	cmd.Env = testtmp.ChildEnv(append(os.Environ(), "TRACE_STORE_TEST_MODE="+mode, "TRACE_STORE_TEST_ROOT="+root, "TRACE_STORE_TEST_PRIVATE="+private, "TRACE_STORE_TEST_ID="+id, fmt.Sprintf("TRACE_STORE_TEST_N=%d", n)))
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	r := bufio.NewReader(out)
	if line, err := r.ReadString('\n'); err != nil || strings.TrimSpace(line) != "ready" {
		t.Fatalf("child ready %q: %v", line, err)
	}
	t.Cleanup(func() { in.Close(); cmd.Process.Kill(); cmd.Wait() })
	return cmd, in, r
}

func TestTraceStoreConcurrentMintersAcrossProcesses(t *testing.T) {
	s, root, private := traceStoreFixture(t)
	for n := 1; n <= 49; n++ {
		storeMint(t, s, n, int64(n))
	}
	_, a, ar := storeChild(t, root, private, "mint", "", 50)
	_, b, br := storeChild(t, root, private, "mint", "", 51)
	fmt.Fprintln(a, "go")
	fmt.Fprintln(b, "go")
	la, ea := ar.ReadString('\n')
	lb, eb := br.ReadString('\n')
	if ea != nil || eb != nil || (strings.TrimSpace(la) != "claimed" && strings.TrimSpace(lb) != "claimed") {
		t.Fatalf("minters: %q %v / %q %v", la, ea, lb, eb)
	}
	if strings.TrimSpace(la) == "claimed" && strings.TrimSpace(lb) == "claimed" {
		t.Fatal("two processes exceeded capacity")
	}
	u, err := s.Usage()
	if err != nil || u.Traces != 50 {
		t.Fatalf("race occupancy: %+v %v", u, err)
	}
}

func TestTraceStoreContinuousChildHandover(t *testing.T) {
	s, root, private := traceStoreFixture(t)
	parent := storeMint(t, s, 1, 1)
	_, in, r := storeChild(t, root, private, "join", parent.ID(), 0)
	fmt.Fprintln(in, "go")
	if line, err := r.ReadString('\n'); err != nil || strings.TrimSpace(line) != "claimed" {
		t.Fatalf("child acquired claim: %q %v", line, err)
	}
	parent.Close() // Never unlock a shared file description inherited from the parent.
	for n := 2; n <= 50; n++ {
		storeMint(t, s, n, int64(n))
	}
	if _, err := s.Mint(storeMeta(51, 51)); err == nil {
		t.Fatal("parent unlock released child's protection")
	}
}
