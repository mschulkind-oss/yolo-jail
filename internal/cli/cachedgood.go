package cli

import (
	"bufio"
	"debug/elf"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/treedigest"
)

var cachedGoodKeyRE = regexp.MustCompile(`^[0-9a-f]{16}$`)

// runCachedGoodFork selects only the check record's exact Good.Entry. It does no check, replay,
// build, scan, or recovery-by-directory-order. The record lock covers the final identity check and
// the launch's hand record, so the existing reaper cannot remove the selected bytes in between.
func runCachedGoodFork(f packload.Fork, req run.ForkBuildRequest, out io.Writer) entrypoint.ForkDelivery {
	baseStore := patchedAdvanceStore(true)
	storeCopy := *baseStore
	storeCopy.NoWait = true
	store := &storeCopy
	record, err := store.LoadCheckRecord(f.Key())
	if err != nil {
		return cachedGoodRefusal(f, nil, req, out, fmt.Sprintf("cannot read the recorded Good build (%v)", err))
	}
	good := cloneGoodBuild(record.Good)
	if good == nil {
		return cachedGoodRefusal(f, record, req, out, "the check record has no recorded Good build")
	}
	entry, receipt, why := validateCachedGood(f, req.Platform, good)
	if why != "" {
		return cachedGoodRefusal(f, record, req, out, why)
	}
	if req.Hand == nil {
		return cachedGoodRefusal(f, record, req, out, "this launch cannot record which build it hands its jail")
	}
	// The old artifact is validated independently of today's recipe. It does not become the current
	// Good, and the desired series, failure outcome, and backoff remain untouched.
	var delivery entrypoint.ForkDelivery
	err = store.WithCheckRecord(f.Key(), nil, func(current *packsrc.CheckRecord, readErr error,
		_ func() error) (bool, error) {
		if readErr != nil || current == nil || !sameGoodBuild(current.Good, good) {
			return false, fmt.Errorf("the recorded Good identity changed while selecting it; retry the fresh launch")
		}
		entryAgain, receiptAgain, why := validateCachedGood(f, req.Platform, current.Good)
		if why != "" || entryAgain.Key != entry.Key || receiptAgain.Digest != receipt.Digest {
			if why == "" {
				why = "the recorded Good entry changed while selecting it"
			}
			return false, fmt.Errorf("%s", why)
		}
		h := run.HandedFork{Key: entryAgain.Key, Fork: f.Key(), Commit: current.Good.Commit,
			Tag: current.Good.Tag, Patches: current.Good.Patches, Series: current.Good.Series}
		if err := req.Hand(f.Bin, h); err != nil {
			return false, fmt.Errorf("recording the launch's handed build: %w", err)
		}
		delivery = entrypoint.ForkDelivery{Key: entryAgain.Key}
		return false, nil
	})
	if err != nil {
		if errors.Is(err, packsrc.ErrLockHeld) {
			return cachedGoodRefusal(f, record, req, out,
				"another yolo process holds the owner record lock; wait for it to finish, then retry this fresh launch")
		}
		return cachedGoodRefusal(f, record, req, out, err.Error())
	}
	if delivery.Key == "" {
		return cachedGoodRefusal(f, record, req, out, "the selected Good entry was not handed")
	}
	fmt.Fprintf(out, "Cached good: %s at %s (%s, series %s, entry %s); this old build does not contain current edits or the current failure's output; this launch skips its advance.\n",
		f.Key(), goodCommitLabel(good), good.Commit, good.Series, good.Entry)
	writeCachedGoodFailure(out, f, record, req.Workspace)
	writeCachedGoodPatchFailure(out, f, record, good.Entry)
	return delivery
}

