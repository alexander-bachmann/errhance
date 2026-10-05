# errhance

## Install
```
go install github.com/alexander-bachmann/errhance@latest
```

## What Happens?
- `errhance` recursively traverses a Go project, enhances returned errors, and writes the changes. The errors are enhanced by wrapping them with the name of the function that initially returned the error.
  ```
  // Before
  if err := foo.Bar(); err != nil {
      return err
  }
  // After
  if err := foo.Bar(); err != nil {
      return fmt.Errorf("foo.Bar: %w", err)
  }
  ```

## Preview Changes

Run from the project directory:

```bash
errhance --dry-run
# 2 files would be modified, 3 errors would be enhanced

errhance --diff > changes.patch
errhance --diff --filepaths=foo.go,bar.go
```

`--dry-run` analyzes and validates proposed changes without writing source files.
`--diff` implies dry-run, even with `--dry-run=false`. It writes only a unified
diff to stdout, with project-relative `a/` and `b/` paths; summaries and
diagnostics go to stderr. No changes means an empty diff and success.
Fatal analysis or validation failure returns a nonzero exit status without
emitting a diff. Unloadable packages are reported and skipped while independently
valid packages can still produce a diff; see the analysis policy below.
Preview validates source changes, but does not test replacement permissions or
access-metadata preservation. A later write run can still fail those checks.

## On Save

Target the saved file rather than running over the entire repository:

```bash
errhance --filepaths=path/to/saved.go
```

Explicit file selection prunes unrelated directories. If syntax proves there is
nothing eligible to rewrite, the command returns without running the Go package
loader. Such a no-op is not a package/type/contract audit and does not produce
those diagnostics. Possible edits still require package-aware type checking,
including test variants, followed by output validation before writing.

The no-op check is conservative: some unchanged files, especially with
`--overwrite`, still need typed analysis. Cold Go caches and large packages cost
more. Use the normal repository-wide command for broader consumer diagnostics.
See [performance measurements](docs/performance.md) for measured latency and limits.

## Examples:
### Kubernetes
```bash
errhance
1970 files modified, 9507 errors enhanced
```
- https://github.com/alexander-bachmann/kubernetes/commit/583ae82f83e13a8aa0fba8e844bee0398c46ec97
---
```bash
errhance --overwrite
2205 files modified, 12544 errors enhanced
```
- https://github.com/alexander-bachmann/kubernetes/commit/99fdf386d93e7518fb2e6776df3158f24e795683


## Behavior
- By default, supported returns of non-nil `error` values are enhanced. Variables do not have to be named `err`.
- Use `--overwrite` to also prefix supported `fmt.Errorf` calls with their error origin. Existing message text, directives, argument evaluation, and `%w` wrapping are preserved. Dynamic formats and variadic calls are skipped; `%v` is no longer guessed to be `%w` from variable names.

- Generated code files are skipped.
  - Use the flag `--generated` to enhance generated files.

- Files in the `vendor` directory are skipped.
  - Use the flag `--vendor` to enhance files in the `vendor` directory.
- Hidden directories, underscore-prefixed directories (such as `_examples`), and `testdata` are skipped, matching Go's recursive package discovery conventions. To process an underscore-prefixed directory deliberately, run from that directory. Explicit `--filepaths` selection still respects traversal filters and the current-directory root. Positional filenames are rejected; use `--filepaths=a.go,b.go`.

- `if err != nil` blocks can be specificed to be skipped if a comment is detected with any of the following: `noeh`, `no eh`, `noerrhance`, or `no errhance`
  ```go
  // ...
  if err != nil {
      return err //noerrhance
  }
  // ...
  if err != nil {
      return fmt.Errorf("custom message: %w", err) // noeh
  }
  ```

## Safety and Analysis

`errhance` requires type-correct packages for the files it rewrites. It loads the active build configuration with `go/packages`, resolves identifier bindings and error types, and validates proposed edits before writing any file. The Go toolchain and project dependencies must already be installed. No dependencies or toolchains are automatically downloaded, and module files are not updated.

