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
- By default, only `return err` statements are enhanced.
  - Use the flag `--overwrite` to also enhance `return fmt.Errorf()` statements.

- Generated code files are skipped.
  - Use the flag `--generated` to enhance generated files.

- Files in the `vendor` directory are skipped.
  - Use the flag `--vendor` to enhance files in the `vendor` directory.

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

## Dependencies
- `errhance` depends on [goimports](https://pkg.go.dev/golang.org/x/tools/cmd/goimports) and will automatically be downloaded if missing.
  - `errhance` depends on `goimports` to import the `fmt` package if introduced in a file. `fmt` is used to wrap errors with `fmt.Errorf`.

## License
- This project is released under the [Unlicense](https://unlicense.org).
- One file, `internal/fmtstr/parse.go`, is copied from an internal package in the Go `x/tools` project and is licensed under a [BSD-style license](internal/fmtstr/LICENSE).

## Miscellaneous
- `errhance` can be called from within Neovim by adding the following keymap to your config:
  ```lua
  vim.keymap.set('n', '<leader>geh',
    ':w<cr>:!errhance --overwrite --filepaths=%:p:.<cr>:let view = winsaveview()<cr>:e!<cr>:call winrestview(view)<cr>',
    { silent = true })
  ```