func cachedGoodRefusal(f packload.Fork, record *packsrc.CheckRecord, req run.ForkBuildRequest, out io.Writer, why string) entrypoint.ForkDelivery {
	if record != nil {
		writeCachedGoodFailure(out, f, record, req.Workspace)
		writeCachedGoodPatchFailure(out, f, record, "")
	}
	repair := "`yolo capture " + f.Bin + "`"
	if pf := currentCachedGoodPatchFailure(f, record); pf != nil {
		if pf.Kind == "base" {
			repair = "re-export the patch series with `git format-patch --base=" + pf.Target.Commit + "`"
		} else {
			repair = "`yolo pack rebase " + f.Key() + " --onto " + pf.Target.Commit + "`"
		}
	}
	if strings.Contains(why, "another yolo process holds the owner record lock") {
		repair = "retry the fresh launch after that process finishes"
	}
	message := fmt.Sprintf("cached good for %s is unavailable: %s. No prior build was delivered. Next step: %s.", f.Key(), why, repair)
	fmt.Fprintln(out, message)
	return entrypoint.ForkDelivery{Reason: message}
}

func currentCachedGoodPatchFailure(f packload.Fork, record *packsrc.CheckRecord) *packsrc.PatchFailure {
	if record == nil {
		return nil
	}
	series, err := f.ReadSeries()
	if err != nil {
		return nil
	}
	inputs, _, _, err := f.CheckWant(series).Inputs()
	if err != nil {
		return nil
	}
	return record.CurrentPatchFailure(inputs, series.Digest)
}

func writeCachedGoodPatchFailure(w io.Writer, f packload.Fork, record *packsrc.CheckRecord, admitted string) {
	pf := currentCachedGoodPatchFailure(f, record)
	if pf == nil {
		return
	}
	if admitted == "" {
		writePatchFailure(w, pf, f.Key(), packsrc.PatchBypass{Command: "yolo", Missing: true}, "")
		return
	}
	target := pf.Target.Tag
	if target == "" {
		target = pf.Target.Commit
	}
	fmt.Fprintf(w, "Current typed patch failure remains recorded at upstream %s (%s), member %s; this cached build predates it.\n",
		target, pf.Target.Commit, printableFailureText(pf.Member))
	if pf.Kind == "conflict" && len(pf.Paths) > 0 {
		fmt.Fprintf(w, "  Conflict: %s\n", printableFailureText(strings.Join(pf.Paths, ", ")))
	} else if pf.Detail != "" {
		fmt.Fprintf(w, "  Application detail: %s\n", printableFailureText(pf.Detail))
	}
	if pf.Kind == "base" {
		fmt.Fprintf(w, "  Repair: re-export the patch series with `git format-patch --base=%s`.\n", pf.Target.Commit)
	} else {
		fmt.Fprintf(w, "  Repair: `yolo pack rebase %s --onto %s`.\n", f.Key(), pf.Target.Commit)
	}
	if pf.Log != "" {
		fmt.Fprintf(w, "  Log: %s\n", printableFailureText(pf.Log))
	}
}

func cloneGoodBuild(g *packsrc.GoodBuild) *packsrc.GoodBuild {
	if g == nil {
		return nil
	}
	copy := *g
	if g.Read != nil {
		read := *g.Read
		copy.Read = &read
	}
	return &copy
}

func sameGoodBuild(a, b *packsrc.GoodBuild) bool {
	if a == nil || b == nil {
		return a == b
	}
	readsMatch := a.Read == nil && b.Read == nil
	if a.Read != nil && b.Read != nil {
		readsMatch = *a.Read == *b.Read
	}
	return readsMatch && a.Commit == b.Commit && a.Tag == b.Tag && a.Version == b.Version && a.Series == b.Series &&
		a.Recipe == b.Recipe && a.Tree == b.Tree && a.Patches == b.Patches && a.Seq == b.Seq &&
		a.Entry == b.Entry && a.At == b.At
}

func goodCommitLabel(g *packsrc.GoodBuild) string {
	if g.Tag != "" {
		return g.Tag
	}
	return g.Commit
}