- Analysis follows error origins by variable identity and conservatively merges control-flow paths. Closure captures do not inherit the surrounding traversal's last assignment. Captured errors that may be mutated, address-exposed errors, ambiguous origins, implicit/tuple returns, and incompatible concrete return types remain unchanged, with diagnostics on stderr. Ordinary read-only captures do not disable surrounding local rewrites.
- Local error handling inside loops, switches, selects, labels, and closures is supported. Loop-carried writes and uncertain branch exits invalidate old origin facts; fresh assignments and nil guards remain eligible. Functions using `goto` remain unsupported. A skipped return is not evidence that its error is unimportant.
- Direct error equality, type assertions, and type switches can change behavior after wrapping. Known conflicts protect traced producers, including forwarding functions, aliases, and individual tuple results. **Unresolved consumer compatibility produces warnings, not a project-wide veto.** Arbitrary interface assertions are not treated as error contracts without error provenance. This is useful local transformation with explicit uncertainty, not proof that all downstream behavior is preserved.
- Methods with the standard `Read`, `ReadByte`, or `ReadRune` signatures are treated conservatively because their contracts can require returning `io.EOF` itself. Unrelated methods sharing those names are not automatically skipped. `errors.Is(err, io.EOF)` works through wrapping, but wrapping inside an `io.Reader` implementation can still violate callers' valid expectation of direct EOF equality.
- Unloadable packages and their affected dependents are skipped with diagnostics; independently type-correct packages can still be processed. Broken test variants prevent rewriting their shared source files. Explicitly selecting an unavailable file, or having no loadable selected packages, returns an error before writes. Consumers inside skipped packages are outside the completed analysis.
- Active workspace members share package loading. Modules not listed in that workspace are reported and left unchanged. Validation preserves declarations, inferred types, and effective build constraints, then rechecks changed package bodies and test variants in memory using the original import graph. Unchanged dependents remain type-compatible because exported declarations cannot change. Unsupported source mappings fall back to loader-based validation; newly introduced type errors abort proposed writes.
- External consumers, reflection, arbitrary dynamic behavior, and other build configurations are not proven safe by this analysis. Review changes and run your project's tests before committing. Use `// noeh` wherever an error must be returned unchanged.
- Wrapping changes `err.Error()` text. Exact-message assertions and consumers that parse error strings can break even when type checking and `errors.Is` behavior are preserved. Public-project trials found this in Prometheus; inspect failures rather than automatically updating expectations.
- Only the active GOOS/GOARCH/build tags are checked. Excluded files are reported, not rewritten. Active cgo source without a direct typed source mapping is also reported and left unchanged. Explicitly selecting unsupported or build-excluded files returns an error.
- Imports and formatting use the bundled `goimports` library, not an installed executable. New import aliases avoid local and test-package name collisions.
- All proposed output is prepared and type-checked before writing. Each changed file is replaced through a synced temporary file and rename; unchanged files are not touched. Originals are checked for intervening edits before replacement.
- Replacement is atomic per file, not across the project. A later write failure or cancellation can leave earlier files updated; the summary reports completed writes. This does not provide a lock against concurrent editors or hostile filesystem changes.
- Symlinks and special files are skipped during traversal; directly targeted unsafe files and hard links are rejected. Permissions and supported access metadata are preserved; metadata that cannot safely be preserved causes an error rather than silent loss. Linux, macOS, Windows, FreeBSD, NetBSD, and OpenBSD have platform-specific handling. Windows privileged audit SACLs are not preserved; do not use this tool on files requiring audit-policy preservation. Other platforms reject disk rewriting.

For internal callers, `Source` now requires a standalone type-correct file, not a syntactic snippet. Use `LocalFile` or `LocalDirectoryContext` for module-aware checking. Reports include diagnostics; on errors, a source report describes proposed output rather than guaranteeing it was written.

## Diagnostics

Diagnostics always go to stderr. By default, every nonempty list is grouped by
identical message, even for small lists. Reasons are sorted by message; each shows
its count and first known location, ordered by filename, line, and column:

```text
166 diagnostics (2 reasons):
  160 x ambiguous error origin (first: example.go:12)
  6 x unsupported control flow (first: example.go:40)
```

Paths are reported as provided by analysis. File-only locations omit the line;
missing locations appear as `<unknown>`. Counts describe diagnostics, not skipped
returns. Use `--verbose` to print every diagnostic in its original order with its
full position, including column when available. With `--diff`, stdout remains
only the unified diff in either mode.

## Compatibility

Existing command flags remain available, but unsafe or ambiguous cases that older versions rewrote are now skipped. `--overwrite` preserves existing message text instead of replacing it. The previously inactive YAML/environment configuration loader has been removed; command flags remain the configuration interface. No version bump is included in this change.

## License
- This project is released under the [Unlicense](https://unlicense.org).

## Validation

CI tests Linux, macOS, and Windows on the minimum and stable Go versions, with
Linux race/vet and bounded fuzz jobs. See [validation results](docs/validation.md)
for pinned public-project trials, actual failures, analysis limits, and reproduction
steps. Trial clones and logs live under the gitignored `.trials/` directory.

## Miscellaneous
- `errhance` can be called from within Neovim by adding the following keymap to your config:
  ```lua
  vim.keymap.set('n', '<leader>geh',
    ':w<cr>:!errhance --overwrite --filepaths=%:p:.<cr>:let view = winsaveview()<cr>:e!<cr>:call winrestview(view)<cr>',
    { silent = true })
  ```
