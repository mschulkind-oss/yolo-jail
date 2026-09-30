// Package modelmenu writes a program's MODEL MENU (packdecl.ModelMenu, a term coined there): the
// program's own catalog entries whose id yolo's list names, in the list's order, with the fields
// the pack declares renumbered, renamed, cleared or set. The generated launcher runs it before
// exec'ing the program, as `yolo internal model-menu` (docs/design/model-lists-and-pickers.md
// MM-D9, MM-D22), and adds the pack's flag naming the file only when it printed one.
//
// It knows no program. Every key it reads and writes is the pack's declaration, so codex's
// `slug`, `priority` and `upgrade` are words packs/codex states, not words this package spells.
package modelmenu

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// Verb is the `yolo internal` verb the generated launcher runs this package as. Spelled here and
// read by both the launcher template (internal/entrypoint) and the dispatcher (internal/cli), so
// the two cannot drift.
const Verb = "model-menu"

// Bounds on one run. codex 0.159.2's bundled catalog is 11 entries of 20 to 65 KB (MEASURED
// 2026-09-30), so both are far above a real catalog and exist only so a program that hangs or
// floods cannot hold the launch the user typed.
const (
	catalogTimeout  = 30 * time.Second
	maxCatalogBytes = 64 << 20
)

// ListEntry is one model of yolo's list, as the pack's derive renders it.
type ListEntry struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// Result is what one projection made of a catalog.
type Result struct {
	// Menu is the menu file's bytes, nil when no listed id survived.
	Menu []byte
	// Kept is the listed ids the menu holds, in its order.
	Kept []string
	// Missing is the listed ids the program's catalog lacks, in the list's order.
	Missing []string
}

// Project keeps the entries of catalog, a JSON object, whose spec.ID names a model of list, in
// the list's order, and applies spec's renumbering, names, clears and sets to each. Every other
// key of the catalog object and of each kept entry is copied as it was, so the entry keeps the
// prompt text the program needs. An error means the catalog is not the shape spec describes.
func Project(catalog []byte, list []ListEntry, spec packdecl.ModelMenu) (Result, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(catalog, &top); err != nil {
		return Result{}, fmt.Errorf("its output is not a JSON object: %v", err)
	}
	raw, ok := top[spec.Entries]
	if !ok {
		return Result{}, fmt.Errorf("its output has no %q key", spec.Entries)
	}
	var entries []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return Result{}, fmt.Errorf("its %q is not an array of objects: %v", spec.Entries, err)
	}
	byID := make(map[string]map[string]json.RawMessage, len(entries))
	for _, e := range entries {
		var id string
		if err := json.Unmarshal(e[spec.ID], &id); err != nil || id == "" {
			continue
		}
		if _, dup := byID[id]; !dup {
			byID[id] = e
		}
	}
	var res Result
	var kept []map[string]json.RawMessage
	seen := map[string]bool{}
	for _, l := range list {
		if l.ID == "" || seen[l.ID] {
			continue
		}
		seen[l.ID] = true
		e, ok := byID[l.ID]
		if !ok {
			res.Missing = append(res.Missing, l.ID)
			continue
		}
		out := make(map[string]json.RawMessage, len(e)+len(spec.Set)+2)
		for k, v := range e {
			out[k] = v
		}
		if spec.Order != "" {
			out[spec.Order] = json.RawMessage(fmt.Sprint(len(kept)))
		}
		if spec.Name != "" && l.Name != "" {
			out[spec.Name] = mustString(l.Name)
		}
		for _, k := range spec.Clear {
			delete(out, k)
		}
		for k, v := range spec.Set {
			out[k] = mustString(v)
		}
		kept = append(kept, out)
		res.Kept = append(res.Kept, l.ID)
	}
	if len(kept) == 0 {
		return res, nil
	}
	arr, err := json.Marshal(kept)
	if err != nil {
		return Result{}, err
	}
	top[spec.Entries] = arr
	menu, err := json.Marshal(top)
	if err != nil {
		return Result{}, err
	}
	res.Menu = menu
	return res, nil
}

func mustString(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return b
}