func validateCachedGood(f packload.Fork, platform string, good *packsrc.GoodBuild) (*capture.Entry, *entrypoint.BuildReceipt, string) {
	if good == nil || good.Entry == "" || !cachedGoodKeyRE.MatchString(good.Entry) {
		return nil, nil, "the check record does not name a safe Good.Entry; yolo will not guess an older directory"
	}
	store := &capture.Store{Dir: paths.CapturesDir()}
	entry, err := store.Resolve(good.Entry)
	if err != nil {
		return nil, nil, fmt.Sprintf("recorded Good.Entry %s is missing or pruned (%v)", good.Entry, err)
	}
	if why := requireRealDir(entry.Root, "entry"); why != "" {
		return nil, nil, why
	}
	if why := requireRealDir(entry.Tree, "tree"); why != "" {
		return nil, nil, why
	}
	marker := filepath.Join(entry.Root, ".yolo-capture-complete")
	if why := requireRegularFile(marker, "completion marker"); why != "" {
		return nil, nil, why
	}
	markerBytes, err := os.ReadFile(marker)
	if err != nil || strings.TrimSpace(string(markerBytes)) != good.Entry {
		return nil, nil, "the recorded Good entry has an invalid completion marker"
	}
	manifestPath := capture.ManifestPath(entry.Root)
	if why := requireRegularFile(manifestPath, "capture manifest"); why != "" {
		return nil, nil, why
	}
	manifest, err := capture.ReadManifest(entry.Root)
	if err != nil {
		return nil, nil, fmt.Sprintf("the recorded Good entry has an unreadable manifest (%v)", err)
	}
	if manifest.Platform != platform {
		return nil, nil, fmt.Sprintf("the recorded Good entry is for %s, not this jail's %s", manifest.Platform, platform)
	}
	if filepath.Clean(manifest.Home) != "/home/agent" {
		return nil, nil, fmt.Sprintf("the recorded Good entry was captured for %s, not the container home /home/agent", manifest.Home)
	}
	if why := compareManifestTree(entry.Tree, manifest); why != "" {
		return nil, nil, why
	}
	digest, err := treedigest.Of(entry.Tree)
	if err != nil {
		return nil, nil, fmt.Sprintf("cannot digest recorded Good entry %s (%v)", good.Entry, err)
	}
	if capture.Key(digest) != good.Entry {
		return nil, nil, fmt.Sprintf("recorded Good entry %s no longer matches its content address", good.Entry)
	}
	receiptsPath := capture.ReceiptsPath(entry.Root)
	if why := requireRegularFile(receiptsPath, "build receipts"); why != "" {
		return nil, nil, why
	}
	receipts, err := entrypoint.ReadBuildReceipts(receiptsPath)
	if err != nil {
		return nil, nil, fmt.Sprintf("cannot read the recorded Good entry's build receipts (%v)", err)
	}
	wantRepo, wantSubdir, err := cachedSourceIdentity(f.Source)
	if err != nil {
		return nil, nil, fmt.Sprintf("the selected upstream source cannot be checked (%v)", err)
	}
	if good.Read != nil && (good.Read.Repo != wantRepo || good.Read.Subdir != wantSubdir) {
		return nil, nil, "the Good build's recorded source differs from the selected upstream repository or subdirectory"
	}
	var matched *entrypoint.BuildReceipt
	for i := range receipts {
		r := &receipts[i]
		if r.Act != entrypoint.ReceiptActRecord || r.Key != good.Entry || r.Bin != f.Bin || r.Fork != f.Key() ||
			r.Platform != platform || filepath.Clean(r.Path) != filepath.Clean(entry.Root) ||
			(r.Revision != "" && good.Commit != "" && r.Revision != good.Commit) ||
			(r.Recipe != "" && good.Recipe != "" && r.Recipe != good.Recipe) ||
			(r.Series != "" && good.Series != "" && r.Series != good.Series) ||
			(r.Tree != "" && good.Tree != "" && r.Tree != good.Tree) ||
			r.Digest != capture.DigestHash(digest) {
			continue
		}
		repo, subdir, sourceErr := cachedSourceIdentity(r.Source)
		if sourceErr != nil || repo != wantRepo || subdir != wantSubdir {
			continue
		}
		matched = r
		break
	}
	if matched == nil {
		return nil, nil, "the exact Good.Entry has no admission record matching its owner, source, platform, and full content digest"
	}
	programPath := packdecl.ForkProgramPath(f.Bin, f.Produces)
	if programPath == "" || !manifestContains(manifest, programPath) {
		return nil, nil, "the current program path is absent from the recorded Good output; repair the current pack declaration and build"
	}
	program := filepath.Join(entry.Tree, filepath.FromSlash(programPath))
	resolved, err := resolveCapturedPath(entry.Tree, manifest.Home, program, 0)
	if err != nil {
		return nil, nil, fmt.Sprintf("the recorded program path %s cannot be resolved inside the captured tree (%v)", programPath, err)
	}
	fi, err := os.Stat(resolved)
	if err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm()&0o111 == 0 {
		return nil, nil, fmt.Sprintf("the recorded program path %s does not resolve to an executable file", programPath)
	}
	if why := validateCachedProgramRuntime(resolved, f, platform); why != "" {
		return nil, nil, why
	}
	return entry, matched, ""
}

