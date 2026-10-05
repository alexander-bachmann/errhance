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
diagnostics go to stderr. No changes means an empty diff and success. Analysis
or validation failure returns a nonzero exit status without emitting a diff.
Preview validates source changes, but does not test replacement permissions or
access-metadata preservation. A later write run can still fail those checks.

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
- Hidden directories and `testdata` are skipped. Explicit `--filepaths` selection still respects traversal filters and the current-directory root. Positional filenames are rejected; use `--filepaths=a.go,b.go`.

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

`errhance` now requires type-correct packages, including their tests. It loads the active build configuration with `go/packages`, resolves identifier bindings and error types, and validates all proposed edits together before writing any file. The Go toolchain and project dependencies must already be installed. No dependencies or toolchains are automatically downloaded, and module files are not updated.

- Analysis follows error origins by variable identity and conservatively merges control-flow paths. Closure captures do not inherit the surrounding traversal's last assignment. Captured or address-exposed errors, ambiguous origins, unsupported control flow, implicit/tuple returns, and incompatible concrete return types remain unchanged, with diagnostics on stderr.
- The current analysis does not rewrite returns inside loops, switch/select statements, or functions using `goto`. It supports local analysis inside closures, but not arbitrary callback execution timing. A skipped return is not evidence that its error is unimportant.
- Direct error equality, type assertions, and type switches can change behavior after wrapping. Analysis warns about these consumers and protects identified producers, including forwarding functions and aliases. An unresolved identity-sensitive flow can conservatively prevent all rewrites in the loaded set.
- Reader methods (`Read`, `ReadByte`, `ReadRune`) are treated conservatively because their contracts can require returning `io.EOF` itself. `errors.Is(err, io.EOF)` works through wrapping, but wrapping inside an `io.Reader` implementation can still violate callers' valid expectation of direct EOF equality.
- External consumers, reflection, arbitrary dynamic behavior, and other build configurations are not proven safe by this analysis. Review changes and run your project's tests before committing. Use `// noeh` wherever an error must be returned unchanged.
- Only the active GOOS/GOARCH/build tags are checked. Excluded files are reported, not rewritten. Active cgo source without a direct typed source mapping is also reported and left unchanged. Explicitly selecting unsupported or build-excluded files returns an error.
- Imports and formatting use the bundled `goimports` library, not an installed executable. New import aliases avoid local and test-package name collisions.
- All proposed output is prepared and type-checked before writing. Each changed file is replaced through a synced temporary file and rename; unchanged files are not touched. Originals are checked for intervening edits before replacement.
- Replacement is atomic per file, not across the project. A later write failure or cancellation can leave earlier files updated; the summary reports completed writes. This does not provide a lock against concurrent editors or hostile filesystem changes.
- Symlinks and special files are skipped during traversal; directly targeted unsafe files and hard links are rejected. Permissions and supported access metadata are preserved; metadata that cannot safely be preserved causes an error rather than silent loss. Linux, macOS, Windows, FreeBSD, NetBSD, and OpenBSD have platform-specific handling. Windows privileged audit SACLs are not preserved; do not use this tool on files requiring audit-policy preservation. Other platforms reject disk rewriting.

For internal callers, `Source` now requires a standalone type-correct file, not a syntactic snippet. Use `LocalFile` or `LocalDirectoryContext` for module-aware checking. Reports include diagnostics; on errors, a source report describes proposed output rather than guaranteeing it was written.

## Compatibility

Existing command flags remain available, but unsafe or ambiguous cases that older versions rewrote are now skipped. `--overwrite` preserves existing message text instead of replacing it. The previously inactive YAML/environment configuration loader has been removed; command flags remain the configuration interface. No version bump is included in this change.

## License
- This project is released under the [Unlicense](https://unlicense.org).

## Miscellaneous
- `errhance` can be called from within Neovim by adding the following keymap to your config:
  ```lua
  vim.keymap.set('n', '<leader>geh',
    ':w<cr>:!errhance --overwrite --filepaths=%:p:.<cr>:let view = winsaveview()<cr>:e!<cr>:call winrestview(view)<cr>',
    { silent = true })
  ```
