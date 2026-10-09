package hostfloor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
)

// openPreparedInstalledFile is replaced only by serial tests that model a read failure.
var openPreparedInstalledFile = os.Open

// PreparePatched resolves one patched program's current delivery choice without installing a floor
// entry or writing user artifacts. allowAdvance permits the selected foreground actor to advance;
// false still resolves cached failure authority but never checks or builds.
func (f *Floor) PreparePatched(ctx context.Context, p Program, allowAdvance bool) (*PatchedPreparation, error) {
	copyOfProgram, err := cloneProgram(p)
	if err != nil {
		return nil, err
	}
	initialStatus := f.Status(p)
	initial := clonePatchedState(f.patched(p))
	if initialStatus.Newer {
		return nil, newerRecordError{initialStatus.Reason}
	}
	if initialStatus.Disposition == NoEntry && f.noEntryReason(p) != "" {
		return nil, fmt.Errorf("%w for %s: %s", ErrNoEntry, p.Bin(), initialStatus.Reason)
	}

	installed := f.patchedServingState(p, initialStatus.Record, initial)
	shouldAdvance := allowAdvance && initial.PatchFailure == nil && f.advances(p, initialStatus)
	resolved := initial
	switch {
	case f.ResolvePatched != nil:
		resolved = clonePatchedState(f.ResolvePatched(ctx, copyOfProgram, cloneRecord(installed), shouldAdvance))
	case shouldAdvance && f.Advance != nil:
		resolved = clonePatchedState(f.Advance(ctx, copyOfProgram, cloneRecord(installed)))
	}
	state := mergePreparedPatchedState(initial, resolved)
	currentStatus := f.Status(copyOfProgram)
	preparation := &PatchedPreparation{
		floor:        f,
		program:      copyOfProgram,
		goos:         f.GOOS,
		goarch:       f.GOARCH,
		recipe:       state.Recipe,
		state:        clonePatchedState(state),
		failure:      clonePatchFailure(state.PatchFailure),
		installed:    cloneRecord(installed),
		selectedGood: clonePatchedBuild(state.Good),
		delivery:     patchedPreparationInstall,
	}
	if preparation.recipe == "" {
		return preparation, fmt.Errorf("%w for %s: %s", ErrNoEntry, p.Bin(), state.Reason)
	}
	if state.AuthorityError != "" || state.OperationError != "" {
		return preparation, preparationError(p, state, preparationDiagnostics(state))
	}
	if state.PatchFailure == nil {
		return preparation, nil
	}

	bypass := f.AllowPatchFailures != nil && f.AllowPatchFailures()
	preparation.bypassRequested = bypass
	if !bypass {
		f.writePatchFailureOnce(p, state)
		return preparation, preparationError(p, state, "patch application failure")
	}
	if f.preparedRecordServes(p, currentStatus.Record, state) {
		preparation.delivery = patchedPreparationKeepInstalled
		preparation.installed = cloneRecord(currentStatus.Record)
		f.writePatchFailureOnce(p, state)
		f.sayContinuingOnce(state, "CONTINUING: using installed compatible build %s; skips this fork's advance", currentStatus.Record.Version)
		return preparation, nil
	}
	if why := f.preparedGoodUsable(p, state, preparation.selectedGood); why == "" {
		preparation.delivery = patchedPreparationInstallGood
		f.writePatchFailureOnce(p, state)
		f.sayContinuingOnce(state, "CONTINUING: installing the selected admitted build %s; skips this fork's advance", preparation.selectedGood.Label)
		return preparation, nil
	}
	f.writePatchFailureOnce(p, state)
	return preparation, preparationError(p, state, "no compatible installed copy or complete current-series store build is available for the literal patch-failure bypass")
}