func cachedSourceIdentity(source string) (string, string, error) {
	a, err := packsrc.Parse(source)
	if err != nil && strings.HasPrefix(source, "git+") && !strings.Contains(source, "?") {
		a, err = packsrc.Parse(source + "?ref=cached-good")
	}
	if err != nil {
		return "", "", err
	}
	return a.Repo, a.Path, nil
}

func requireRealDir(path, name string) string {
	fi, err := os.Lstat(path)
	if err != nil || !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Sprintf("recorded Good %s is missing or not a real directory", name)
	}
	return ""
}

func requireRegularFile(path, name string) string {
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return fmt.Sprintf("recorded Good %s is missing or not a regular file", name)
	}
	return ""
}

func compareManifestTree(root string, manifest *capture.Manifest) string {
	if manifest == nil {
		return "the recorded Good entry has no capture manifest"
	}
	listed := make(map[string]capture.ManifestEntry, len(manifest.Entries))
	for _, entry := range manifest.Entries {
		if !safeManifestPath(entry.Path) {
			return fmt.Sprintf("the capture manifest contains unsafe path %q", entry.Path)
		}
		if _, ok := listed[entry.Path]; ok {
			return fmt.Sprintf("the capture manifest repeats path %q", entry.Path)
		}
		listed[entry.Path] = entry
	}
	seen := make(map[string]bool, len(listed))
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if p == root {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		want, ok := listed[rel]
		if !ok {
			return fmt.Errorf("tree path %s is absent from the manifest", rel)
		}
		seen[rel] = true
		fi, err := os.Lstat(p)
		if err != nil {
			return err
		}
		mode := fi.Mode()
		switch {
		case mode.IsDir():
			if want.Kind != capture.KindDir || want.Mode != fmt.Sprintf("%04o", mode.Perm()) {
				return fmt.Errorf("directory %s disagrees with its manifest kind or mode", rel)
			}
		case mode.IsRegular():
			declaredMode, parseErr := strconv.ParseUint(want.Mode, 8, 32)
			if parseErr != nil || want.Kind != capture.KindFile || mode.Perm()&0o111 != fs.FileMode(declaredMode)&0o111 ||
				want.Size != fi.Size() {
				return fmt.Errorf("file %s disagrees with its manifest kind, executable mode, or size", rel)
			}
		case mode&os.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil || want.Kind != capture.KindSymlink || want.Target != target {
				return fmt.Errorf("symlink %s disagrees with its manifest target", rel)
			}
			if _, err := resolveCapturedPath(root, manifest.Home, p, 0); err != nil {
				return fmt.Errorf("symlink %s escapes or dangles in the captured tree", rel)
			}
		default:
			return fmt.Errorf("tree path %s has an unsupported file type", rel)
		}
		return nil
	})
	if err != nil {
		return fmt.Sprintf("the recorded Good tree and manifest disagree (%v)", err)
	}
	if len(seen) != len(listed) {
		return "the capture manifest names paths missing from the recorded Good tree"
	}
	return ""
}

