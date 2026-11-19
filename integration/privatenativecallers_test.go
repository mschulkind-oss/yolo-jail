package integration

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// The constructor controls alone would stay green if a runtime caller reverted
// to exec.Command. Pin the actual guarded edges, including fixture registration.
func TestNativeFixtureRuntimeCallersKeepTheirExternalAdmission(t *testing.T) {
	for _, edge := range []struct {
		file, caller, callee string
		count                int
	}{
		{"forkbuild_test.go", "TestForkBuildDeliversTheForkInPlaceOfItsBase", "usePrivateNativeFixture", 1},
		{"forkbuild_test.go", "TestForkBuildDeliversTheForkInPlaceOfItsBase", "plainForkNativeFixture", 1},
		{"detachkeys_linux_test.go", "TestTheDetachSequenceNeverLeavesAnAttachedSessionHeadless", "usePrivateNativeFixture", 1},
		{"detachkeys_linux_test.go", "TestTheDetachSequenceNeverLeavesAnAttachedSessionHeadless", "privateNativeWorkspaceAdmission", 1},
		{"detachkeys_linux_test.go", "TestTheDetachSequenceNeverLeavesAnAttachedSessionHeadless", "privateLaunchCommand", 1},
		{"detachkeys_linux_test.go", "TestTheDetachSequenceNeverLeavesAnAttachedSessionHeadless", "privateLaunchEnvironment", 1},
		{"packtreeprogram_test.go", "TestPackTreeProgramUsesThePrebuiltWithoutSources", "usePrivateNativeFixture", 1},
		{"packtreeprogram_test.go", "TestPackTreeProgramUsesThePrebuiltWithoutSources", "packPrebuiltNativeFixture", 1},
		{"packtreeprogram_test.go", "TestPackTreeProgramUsesThePrebuiltOverItsSourceOnlyWithTheOptIn", "usePrivateNativeFixture", 1},
		{"packtreeprogram_test.go", "TestPackTreeProgramUsesThePrebuiltOverItsSourceOnlyWithTheOptIn", "packPrebuiltNativeFixture", 1},
		{"packtreeprogram_test.go", "TestPackTreeProgramBuildsOnceAndRebuildsOnChange", "usePrivateNativeFixture", 1},
		{"packtreeprogram_test.go", "TestPackTreeProgramBuildsOnceAndRebuildsOnChange", "packTreeNativeFixture", 1},
		{"packtreeprogram_test.go", "TestPackTreeProgramBuildFailureRefusesOnlyIt", "usePrivateNativeFixture", 1},
		{"packtreeprogram_test.go", "TestPackTreeProgramBuildFailureRefusesOnlyIt", "packTreeFailureNativeFixture", 1},
		{"packbuild_test.go", "TestPackBuildPrebuildsWhatALaunchThenHits", "usePrivateNativeFixture", 1},
		{"packbuild_test.go", "TestPackBuildPrebuildsWhatALaunchThenHits", "packBuildNativeFixture", 1},
		{"preparationscheduler_linux_test.go", "TestPreparationSchedulerRealWorkerLifecycle", "preparationCommand", 2},
		{"preparationscheduler_linux_test.go", "TestPreparationSchedulerRealWorkerLifecycle", "usePrivateNativeFixture", 1},
		{"preparationscheduler_linux_test.go", "prepCLI", "preparationCommand", 1},
		{"preparationscheduler_linux_test.go", "preparationCommand", "privateNativeCommandChecked", 1},
		{"backgroundadvance_linux_test.go", "newXBFixture", "usePrivateNativeFixture", 1},
		{"backgroundadvance_linux_test.go", "TestBackgroundAdvanceSignalsFenceAndRecoverItsRealBuildWorkspace", "xbRequesterCommand", 1},
		{"privatenative_linux_test.go", "xbRequesterCommand", "privateNativeCommandChecked", 1},
		{"harness_test.go", "warmJail", "privateNativeCommandChecked", 1},
		{"harness_test.go", "warmJail", "privateNativeWorkspaceAdmission", 1},
		{"harness_test.go", "runLaunch", "privateNativeWorkspaceAdmission", 1},
		{"privatenative_linux_test.go", "xbRequesterCommand", "privateNativeWorkspaceAdmission", 1},
		{"hostrun_test.go", "TestHostRunDeliversAHostCommandToTheJail", "privateNativeWorkspaceAdmission", 1},
		{"hostrun_test.go", "TestHostRunDeliversAHostCommandToTheJail", "privateNativeCommandChecked", 1},
		{"versionstamp_test.go", "TestTheJailsYoloCarriesItsMatchedBundleStamp", "privateNativeCommandChecked", 1},
		{"privatesuite_test.go", "privateLaunchCommandChecked", "privateNativeCommandChecked", 1},
	} {
		t.Run(edge.caller+"/"+edge.callee, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), edge.file, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Name.Name != edge.caller {
					continue
				}
				ast.Inspect(fn.Body, func(node ast.Node) bool {
					call, ok := node.(*ast.CallExpr)
					if ok {
						name, ok := call.Fun.(*ast.Ident)
						if ok && name.Name == edge.callee {
							calls++
						}
					}
					return true
				})
			}
			if calls != edge.count {
				t.Fatalf("actual native caller %s/%s has %d %s calls, want %d; restore external admission before runtime execution", edge.file, edge.caller, calls, edge.callee, edge.count)
			}
		})
	}
}
