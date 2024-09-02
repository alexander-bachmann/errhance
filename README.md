# errhance

### Before
```go
func foo(x int) (int, error) {
  y, err := bar(x)
  if err != nil {
    return 0, err
  }
  z, err := b.Baz(y)
  if err != nil {
    return 0, err
  }
  return z, nil
}
```

### After
```go
func foo(x int) (int, error) {
  y, err := bar(x)
  if err != nil {
    return 0, fmt.Errorf("bar: %w", err)
  }
  z, err := b.Baz(y)
  if err != nil {
    return 0, fmt.Errorf("b.Baz: %w", err)
  }
  return z, nil
}
```

### CLI Usage
```
go install github.com/alexander-bachmann/errhance@latest
cd <your_go_repo>
errhance
```

### /pkg Usage
```go
src, err := errhance.Do(errhance.Config{}, src)
if err != nil {
    return fmt.Errorf("errhance.Do: %w", err)
}
```
