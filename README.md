# errhance

## Install and Run
```
go install github.com/alexander-bachmann/errhance@latest
errhance
```

## About
```
errhance recursively traverses a Go project, enhances returned errors, and writes the changes. The errors are enhanced by wrapping them with the name of the function that initially returned the error.

-- Before --
if err := foo.Bar(); err != nil {
    return err
}
-- After --
if err := foo.Bar(); err != nil {
    return fmt.Errorf("foo.Bar: %w", err)
}

Default Behavior:
- only 'return err' statements are enhanced; to enhance 'return fmt.Errorf' statements, use the flags: --overwrite or --force-overwrite
    - The --overwrite flag will enhance both 'return err' and safe 'return fmt.Errorf' statements
    - The --force-overwrite flag will enhance 'return err' , safe 'return fmt.Errorf', and unsafe 'fmt.Errorf' statements
    - Safe fmt.Errorf example:
        - return fmt.Errorf("failed: %w", err)
    - Unsafe fmt.Errorf examples:
        - return fmt.Errorf("failed: %w", ErrNotFound)    // sentinel error
        - return fmt.Errorf("failed: %w: %w", errA, errB) // multiple identifiers
        - return fmt.Errorf("%s: %w", str, err)           // multiple identifiers

- Generated code is not enhanced; to enhance generated code, use the flag: --generated

- Specific 'if err != nil' blocks are skipped if a comment is detected with any of the following: noeh, no eh, noerrhance, or no errhance
// ...
if err != nil {
    return err //noerrhance
}
// ...
if err != nil {
    return fmt.Errorf("custom message: %w", err) // noeh
}

Usage:
  errhance [flags]

Flags:
  -f, --force-overwrite   returns enhanced: err, safe fmt.Errorf, and unsafe fmt.Errorf
  -g, --generated         enhances returns in generated code
  -h, --help              help for errhance
  -o, --overwrite         returns enhanced: err and safe fmt.Errorf
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

## Dependencies
- `errhance`depends on [goimports](https://pkg.go.dev/golang.org/x/tools/cmd/goimports) and will automatically be downloaded if missing.
  - `errhance` requires `goimports` to import the `fmt` package when wrapping errors with `fmt.Errorf`
