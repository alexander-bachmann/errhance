# Performance and On-Save Use

Use `errhance --filepaths=path/to/saved.go` for an editor hook. A repository-wide
scan is a batch operation, not the on-save path.

## Measurements

Kubernetes v1.32.2, Linux/amd64, Go 1.26.7. Sequential fresh CLI processes, warm
Go caches, `GOMAXPROCS=4`, `GOFLAGS=-p=2`, `GOMEMLIMIT=2GiB`. Before/after used the
same checkout and environment. Single-file measurements use `--dry-run`, so they
include process startup and validation but not replacement/fsync time.

| Operation | Before | After | Repetitions |
| --- | ---: | ---: | ---: |
| Already-wrapped `defaultfs.go`, no edits | 145 ms | **2.2 ms** | 5 each, median |
| `defaultfs.go`, 3 proposed wraps | 613 ms | **158 ms** | 5 each, median |
| `kubelet.go`, 29 proposed wraps | 2.49 s | **835 ms** | 3 each, median |
| Full Kubernetes `--diff`, 7,901 proposed wraps | 40.85 s | **30.50 s** | 1 each |

Both full previews report **1,761 files and 7,901 wraps**. The optimizations do
not obtain speed by suppressing those edits.
The optimized patch passed application checks on the pristine checkout and reverse
application checks on the previously rewritten checkout.

Peak RSS was roughly 31 MiB for the small changed-file run, versus 270-279 MiB
before. The large changed-file run used 286-301 MiB, versus about 1.45 GiB before.
Full-root RSS remains high: about 3.63 GiB after versus 4.39 GiB before.

Earlier restoration trials reported 185 seconds for the full run. Cache state and
resource conditions differed; **do not attribute 185-to-30 seconds entirely to
these code changes**. The matched warm-cache comparison is 40.85-to-30.50 seconds.
Cold dependency compilation can still dominate first use, and large changed files
are not instant. These measurements are not a universal latency guarantee.

## Changes

- Explicit file selection prunes directories that cannot contain a target.
- A scope-aware syntactic overapproximation detects single-file no-ops without
  calling `go env`, `go list`, or a type checker. It must not miss a possible typed
  rewrite; property tests and fuzz checks enforce that relationship.
- Actual edits retain package-aware binding/type analysis and test variants.
- Changed packages are validated in memory rather than loaded again. Validation
  does not collect expression/use/definition maps it never consumes.
- An AST invariant verifies that declarations, aliases, constants, inferred
  initializer types, variadic calls, imports, and effective build constraints are
  preserved. Function bodies may change. This permits reuse of original dependency
  types without rechecking unchanged consumers against rebuilt exports.
- Unsupported cgo/generated mappings or missing validation metadata retain the
  previous loader-based validation fallback. Invariant/type failures do not bypass
  checking or permit writes.
- Initial expression/use maps are released before output validation. Irrelevant
  scalar contract nodes and repeated import-name scans are no longer retained or
  recomputed. Parsing avoids unused legacy object resolution except in the cheap
  candidate scan, which uses it for lexical identity.

Profiles identified allocation/GC pressure as the dominant cost. Initial traces
showed roughly 16 GiB of cumulative allocations across the two-load pipeline;
the first in-memory-validation revision reduced that to roughly 10.6 GiB. These
are cumulative allocations, not peak RAM. Later allocation reductions were not
independently quantified in that profile comparison.

## Boundaries

The syntax-only path proves only that this transformer has no edit to make. It
does not certify imports, build configuration, package validity, or downstream
contracts. Syntax errors are still reported. If an edit may be possible, normal
typed checks apply. Explicit cgo targets retain their unsupported-mapping checks.

An unchanged file may still contain ambiguous candidates and require a package
load. `--overwrite` also makes more return calls potential candidates. The tool
has no persistent daemon/cache of mutable type information, so it cannot promise
LSP-style incremental latency for every change in a large package.

Full-project checking remains substantially heavier than the original syntax-only
tool. No safety check was disabled to hide that remaining cost, and no claim of
whole-program behavioral equivalence is made.

## Local Evidence

All artifacts are under ignored `.trials/`:

- `logs/bench-{before,after}-{small,large,noop,project}.json`: per-run timings,
  peak RSS, success status, and summary.
- `logs/bench-*-*.log`: CLI output for each measurement.
- `logs/{onsave-before,project-before,cli-before}.cpu`: profiles before changes.
- `logs/project-first-fast.{cpu,allocs}`: first optimized validation profile.
- `bench/main.go`, `profile/main.go`, `profile-cli/main.go`: local measurement tools.

Run the maintained microbenchmark with:

```sh
go test ./internal/errhance -run '^$' -bench BenchmarkOnSaveNoop -benchmem
```

That benchmark measures the in-process no-op path, not CLI startup or a full
package rewrite. It removes `go` from `PATH` to guard against accidentally adding
a package-driver call back to the no-op path.

## Verification

- 633 tests passed under the race detector; vet and whitespace checks passed.
- The full suite passed on Go 1.22.5.
- A 20-second structured fuzz run completed 16,190 executions. Its properties
  include checking that the no-op filter never misses an actual typed rewrite.
- The small in-process no-op microbenchmark measured about 28 microseconds and
  10.4 KiB allocated per operation on this host. This is separate from the CLI
  and Kubernetes file measurements above.
- Native macOS/Windows execution remains dependent on the CI matrix; it was not
  performed on this Linux host.
