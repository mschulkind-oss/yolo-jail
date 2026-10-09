# Completion experiment (not production wiring)

The production `just done` is [`scripts/completion-check.py`](../../scripts/completion-check.py),
built from the [design's What shipped section](../../docs/design/change-aware-completion.md#what-shipped).
It uses none of this package, which is kept as the earlier experiment.

This package keeps the experimental completion sources and their focused tests
in the repository. The Go helper reports observed build inputs; the Python
components capture those inputs, check bounded file reads, and control synthetic
helper executions. The [completion design](../../docs/design/change-aware-completion.md)
describes the intended feature and the work still needed.

This is not a production completion command, reusable baseline, quality result,
or proof of all inputs a real toolchain reads. The production
[pin tool](../pack-binaries/) and [Justfile](../../Justfile) are unchanged; no
production command dispatches to this package.

The Python components use only the standard library and local siblings:

```text
full_controller → capture, closure, official_adapter
 official_adapter → capture, closure
 capture → closure
```

The Go helper uses the existing module imports and vendor tree. Its command
skeleton needs copies of the production [pin writer](../pack-binaries/pin.go) and
[build recipe](../pack-binaries/recipe.go); the production toolchain pin remains in
the [production toolchain source](../pack-binaries/toolchain.go). No external overlay, historical
executable, receipt, or durable scratch path is needed. This is a diagnostic
package: do not use its inherited pin, stage, or seed modes for production.

## Focused validation

From the repository root:

```sh
go test -v -short -count=1 -timeout=120s ./tools/completion-experiment
go test -v -short -race -count=1 -timeout=120s ./tools/completion-experiment
go vet ./tools/completion-experiment
```

The standard-library Python fixtures are explicitly invoked; no project recipe
has been changed to run them:

```sh
python3 -m unittest discover -v -s tools/completion-experiment -p 'test_*.py'
```

The [controller fixture](test_full_controller.py) attempts one temporary setup
build per Python-process fixture setup, with a 120-second deadline:

```sh
go test -c -short -mod=vendor -o <temporary>/emitter.test ./tools/completion-experiment
```

It inherits the Go environment and shared caches, fails without retry or
fallback, kills and reaps its owned compiler process group on interruption,
and registers temporary-directory cleanup before setup can fail. It checks
current source and artifact bindings in memory. The other Python components
use disposable test-owned file trees and do not compile Go.

## Deliberate nonclaims

These are synthetic fixture controls, not production Go outcomes. Their
reports leave profile coverage unknown, quality results empty, and baseline
publication false: `profileState="Unknown"`, `qualityOutcomes=[]`, and
`baselinePublished=false`.

Protected prose comparison remains unwired. The package establishes no
zero-process comparison, production acquisition provenance, complete compiler
or library input coverage, Node/Pi coverage, reusable baseline publication, or
change to the default completion command. The host-dependent diagnostic that
scans installed Go/Pi trees is not part of the portable unit suite.
