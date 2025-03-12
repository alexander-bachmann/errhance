# errhance

## Install and Run
```
go install github.com/alexander-bachmann/errhance@latest
errhance
```

## Usage
```
errhance --help
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

## Default Behavior
- generated files are not modified
- only instances of `return err` are modified
- `if err != nil` blocks can be skipped by adding a comment with any of the following options: `noeh`, `no eh`, `noerrhance`, `no errhance`
  - e.g.
```
if err != nil {
  return fmt.Errorf("some custom error message: %w", err) //noeh
}
```

## Dependencies
- `errhance`depends on [goimports](https://pkg.go.dev/golang.org/x/tools/cmd/goimports) and will automatically be downloaded if missing.
  - `errhance` needs `goimports` for importing the `fmt` package when wrapping errors with `fmt.Errorf`.

