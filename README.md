# errhance

## Install and Run
```
go install github.com/alexander-bachmann/errhance@latest
errhance
```

## What Happens?
- `errhance` recursively traverses your Go project, enhances returned errors, and writes the changes
- By default, generated files remain unmodified
  - generated files can be enhanced with `errhance --generated`
- By default, `return err` statements are enhanced, `return fmt.Errorf` statements are not
  - `return fmt.Errorf` statements can be enhanced with `errhance --overwrite`
- specific `if err != nil` blocks are skipped if a comment is detected with any of the following: `noeh`, `no eh`, `noerrhance`, or `no errhance`
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

## Example
### Before
```go
func Foo() (int, error) {
  body, err := Get("https://example.com")
  if err != nil {
    return 0, err
  }
  num, err := strconv.Atoi(body)
  if err != nil {
    return 0, err
  }
  return num, nil
}

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

### After
```go
func Foo() (int, error) {
  body, err := Get("https://example.com")
  if err != nil {
    return 0, fmt.Errorf("Get: %w", err)
  }
  num, err := strconv.Atoi(body)
  if err != nil {
    return 0, fmt.Errorf("strconv.Atoi: %w", err)
  }
  return num, nil
}

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

## Usage
```
errhance --help
```

## Dependencies
- `errhance`depends on [goimports](https://pkg.go.dev/golang.org/x/tools/cmd/goimports) and will automatically be downloaded if missing.
  - `errhance` requires `goimports` to import the `fmt` package when wrapping errors with `fmt.Errorf`

