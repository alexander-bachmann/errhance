# errhance

### Before
```go
func foo(x int) (int, error) {
  y, err := bar(x)
  if err != nil {
    return 0, err
  }
  z, err := baz(y)
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
  z, err := baz(y)
  if err != nil {
    return 0, fmt.Errorf("baz: %w", err)
  }
  return z, nil
}
```

### Usage
```go
src, err := errhance.Do(errhance.Config{}, src)
if err != nil {
    return fmt.Errorf("errhance.Do: %w", err)
}
```