// EnsurePrepared consumes exactly one preparation from this Floor and declaration. The selected
// Good build is carried through install rather than re-read after another actor may have moved it.
func (f *Floor) EnsurePrepared(ctx context.Context, p Program, prepared *PatchedPreparation) (Status, Outcome, error) {
	if err := f.validatePatchedPreparation(p, prepared); err != nil {
		return f.Status(p), "", retainPreparationFailure(prepared, err)
	}
	st := f.Status(p)
	if prepared.delivery == patchedPreparationInstallGood {
		if err := f.preparedGoodError(p, prepared.state, prepared.selectedGood); err != nil {
			return st, "", retainPreparationFailure(prepared,
				fmt.Errorf("%s: selected patched build is no longer installable: %w", p.Bin(), err))
		}
	}
	if st.Newer {
		return st, "", retainPreparationFailure(prepared, newerRecordError{st.Reason})
	}
	if st.Disposition == NoEntry {
		return st, "", retainPreparationFailure(prepared, fmt.Errorf("%w for %s: %s", ErrNoEntry, p.Bin(), st.Reason))
	}
	if prepared.delivery == patchedPreparationKeepInstalled {
		if st.Record == nil || !reflect.DeepEqual(st.Record, prepared.installed) || !f.preparedRecordServes(p, st.Record, prepared.state) {
			return st, "", retainPreparationFailure(prepared,
				fmt.Errorf("%s: the compatible installed copy changed after patched-Floor preparation", p.Bin()))
		}
		return st, Current, nil
	}
	if prepared.failure == nil && st.Disposition == Provisioned && st.Pending == "" {
		return st, Current, nil
	}
	if err := f.ensureDir("bin", "programs", "records", "locks"); err != nil {
		return st, "", retainPreparationFailure(prepared, err)
	}
	lk, err := acquire(f.lockPath(p.Bin()), true, func(pid int) {
		f.say("waiting for pid %d, which is installing %s into yolo's floor", pid, p.Bin())
	})
	if err != nil {
		return st, "", retainPreparationFailure(prepared, err)
	}
	defer lk.release()

	st = f.Status(p)
	if err := f.validatePatchedPreparation(p, prepared); err != nil {
		return st, "", retainPreparationFailure(prepared, err)
	}
	if prepared.delivery == patchedPreparationInstallGood {
		if err := f.preparedGoodError(p, prepared.state, prepared.selectedGood); err != nil {
			return st, "", retainPreparationFailure(prepared,
				fmt.Errorf("%s: selected patched build is no longer installable: %w", p.Bin(), err))
		}
	}
	if st.Newer {
		return st, "", retainPreparationFailure(prepared, newerRecordError{st.Reason})
	}
	if st.Disposition == NoEntry {
		return st, "", retainPreparationFailure(prepared, fmt.Errorf("%w for %s: %s", ErrNoEntry, p.Bin(), st.Reason))
	}
	if prepared.delivery == patchedPreparationKeepInstalled {
		if st.Record == nil || !reflect.DeepEqual(st.Record, prepared.installed) || !f.preparedRecordServes(p, st.Record, prepared.state) {
			return st, "", retainPreparationFailure(prepared,
				fmt.Errorf("%s: the compatible installed copy changed after patched-Floor preparation", p.Bin()))
		}
		return st, Current, nil
	}
	if prepared.failure == nil && st.Disposition == Provisioned && st.Pending == "" {
		return st, Current, nil
	}

	var rec *Record
	if prepared.state.Good != nil && prepared.state.Good.Entry == nil && prepared.delivery != patchedPreparationInstallGood {
		err = errors.New(f.patchedGoneReason(p, prepared.state.Good))
	} else {
		why := st.Reason
		if st.Pending != "" {
			why = st.Pending
		}
		f.say("installing %s into yolo's floor (%s): %s", p.Bin(), why, f.describeRecipe(p))
		rec, err = f.installSelected(ctx, p, &prepared.state)
	}
	if err != nil {
		if prepared.delivery == patchedPreparationInstallGood {
			if noEntry := noEntryReasonOf(err); noEntry != "" {
				st.Disposition, st.Reason = NoEntry, noEntry
			}
			return st, "", retainPreparationFailure(prepared, err)
		}
		noEntry := noEntryReasonOf(err)
		if st.Disposition == Provisioned && prepared.failure == nil && noEntry == "" &&
			!f.preparedRecordIsNearMiss(p, st.Record, prepared.state) {
			f.say("could not reinstall %s (%v); running the installed %s", p.Bin(), err, st.Record.Version)
			return st, Kept, nil
		}
		if st.Disposition == Provisioned {
			if noEntry != "" {
				f.say("%s: %s, so the floor no longer runs the installed %s", p.Bin(), noEntry, st.Record.Version)
			} else {
				f.say("could not install %s (%v); the installed %s is not the build the pack now asks for, so the floor no longer runs it",
					p.Bin(), err, st.Record.Version)
			}
			f.removeInstalled(p)
			st.Disposition, st.Record, st.Pending = Missing, nil, ""
			st.Reason = "the install the pack now asks for failed, and the installed copy was not it"
		}
		if noEntry != "" {
			st.Disposition, st.Reason = NoEntry, noEntry
		}
		return st, "", retainPreparationFailure(prepared, err)
	}
	outcome := Installed
	if st.Disposition == Provisioned {
		outcome = Updated
	}
	f.say("installed %s %s → %s", p.Bin(), rec.Version, f.Launcher(p.Bin()))
	return f.Status(p), outcome, nil
}

