# errhance

## Install
```
go install github.com/alexander-bachmann/errhance@latest
```

## Run
```
errhance
```

## What Happens?
- `errhance` recursively traverses a Go project, enhances returned errors, and writes the changes. The errors are enhanced by wrapping them with the name of the function that initially returned the error.

### Examples
```bash
% errhance

52 files modified, 201 errors enhanced
```

#### Before
```go
func Get(url string) (string, error) {
  resp, err := http.Get(url)
  if err != nil {
    return "", err
  }
  defer resp.Body.Close()
  body, err := io.ReadAll(resp.Body)
  if err != nil {
    return "", err
  }
  return string(body), nil
}
```
#### After
```go
func Get(url string) (string, error) {
  resp, err := http.Get(url)
  if err != nil {
    return "", fmt.Errorf("http.Get: %w", err)
  }
  defer resp.Body.Close()
  body, err := io.ReadAll(resp.Body)
  if err != nil {
    return "", fmt.Errorf("io.ReadAll: %w", err)
  }
  return string(body), nil
}
```

## Default Behavior
- Only `return err` statements are enhanced.
  - Use the flags `--overwrite` or `--force-overwrite` to also enhance `return fmt.Errorf` statements.
    - `--overwrite` will enhance both `return err` and `return fmt.Errorf` statements that have been deemed **safe** to enhance.
    - `--force-overwrite` will enhance `return err` , **safe** `return fmt.Errorf`, and **unsafe** `fmt.Errorf` statements.
    - `fmt.Errorf` Examples:
      - Safe:
      ```go
      return fmt.Errorf("failed: %w", err)
      ```
      - Unsafe:
      ```go
      return fmt.Errorf("failed: %w", ErrNotFound)    // sentinel error
      return fmt.Errorf("failed: %w: %w", errA, errB) // multiple format arguments
      return fmt.Errorf("%s: %w", str, err)           // multiple format arguments
      ```

- Generated code files are skipped.
  - Use the flag `--generated` to enhance generated files.

- Specific `if err != nil` blocks are skipped if a comment is detected with any of the following: `noeh`, `no eh`, `noerrhance`, or `no errhance`
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
- `errhance`depends on [goimports](https://pkg.go.dev/golang.org/x/tools/cmd/goimports) and will automatically be downloaded if missing.
  - `errhance` requires `goimports` to import the `fmt` package when wrapping errors with `fmt.Errorf`
