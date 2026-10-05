# Validation and Public-Project Trials

The Cobra, go-git, and Prometheus results below describe the earlier conservative
policy and remain as a record of the regression. The restored policy and current
Kubernetes measurements are in [Kubernetes restoration](kubernetes-restoration.md).

These are bounded experiments, not a claim of compatibility with all Go projects.
The local host was Linux/amd64, Go 1.26.7. No public-repository commits or resets
were made. Clones, patches, test logs, and explicit scratch artifacts remain in
the gitignored `.trials/` directory.

## Automated Checks

`.github/workflows/ci.yml` defines:

- Tests on Linux, macOS, and Windows with Go 1.22.5 and the current stable Go.
- Linux race tests and vet.
- Three bounded Linux fuzz smoke tests, 20 seconds each with two workers.

Local verification passed: 261 tests under the race detector on Go 1.26.7,
the full suite on Go 1.22.5, vet, and `git diff --check`.

Native macOS and Windows execution has not been performed locally. The workflow
must run on GitHub before those platforms can be considered verified. Test
fixtures now resolve macOS temporary-directory symlinks and compare filesystem
permissions against their actual originals rather than assuming Unix modes.

Fuzz properties cover arbitrary and structured Go input, output type correctness,
idempotence, positive rewrite counts for supported fixtures, unchanged source reports on errors, and unified-diff reconstruction,
including empty files and missing final newlines. Integration tests separately
cover unchanged disk files after preflight failures. These properties do not
prove runtime equivalence of arbitrary Go programs.

Final local 20-second fuzz sessions passed:

| Target | Executions |
| --- | ---: |
| `FuzzSource` | 219,467 |
| `FuzzSourceStructured` | 16,740 |
| `FuzzWriteDiff` | 496,066 |

Logs are `.trials/logs/fuzz-{source,structured,diff}.log`. These runs used a
previously warmed fuzz corpus; counts are observations, not benchmark results.

## Pinned Projects

| Project | Tag | Commit |
| --- | --- | --- |
| Cobra | v1.9.1 | `40b5bc1437a564fc795d388b23835e84f54cd1d1` |
| go-git | v5.13.1 | `b6bc0c0bf2d0ab9ec5e943222de091bd8af37faf` |
| Prometheus | v3.2.1 | `804c49d58f3f3784c77c9c8ec17c9062092cae27` |

Dependencies were explicitly downloaded before offline errhance analysis. The
Prometheus example/tool modules also required their own dependency downloads.
No module files were changed. Tests were run on the disposable checkouts, not
against the main repository's source.

## Historical Root-Wide Results

After dependency setup and the discovery fix, all three root-wide previews
completed successfully but proposed **zero changes**. Unresolved identity-sensitive
flows conservatively block the loaded set. This is a significant usefulness
limitation, not evidence that these projects contain no enhanceable errors.

| Project | Wall time | Diagnostics | Distinct reasons | Stderr lines with grouping |
| --- | ---: | ---: | ---: | ---: |
| Cobra | 3.50 s | 166 | 6 | 8 |
| go-git | 7.44 s | 2,963 | 9 | 11 |
| Prometheus | 34.51 s | 4,186 | 9 | 11 |

These final runs used warm caches and followed the focused rewrites below. Timing
is not a controlled comparison. Prometheus peaked around 997 MB RSS on the final
root preview. Earlier cold runs were substantially slower.

The go-git trial initially failed because `_examples/ls` contains an outdated
commitgraph API call. Go's `./...` ignores underscore directories; errhance did
not. The CLI now skips underscore-prefixed descendants, with regression tests.
Running from an underscore-prefixed directory still explicitly selects that root.

Thousands of repeated diagnostics also obscured the useful signal. Default
reporting now groups identical reasons and gives the first location; `--verbose`
retains all individual locations. Counts describe diagnostics, not skipped returns.

## Focused Before/After Results