// sayContinuingOnce says the bypass's CONTINUING line unless the advance that took the skip already
// did: like the error block (writePatchFailureOnce), it is said once per operation.
func (f *Floor) sayContinuingOnce(state PatchedState, format string, args ...any) {
	if state.ContinuingSaid {
		return
	}
	f.say(format, args...)
}

func mergePreparedPatchedState(initial, resolved PatchedState) PatchedState {
	out := clonePatchedState(resolved)
	if out.Recipe == "" {
		out.Recipe = initial.Recipe
	}
	if out.Good == nil && initial.Good != nil && initial.Recipe == out.Recipe &&
		out.PatchFailure == nil && initial.PatchFailure == nil {
		out.Good = clonePatchedBuild(initial.Good)
	}
	if out.PatchFailure == nil {
		out.PatchFailure = clonePatchFailure(initial.PatchFailure)
	} else if initial.PatchFailure != nil && !reflect.DeepEqual(out.PatchFailure, initial.PatchFailure) {
		out.AuthorityError = mergeDistinctMessage(out.AuthorityError, "patch-failure authority changed during preparation")
	}
	out.OperationError = mergeDistinctMessage(out.OperationError, initial.OperationError)
	out.AuthorityError = mergeDistinctMessage(out.AuthorityError, initial.AuthorityError)
	out.AdvanceBypassed = out.AdvanceBypassed || initial.AdvanceBypassed
	if out.Reason == "" {
		out.Reason = initial.Reason
	}
	return out
}

func (f *Floor) validatePatchedPreparation(p Program, prepared *PatchedPreparation) error {
	if prepared == nil || prepared.floor != f {
		return errors.New("patched-Floor preparation belongs to a different Floor")
	}
	currentProgram, err := cloneProgram(p)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(currentProgram, prepared.program) {
		return fmt.Errorf("%s: program declaration changed after patched-Floor preparation", p.Bin())
	}
	if prepared.failure != nil && (prepared.delivery == patchedPreparationInstall || !prepared.bypassRequested) {
		return preparationError(p, prepared.state, "patch application failure was not prepared for a compatible literal bypass")
	}
	if prepared.state.AuthorityError != "" || prepared.state.OperationError != "" {
		return preparationError(p, prepared.state, preparationDiagnostics(prepared.state))
	}
	if prepared.goos != f.GOOS || prepared.goarch != f.GOARCH {
		return fmt.Errorf("%s: host platform changed after patched-Floor preparation", p.Bin())
	}
	current := f.patched(p)
	if current.Recipe != prepared.recipe {
		return fmt.Errorf("%s: patch recipe changed after patched-Floor preparation", p.Bin())
	}
	if current.AuthorityError != "" {
		return fmt.Errorf("%s: patch-failure authority is unresolved after preparation: %s", p.Bin(), current.AuthorityError)
	}
	if current.PatchFailure != nil && !reflect.DeepEqual(current.PatchFailure, prepared.failure) {
		return fmt.Errorf("%s: patch-failure authority changed after patched-Floor preparation", p.Bin())
	}
	return nil
}

