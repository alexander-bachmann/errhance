# Restoring Useful Kubernetes Coverage

This report records the restoration before subsequent performance work. See
[performance measurements](performance.md) for the current on-save path and
matched before/after timings.

## Policy

The earlier global safety fallback made the tool ineffective: a single unresolved
consumer blocked the whole loaded project. The default now distinguishes:

- Invalid or ambiguous local rewrite: skip it.
- Known traced identity-sensitive consumer: protect its producer.
- Unknown consumer compatibility: warn, but do not veto unrelated rewrites.

This deliberately does not promise whole-program behavioral equivalence. Error
messages and concrete identity change when wrapping; external or unresolved
consumers can still be affected. Local bindings, non-nil guards, result types,
closure isolation, and pre-write type validation remain required.

Local rewrites inside loops, switch/type-switch/select statements, and labels have
been restored. Loop-carried assignments invalidate incoming facts, while fresh
local assignments remain analyzable. Ordinary read-only captures no longer block
surrounding rewrites. Deferred consumers of named results receive targeted
protection when they observe error identity. Contract provenance separates tuple
results instead of attributing one result's error to an unrelated interface value.

## Matched Kubernetes Comparison

Kubernetes v1.32.2, commit `67a30c0adcf52bd3f56ff0893ce19966be12991f`.
Linux/amd64, Go 1.26.7, four runtime workers, Go build parallelism two, soft Go
memory limit 2 GiB. The limit does not cap process RSS or subprocess memory.
Default mode, not overwrite; vendor and generated files excluded normally.

| Version / selection | Files | Wraps | Wall time | Outcome |
| --- | ---: | ---: | ---: | --- |
| Original `d92f170`, root | 1,925 | 9,147 | 11.54 s | Applied successfully |
| Overly conservative rewrite, root | 0 | 0 | 191.28 s | Package-loading failure |
| Overly conservative rewrite, `pkg` | 0 | 0 | 56.98 s | Global blocking |
| Restored final policy, root | **1,761** | **7,901** | **184.72 s** | Validated preview, then applied to disposable checkout |

The restored run recovers about 86% of the original raw wrap count. It is not
expected to reproduce every old edit: known contracts, concrete return types,
mutated captures, build exclusions, unloadable packages, and uncertain local
origins still cause skips. An intermediate subtree trial restored 2,417 wraps
across 547 `pkg` files; the original root patch contained 2,782 added wrappers
across 600 files in that subtree.

Performance is still substantially worse than the original syntax-only tool.
The final root run peaked at 4,495,248 KiB RSS (about 4.3 GiB). Loading/types and
overlay validation across a workspace this large are expensive; these numbers
are not an acceptable basis for claiming the performance problem is solved.
Active workspace members share loads, and initial AST/type graphs are released
before overlay validation, but further profiling is warranted. Timings were not
isolated benchmarks and cache warmth differed.

Unrelated nested tooling and example package errors no longer abort valid work.
Those packages remain unchanged and receive diagnostics. A partial result is not
a claim that the entire Kubernetes source tree or its consumers were analyzed.

## Validation Performed

The final root patch passed `git apply --check`, applied to a separate pristine
worktree, and passed `git diff --check`. All 1,761 changed files match the validated
preview. The original current-tool checkout remains unmodified.
Repeating the full-root preview on the rewritten checkout succeeded with an empty
patch and zero proposed changes (156.27 seconds), confirming project-level
idempotence for this trial.

Ran this exact command before and after applying the full patch:

```sh
go test -count=1 -timeout=90s \
  ./pkg/util/filesystem \
  ./pkg/kubelet/checkpointmanager \
  ./pkg/volume/util/operationexecutor \
  ./staging/src/k8s.io/apimachinery/pkg/util/yaml
```

**All four packages passed both runs.** Their existing tests and expectations
were not adjusted. These packages contain 29 inspected return replacements across
seven files. Additional loop and closure samples were inspected in
`cluster/images/etcd/migrate/versions.go` and `cmd/kube-apiserver/app/server.go`.
Inspected YAML reader/decoder paths preserve direct EOF handling.

This is a sample, not an audit of all 7,901 rewrites. No full Kubernetes runtime,
integration, or cluster suite was run. Exact-error-text assertions elsewhere may
still fail; the earlier Prometheus trial demonstrates that risk.

Regression tests cover unrelated interface assertions, warning-only unknown
contracts, known boxed errors, reader signatures, tuple-slot provenance, named
result/defer identity, loop-carried mutation, labels, fallthrough, partial package
failure, invalid test variants, workspace membership, and newly invalid overlay
dependents. Existing fuzz properties continue to require valid output,
idempotence, and positive rewrite counts for supported inputs.

Local verification after restoration: **372 tests passed under the race detector**,
the full suite passed on Go 1.22.5, vet and whitespace checks passed, and a
20-second structured fuzz run completed 14,879 executions without a failure.
Native macOS/Windows CI has not been executed locally.

## Local Artifacts

Everything listed here is inside the ignored `.trials/` directory:

- `projects/kubernetes`: unchanged comparison checkout.
- `projects/kubernetes-original`: original tool's 9,147 edits.
- `projects/kubernetes-restored`: restored tool's validated 7,901 edits.
- `logs/KUBERNETES.md`: original regression measurements.
- `logs/kubernetes-root-final-restored.{patch,stderr,time}`: final preview and timing.
- `logs/kubernetes-targeted-{baseline,restored}.{stdout,stderr,time}`: test results.
- `logs/kubernetes-restored-idempotence.{patch,stderr,time}`: empty repeat preview.

Build with `go build -o .trials/bin/errhance-restored .`, then run that binary with
`--diff` from an unchanged checkout to repeat analysis. Prepare dependencies and
workspace context first; the tool does not download missing dependencies.
