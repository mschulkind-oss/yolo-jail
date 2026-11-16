# Red-phase receipts: agent-scoped exact model menus

These are development receipts, not passing validation. The current validation commands and their results are in the final acceptance report.

## Preserved raw projection regression

[`claude-exact-model-menu-projection-red.txt`](claude-exact-model-menu-projection-red.txt) is the original failing output from `TestDeriveContextProjectsProviderModelsAndNeverExposesViews`. Before projection was wired into the derive context, Lua observed the unfiltered model map and did not see `models_only`.

## Recorded schema regression

The initial composition/validation regression failed to compile because `ModelsContribution.Agents` had not yet been added. Its output file was later reused by a green focused test, so the raw failure was overwritten; the recorded diagnosis is from the implementation session summary, not a reconstructed raw log.

## Recorded production-render regression

`go test ./internal/entrypoint -run 'TestClaudeExactModelMenuProjectsScopedOnlyAtProductionCallers|TestScopedClaudeOnlyLeavesPiAndCopilotRendersByteIdentical' -count=1` first failed because the test fixture inverted the `EnforceModels` boolean pointer. After that was fixed, the Claude render comparison exposed a fixture mistake: it compared a scoped `only` against an add-only baseline rather than the same unscoped `only`. Both fixture defects were corrected; the final selector passes.

A subsequent Pi assertion initially expected the requester's historical wildcard for the current ten-row company-add fixture. The current render instead produces ten explicit native Bedrock model ids, as source-verified in [`claude-exact-model-menu-grounding.md`](../claude-exact-model-menu-grounding.md) and agreed by the supervisor. This was a test expectation correction; no production Pi behavior changed.

## Other fixture-only failures

- One combined package test initially failed to compile because the new intersection test omitted its `internal/modelview` import; fixed before the final focused package run.
- The same intersection test initially omitted the surviving `default` alias from its expected list; corrected to preserve the provider's alias order.
- The Copilot output check initially omitted the `wire-bridge` profile and therefore composed no `providers.json`; the helper now selects the real bridge profile and asserts the file is non-empty.