func (f *Floor) preparedRecordServes(p Program, rec *Record, state PatchedState) bool {
	return rec != nil && rec.Bin == p.Bin() && rec.Pack == p.Pack &&
		rec.Via == packdecl.ViaSource && rec.Declared == declared(p.Install) &&
		state.Recipe != "" && rec.Recipe == state.Recipe && rec.Version != "" &&
		rec.Capture != "" && rec.Revision != "" &&
		(rec.Node == "" || packdecl.SatisfiesNodeFloor(rec.Node, p.Install.NodeFloor)) &&
		f.preparedInstalledCopyUsable(p, rec)
}

func (f *Floor) preparedInstalledCopyUsable(p Program, rec *Record) bool {
	if !filepath.IsAbs(rec.Dir) {
		return false
	}
	programs := f.programsDir(p.Bin())
	recordDir := filepath.Clean(rec.Dir)
	resolvedPrograms, err := filepath.EvalSymlinks(programs)
	if err != nil {
		return false
	}
	resolvedRecordDir, ok := preparedResolvedWithin(programs, recordDir)
	if !ok {
		return false
	}
	rel, err := filepath.Rel(resolvedPrograms, resolvedRecordDir)
	if err != nil || rel == "." || !filepath.IsLocal(rel) || filepath.Dir(rel) != "." ||
		!preparedRegularPath(f.Dir, f.Launcher(p.Bin()), true, false) {
		return false
	}
	marker := filepath.Join(recordDir, completeMarker)
	if !preparedRegularPath(recordDir, marker, false, false) {
		return false
	}
	home := filepath.Join(recordDir, "home")
	if _, ok := preparedResolvedWithin(recordDir, home); !ok {
		return false
	}
	programPath := p.Install.ProgramPath()
	if programPath == "" || !filepath.IsLocal(filepath.FromSlash(programPath)) {
		return false
	}
	entry := filepath.Join(home, filepath.FromSlash(programPath))
	if filepath.Clean(rec.Entry) != filepath.Clean(entry) {
		return false
	}
	resolvedRecordEntry, ok := preparedResolvedWithin(home, rec.Entry)
	if !ok {
		return false
	}
	resolvedDeclaredEntry, ok := preparedResolvedWithin(home, entry)
	if !ok || resolvedRecordEntry != resolvedDeclaredEntry {
		return false
	}
	if len(rec.Exec) == 1 {
		if rec.Node != "" || rec.Exec[0] != rec.Entry || !preparedRegularPath(home, entry, true, false) {
			return false
		}
	} else if len(rec.Exec) == 2 {
		if rec.Node == "" || filepath.Base(rec.Node) != rec.Node || rec.Exec[1] != rec.Entry ||
			!packdecl.SatisfiesNodeFloor(rec.Node, p.Install.NodeFloor) ||
			rec.Exec[0] != filepath.Join(f.nodeBin(rec.Node), "node") ||
			!preparedRegularPath(f.nodeDir(rec.Node), rec.Exec[0], true, false) ||
			!preparedRegularPath(home, rec.Entry, false, true) {
			return false
		}
	} else {
		return false
	}
	for _, declaredPath := range p.Install.Produces {
		if declaredPath == "" || !filepath.IsLocal(filepath.FromSlash(declaredPath)) {
			return false
		}
		path := filepath.Join(home, filepath.FromSlash(declaredPath))
		resolved, ok := preparedResolvedWithin(home, path)
		if !ok {
			return false
		}
		info, err := os.Stat(resolved)
		if err != nil || !(info.Mode().IsRegular() || info.IsDir()) {
			return false
		}
	}
	return true
}

func preparedResolvedWithin(root, path string) (string, bool) {
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", false
	}
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(resolvedRoot, resolvedPath)
	if err != nil || rel == "." || !filepath.IsLocal(rel) {
		return "", false
	}
	return resolvedPath, true
}

