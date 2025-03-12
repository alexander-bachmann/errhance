# errhance

view [internal/errhance/errhance_test.go] to see all cases currently supported

- ignores generated files by default
-

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

### Usage
```bash
go install github.com/alexander-bachmann/errhance@latest
errhance
go install golang.org/x/tools/cmd/goimports@latest
goimports -w .
```
- note: if `errhance` introduced the need for `fmt` in a file, `goimports` will automatically import