// ReadList reads yolo's list: nil for a missing, unparseable or empty file, which all mean "no
// menu this launch".
func ReadList(path string) []ListEntry {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var doc struct {
		Models []ListEntry `json:"models"`
	}
	if json.Unmarshal(data, &doc) != nil {
		return nil
	}
	var out []ListEntry
	for _, m := range doc.Models {
		if m.ID != "" {
			out = append(out, m)
		}
	}
	return out
}

// Flag is spec.Flag with packdecl.ModelMenuInto replaced by into, the menu's absolute path.
func Flag(spec packdecl.ModelMenu, into string) []string {
	out := make([]string, len(spec.Flag))
	for i, w := range spec.Flag {
		out[i] = strings.ReplaceAll(w, packdecl.ModelMenuInto, into)
	}
	return out
}

// Run is `yolo internal model-menu --bin=<bin> --spec=<json> -- <program> [prefix argv...]`: it
// reads yolo's list and, when the list names a model, the program's catalog (the program run with
// spec.Catalog appended, stdout captured), writes the menu to spec.Into and prints the flag words
// the launcher adds, each NUL-terminated, on stdout. It prints nothing, and the launcher adds
// nothing, whenever there is no menu to name, and it never fails the launch: every problem is a
// warning on stderr and exit 0, except a misuse, which is exit 2. home is the directory the
// spec's paths are relative to.
func Run(args []string, home string, stdout, stderr io.Writer) int {
	var bin, specJSON string
	var program []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			program = args[i+1:]
			i = len(args)
		case strings.HasPrefix(a, "--bin="):
			bin = strings.TrimPrefix(a, "--bin=")
		case strings.HasPrefix(a, "--spec="):
			specJSON = strings.TrimPrefix(a, "--spec=")
		default:
			fmt.Fprintf(stderr, "yolo internal %s: unexpected argument %q\n", Verb, a)
			return 2
		}
	}
	var spec packdecl.ModelMenu
	if bin == "" || len(program) == 0 || json.Unmarshal([]byte(specJSON), &spec) != nil ||
		spec.List == "" || spec.Into == "" || spec.Entries == "" || spec.ID == "" || home == "" {
		fmt.Fprintf(stderr, "usage: yolo internal %s --bin=<bin> --spec=<json> -- <program> [argv...]\n", Verb)
		return 2
	}
	listPath := filepath.Join(home, filepath.FromSlash(spec.List))
	into := filepath.Join(home, filepath.FromSlash(spec.Into))
	keyPath := into + ".key"
	list := ReadList(listPath)
	if len(list) == 0 {
		// No list this launch: a menu left from an earlier one must not be reusable.
		removeMenu(into, keyPath)
		return 0
	}
	key := cacheKey(specJSON, listPath, program[len(program)-1])
	if got, err := os.ReadFile(keyPath); err == nil && fileExists(into) {
		if gotKey, missing := parseKeyFile(got); gotKey == key {
			// A reused menu leaves out what the rebuilt one did, so it says so again: the
			// warning is about the menu this launch hands the program, not about the rebuild.
			warnMissing(stderr, bin, missing)
			printFlag(stdout, Flag(spec, into))
			return 0
		}
	}
	catalog, err := runCatalog(program, spec.Catalog)
	if err != nil {
		fmt.Fprintf(stderr, "yolo: could not read %s's own model catalog (%v), so %s shows its own model menu.\n",
			bin, err, bin)
		removeMenu(into, keyPath)
		return 0
	}
	res, err := Project(catalog, list, spec)
	if err != nil {
		fmt.Fprintf(stderr, "yolo: %s's own model catalog is not the shape its pack declares (%v), so %s "+
			"shows its own model menu.\n", bin, err, bin)
		removeMenu(into, keyPath)
		return 0
	}
	warnMissing(stderr, bin, res.Missing)
	if res.Menu == nil {
		fmt.Fprintf(stderr, "yolo: none of yolo's models for this provider is in %s's own catalog, so %s "+
			"shows its own model menu.\n", bin, bin)
		removeMenu(into, keyPath)
		return 0
	}
	if err := writeAtomic(into, res.Menu); err != nil {
		fmt.Fprintf(stderr, "yolo: could not write %s's model menu (%v), so %s shows its own.\n", bin, err, bin)
		return 0
	}
	// The key is written last: a crash between the two leaves a menu with no key, which the next
	// launch rebuilds, never a key vouching for a menu that is not there. It carries the ids the
	// menu left out, so a launch that reuses the menu warns about them as this one did.
	_ = writeAtomic(keyPath, keyFile(key, res.Missing))
	printFlag(stdout, Flag(spec, into))
	return 0
}