func preparedRegularPath(root, path string, executable, readable bool) bool {
	resolved, ok := preparedResolvedWithin(root, path)
	if !ok {
		return false
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() || executable && info.Mode().Perm()&0o111 == 0 {
		return false
	}
	if readable {
		file, err := openPreparedInstalledFile(resolved)
		if err != nil {
			return false
		}
		var one [1]byte
		_, readErr := file.Read(one[:])
		closeErr := file.Close()
		if readErr != nil && !errors.Is(readErr, io.EOF) || closeErr != nil {
			return false
		}
	}
	return true
}

func (f *Floor) preparedGoodError(p Program, state PatchedState, good *PatchedBuild) error {
	if good != nil && good.Entry != nil {
		root := filepath.Clean(good.Entry.Root)
		if filepath.Base(root) != good.Entry.Key || filepath.Base(filepath.Dir(root)) != "entries" {
			return errors.New("the selected capture entry is not rooted in its recorded store path")
		}
		store := &capture.Store{Dir: filepath.Dir(filepath.Dir(root))}
		resolved, err := store.Resolve(good.Entry.Key)
		if err != nil {
			return err
		}
		if filepath.Clean(resolved.Root) != root {
			return errors.New("the selected capture entry changed its root path")
		}
	}
	if why := f.preparedGoodUsable(p, state, good); why != "" {
		return errors.New(why)
	}
	return nil
}

func retainPreparationFailure(prepared *PatchedPreparation, err error) error {
	if err == nil || prepared == nil || prepared.failure == nil {
		return err
	}
	var found *packsrc.PatchFailure
	if errors.As(err, &found) && reflect.DeepEqual(found, prepared.failure) {
		return err
	}
	return errors.Join(prepared.failure, err)
}

func (f *Floor) preparedRecordIsNearMiss(p Program, rec *Record, state PatchedState) bool {
	return !f.preparedRecordServes(p, rec, state)
}

func (f *Floor) patchedServingState(p Program, rec *Record, state PatchedState) *Record {
	if !f.preparedRecordServes(p, rec, state) {
		return nil
	}
	return rec
}

func (f *Floor) preparedGoodUsable(p Program, state PatchedState, good *PatchedBuild) string {
	if good == nil {
		if state.Reason != "" {
			return state.Reason
		}
		return "no selected current-series good build is available"
	}
	if state.Recipe == "" || good.Recipe != state.Recipe {
		return "the selected build is not for the current recipe"
	}
	if good.Entry == nil {
		return f.patchedGoneReason(p, good)
	}
	if !good.Entry.Complete() {
		return "its complete capture-store entry is no longer present"
	}
	if why := buildUnusableAs(p, f.patchedWhat(p, good), good.Entry); why != "" {
		return why
	}
	if why := f.storeProgramLoaderProblem(good.Entry, p.Install.ProgramPath()); why != "" {
		return f.patchedWhat(p, good) + " " + why
	}
	if !packdecl.SatisfiesNodeFloor(f.NodeVersion(), p.Install.NodeFloor) {
		return fmt.Sprintf("the floor's Node %s is below the pack's node_floor %s", f.NodeVersion(), p.Install.NodeFloor)
	}
	return ""
}

func preparationDiagnostics(state PatchedState) string {
	var messages []string
	if state.AuthorityError != "" {
		messages = append(messages, "patch-failure authority is unresolved: "+state.AuthorityError)
	}
	if state.OperationError != "" {
		messages = append(messages, "patched fork operation failed: "+state.OperationError)
	}
	return strings.Join(messages, "; ")
}

func preparationError(p Program, state PatchedState, message string) error {
	var errs []error
	if state.PatchFailure != nil {
		errs = append(errs, state.PatchFailure)
	}
	if message != "" {
		errs = append(errs, errors.New(message))
	}
	return fmt.Errorf("%s: %w", p.Bin(), errors.Join(errs...))
}

func mergeDistinctMessage(first, second string) string {
	if first == "" {
		return second
	}
	if second == "" {
		return first
	}
	for _, part := range strings.Split(first, "; ") {
		if part == second {
			return first
		}
	}
	return joinMessage(first, second)
}

func joinMessage(first, second string) string {
	if first == "" {
		return second
	}
	return first + "; " + second
}
