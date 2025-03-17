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

### Example
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

## Behavior
- Only `return err` statements are enhanced.
  - Use the flags `--overwrite` or `--force-overwrite` to also enhance `return fmt.Errorf` statements.
    - `--overwrite` will enhance both `return err` and `return fmt.Errorf` statements that have been deemed **safe** to enhance.
    - `--force-overwrite` will enhance `return err` , **safe** `return fmt.Errorf`, and **unsafe** `return fmt.Errorf` statements.
    - Examples:
      - Safe: can be enhanced with `errhance --overwrite`
        ```go
        return fmt.Errorf("failed: %w", err)
        ```
      - Unsafe: can be enhanced with `errhance --force-overwrite`
        ```go
        return fmt.Errorf("failed: %w", ErrNotFound)    // sentinel error
        return fmt.Errorf("failed: %w: %w", errA, errB) // multiple format arguments
        ```
        - Returning a sentinel error is deemed unsafe for enhancement because it is probable that code further up the call stack depends on that error using `errors.Is()` or `errors.As()`.
        - Returning an error with multiple format arguments is deemed unsafe for enhancement because it is probable that the the additional formatted argument(s) are necessary.

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
- `errhance` depends on [goimports](https://pkg.go.dev/golang.org/x/tools/cmd/goimports) and will automatically be downloaded if missing.
  - `errhance` depends on `goimports` to import the `fmt` package if introduced in a file. `fmt` is used to wrap errors with `fmt.Errorf`.
