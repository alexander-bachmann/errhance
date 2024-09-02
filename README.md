# errhance

### Before
```go
package main
  import (
    "foo"
    "fee/fi/fo"
  )
  func A() error {
    b := foo.Bar{}
    err := b.Baz()
    if err != nil {
      return err
    }
    err = fo.Fum()
    if err != nil {
      return err
    }
  }
```

### After
```go
package main
  import (
    "foo"
    "fee/fi/fo"
  )
  func A() error {
    b := foo.Bar{}
    err := b.Baz()
    if err != nil {
      return fmt.Errorf("Baz: %w", err)
    }
    err = fo.Fum()
    if err != nil {
      return fmt.Errorf("fo.Fum: %w", err)
    }
  }
```

### Usage
#### CLI
```bash
go install github.com/alexander-bachmann/errhance@latest
cd <your_go_repo>
errhance
```
#### /pkg
```go
src, err := errhance.Do(errhance.Config{}, src)
if err != nil {
    return fmt.Errorf("errhance.Do: %w", err)
}
```