// warnMissing says which of yolo's listed ids the program's own catalog lacks, and so the menu
// leaves out, or nothing when there are none. Printed at EVERY launch whose menu lacks them, the
// one that rebuilt it and each that reuses it (MM-D9: "left out, with a warning").
func warnMissing(stderr io.Writer, bin string, missing []string) {
	if len(missing) == 0 {
		return
	}
	these, it := "the model", "it"
	if len(missing) > 1 {
		these, it = "these models", "them"
	}
	fmt.Fprintf(stderr, "yolo: %s's own model catalog has no %s, so its model menu leaves %s out. "+
		"A %s older than %s lacks %s; updating %s adds %s.\n",
		bin, strings.Join(missing, ", "), it, bin, these, it, bin, it)
}

// keyFile is the key file's bytes: the cache key on the first line, then the listed ids the menu
// left out, as a JSON array, so no id can be split or lost on its way back.
func keyFile(key string, missing []string) []byte {
	ids, _ := json.Marshal(append([]string{}, missing...))
	return []byte(key + "\n" + string(ids))
}

// parseKeyFile is keyFile's inverse: the key, and the ids the menu left out. A second line that
// is not a JSON array of strings reads as none left out.
func parseKeyFile(data []byte) (key string, missing []string) {
	key, rest, _ := strings.Cut(string(data), "\n")
	_ = json.Unmarshal([]byte(rest), &missing)
	return key, missing
}

// cacheKey names what the menu was built from: the declaration, the list's bytes, and the
// program file's identity (resolved, so an update that repoints a symlink changes it). A menu
// whose key matches is reused without running the program.
func cacheKey(specJSON, listPath, program string) string {
	h := sha256.New()
	fmt.Fprintf(h, "spec\x00%s\x00", specJSON)
	if data, err := os.ReadFile(listPath); err == nil {
		h.Write(data)
	}
	h.Write([]byte{0})
	if resolved, err := filepath.EvalSymlinks(program); err == nil {
		program = resolved
	}
	if fi, err := os.Stat(program); err == nil {
		fmt.Fprintf(h, "%s\x00%d\x00%d", program, fi.Size(), fi.ModTime().UnixNano())
	} else {
		fmt.Fprintf(h, "%s\x00absent", program)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// runCatalog runs program with argv appended, bounded in time and output, and returns its
// stdout. Its stdin is empty and its stderr discarded: the catalog is all it is asked for.
func runCatalog(program, argv []string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), catalogTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, program[0], append(append([]string(nil), program[1:]...), argv...)...)
	var out limitedBuffer
	out.limit = maxCatalogBytes
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("it did not finish in %s", catalogTimeout)
		}
		return nil, fmt.Errorf("%s %s: %v", filepath.Base(program[len(program)-1]), strings.Join(argv, " "), err)
	}
	if out.over {
		return nil, fmt.Errorf("it printed more than %d bytes", maxCatalogBytes)
	}
	return out.Bytes(), nil
}

type limitedBuffer struct {
	bytes.Buffer
	limit int
	over  bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		b.over = true
		return len(p), nil
	}
	return b.Buffer.Write(p)
}

func printFlag(w io.Writer, words []string) {
	for _, word := range words {
		fmt.Fprintf(w, "%s\x00", word)
	}
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

func removeMenu(into, keyPath string) {
	_ = os.Remove(keyPath)
	_ = os.Remove(into)
}

// writeAtomic writes data to path through a temporary file in its directory, 0600, so a program
// reading the menu never sees half of one.
func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}