func safeManifestPath(p string) bool {
	return p != "" && !strings.Contains(p, "\\") && !path.IsAbs(p) && path.Clean(p) == p && p != "." &&
		!strings.HasPrefix(p, "../")
}

func manifestContains(manifest *capture.Manifest, output string) bool {
	for _, e := range manifest.Entries {
		if e.Path == output || strings.HasPrefix(e.Path, strings.TrimSuffix(output, "/")+"/") {
			return true
		}
	}
	return false
}

func resolveCapturedPath(root, home, current string, depth int) (string, error) {
	if depth > 32 {
		return "", fmt.Errorf("too many symlinks")
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	currentAbs, err := filepath.Abs(current)
	if err != nil {
		return "", err
	}
	if !pathWithin(rootAbs, currentAbs) {
		return "", fmt.Errorf("path escapes capture tree")
	}
	fi, err := os.Lstat(currentAbs)
	if err != nil {
		return "", err
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		return currentAbs, nil
	}
	target, err := os.Readlink(currentAbs)
	if err != nil {
		return "", err
	}
	var next string
	if filepath.IsAbs(target) {
		home = filepath.Clean(home)
		target = filepath.Clean(target)
		if target != home && !strings.HasPrefix(target, home+string(filepath.Separator)) {
			return "", fmt.Errorf("absolute symlink leaves captured home")
		}
		rel, err := filepath.Rel(home, target)
		if err != nil {
			return "", err
		}
		next = filepath.Join(rootAbs, rel)
	} else {
		next = filepath.Join(filepath.Dir(currentAbs), target)
	}
	return resolveCapturedPath(rootAbs, home, next, depth+1)
}

func pathWithin(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func validateCachedProgramRuntime(program string, f packload.Fork, platform string) string {
	file, err := os.Open(program)
	if err != nil {
		return "the recorded program cannot be opened"
	}
	defer file.Close()
	reader := bufio.NewReader(io.LimitReader(file, 4096))
	first, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "the recorded program has an unreadable runtime header"
	}
	if strings.HasPrefix(first, "#!") {
		fields := strings.Fields(strings.TrimSpace(strings.TrimPrefix(first, "#!")))
		if len(fields) == 0 {
			return "the recorded program has an empty interpreter declaration"
		}
		interpreter := fields[0]
		command := ""
		if interpreter == "/usr/bin/env" {
			if len(fields) == 2 {
				command = fields[1]
			} else if len(fields) >= 3 && fields[1] == "-S" {
				command = fields[2]
			} else {
				return "the recorded program uses an unsupported env interpreter form"
			}
			if strings.Contains(command, "/") || command == "" {
				return "the recorded program's env interpreter is not a PATH command"
			}
		} else if len(fields) == 1 {
			command = map[string]string{
				"/bin/sh": "sh", "/usr/bin/sh": "sh", "/bin/bash": "bash", "/usr/bin/bash": "bash",
				"/usr/bin/node": "node", "/bin/node": "node",
			}[interpreter]
		} else {
			return "the recorded program uses an unsupported interpreter argument form"
		}
		switch command {
		case "sh", "bash":
			return ""
		case "node":
			if f.NodeFloor == "" {
				return "the recorded program requires Node but the selected base pack does not currently provision node_floor"
			}
			return ""
		default:
			return fmt.Sprintf("the recorded program requires unprovisioned or unsupported interpreter %q", interpreter)
		}
	}
	binary, err := elf.Open(program)
	if err != nil {
		return "the recorded program is neither a script with a supported interpreter nor an ELF executable"
	}
	defer binary.Close()
	if binary.Type != elf.ET_EXEC && binary.Type != elf.ET_DYN || binary.Type == elf.ET_DYN && binary.Entry == 0 {
		return fmt.Sprintf("the recorded native program has unsupported ELF executable type %s", binary.Type)
	}
	parts := strings.Split(platform, "/")
	if len(parts) != 2 || parts[0] != "linux" {
		return fmt.Sprintf("the recorded native program targets unsupported container platform %s", platform)
	}
	goarch := parts[1]
	wantMachine, ok := cachedELFMachine(goarch)
	if !ok || binary.Machine != wantMachine {
		return fmt.Sprintf("the recorded native program targets ELF machine %s, not this jail's %s", binary.Machine, goarch)
	}
	for _, p := range binary.Progs {
		if p.Type != elf.PT_INTERP {
			continue
		}
		b := make([]byte, p.Filesz)
		if _, err := p.ReadAt(b, 0); err != nil {
			return "the recorded native program's loader declaration is unreadable"
		}
		interpreter := strings.TrimRight(string(b), "\x00")
		if !cachedELFLoaderSupported(goarch, interpreter) {
			return fmt.Sprintf("the recorded native program requires unsupported loader %s", interpreter)
		}
	}
	return ""
}

func cachedELFMachine(goarch string) (elf.Machine, bool) {
	switch goarch {
	case "amd64":
		return elf.EM_X86_64, true
	case "arm64":
		return elf.EM_AARCH64, true
	default:
		return elf.EM_NONE, false
	}
}

func cachedELFLoaderSupported(goarch, loader string) bool {
	allowed := map[string]map[string]bool{
		"amd64": {
			"/lib64/ld-linux-x86-64.so.2": true, "/lib/x86_64-linux-gnu/ld-linux-x86-64.so.2": true,
			"/usr/lib/x86_64-linux-gnu/ld-linux-x86-64.so.2": true,
		},
		"arm64": {
			"/lib/ld-linux-aarch64.so.1": true, "/lib/aarch64-linux-gnu/ld-linux-aarch64.so.1": true,
			"/usr/lib/aarch64-linux-gnu/ld-linux-aarch64.so.1": true,
		},
	}
	return allowed[goarch][loader]
}

func writeCachedGoodFailure(w io.Writer, f packload.Fork, record *packsrc.CheckRecord, workspace string) {
	if record == nil {
		return
	}
	var latest *packsrc.EntryOutcome
	for i := range record.Outcomes {
		o := &record.Outcomes[i]
		if o.Kind != packsrc.OutcomeBuildFailed {
			continue
		}
		if latest == nil || o.At > latest.At {
			latest = o
		}
	}
	if replay := record.ApplyErrAtLastCheck(); replay != nil {
		fmt.Fprintf(w, "Current patch replay failure remains recorded: %s; repair its cause, then retry with `yolo capture %s`.\n",
			replay.Error, f.Bin)
	}
	if latest == nil {
		if record.ApplyErrAtLastCheck() == nil && currentCachedGoodPatchFailure(f, record) == nil {
			fmt.Fprintln(w, "No current failure is recorded; this is operator-requested prior-good selection.")
		}
		return
	}
	if latest.Kind == packsrc.OutcomeBuildFailed {
		log := filepath.Join(paths.GlobalStorage(), "logs", "build-"+run.PatchedCopySlug(f.Key())+".log")
		if workspace != "" {
			log = filepath.Join(workspace, ".yolo", "build-"+run.PatchedCopySlug(f.Key())+".log")
		}
		fmt.Fprintf(w, "Current build failure remains recorded at %s: %s; the existing log %s is unchanged. Repair with `yolo capture %s`.\n",
			latest.Commit, latest.Error, log, f.Bin)
	}
}