Focused analysis has a smaller consumer scope than root analysis. It does not
establish compatibility with consumers outside that scope. No checks were
disabled to obtain these rewrites.

| Selection | Applied files / wraps | Tests before | Tests after |
| --- | ---: | --- | --- |
| Cobra `doc` | 4 / 14 | Both root packages passed | Both passed |
| go-git `config` | 2 / 11 | 48 packages passed, 4 failed, 7 had no tests | Same outcomes and five existing failures |
| Prometheus `util/fmtutil`, `util/documentcli` | 2 / 7 | Three selected packages passed, one had no tests | Two passed, one failed, one had no tests |

Every applied diff hunk was inspected. Preview patches matched actual rewrites,
all affected clones passed `git diff --check`, and subsequent focused previews
were empty. Test expectations were not changed to conceal failures.

Cobra and go-git used the exact same root command before and after:

```sh
go test ./... -count=1
```

go-git's existing failures were `TestPlainOpenNotExistsDetectDotGit`,
`TestPushWithProgress`, and three transport variants of
`TestSendPackOnNonEmptyWithReportStatusWithError`. The last three expect different
Git subprocess error text. None was introduced by the focused rewrite.

Prometheus used this exact focused baseline/post command:

```sh
go test -timeout=90s -count=1 ./util/fmtutil ./util/documentcli ./cmd/promtool ./cmd/prometheus
```

Two `fmtutil` assertions newly failed:

- `TestMetricTextToWriteRequestErrorParsingFloatValue`
- `TestMetricTextToWriteRequestErrorParsingMetricType`

Both compare exact `err.Error()` strings. Wrapping intentionally added
`parser.TextToMetricFamilies: `, so the assertions no longer match. Both CLI
consumer suites still passed. This is a real error-text compatibility failure,
not a type-check failure or malformed error chain. The current analysis does not
detect all error-text consumers. Review such failures individually; do not blindly
update expectations or assume every string comparison is expendable.

The attempted full Prometheus baseline timed out after 240 seconds, with 63
completed package-result lines and no failures in those completed results. It is
partial coverage, not a full-suite pass. The full suite was not rerun after edits.

Detailed local records: `.trials/logs/RESULTS.md`,
`.trials/logs/PROMETHEUS.md`, `*-baseline.*`, `*-post.*`, `*-root-final.*`,
and `*-applied.patch`. The modified disposable clones are intentionally retained
for inspection.

## Reproducing

Use fresh disposable directories rather than rerunning a baseline on an already
rewritten clone. From this repository, the initial setup is:

```sh
mkdir -p .trials/projects .trials/bin .trials/logs .trials/tmp
export TMPDIR="$PWD/.trials/tmp"
export GOPATH="$TMPDIR/go"
export GOCACHE="$TMPDIR/go-build-cache"
export GOMAXPROCS=4 GOFLAGS='-p=2 -mod=readonly' GOWORK=off
go build -o .trials/bin/errhance .
git clone --depth 1 --branch v1.9.1 https://github.com/spf13/cobra.git .trials/projects/cobra
git clone --depth 1 --branch v5.13.1 https://github.com/go-git/go-git.git .trials/projects/go-git
git clone --depth 1 --branch v3.2.1 https://github.com/prometheus/prometheus.git .trials/projects/prometheus
```

In each clone, download dependencies with `go mod download`, record the commit
and clean state, run the baseline, capture `errhance --diff`, inspect it, then
apply using normal errhance and rerun the identical tests. Download nested-module
dependencies separately. Keep timeouts and failing baselines in the record.
Repeat the preview to check idempotence. The tool itself never downloads missing
dependencies. Do not treat a nonzero preview exit as a successful no-op.

These trials motivated the restoration: unknown consumer compatibility now warns
rather than blocking unrelated functions, while known traced conflicts still
receive targeted protection. See the linked restoration report for its tests,
coverage, and remaining limits.
