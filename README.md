# errhance

### Before
```go
package main
import (
  "foo"
  "fee/fi/fo"
  "fiddly"
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
  err = meep()
  if err != nil {
    return err
  }
  err = fiddly.Widdly().Weddly().Woddly()
  if err != nil {
    return err
  }
  err = a().b().c().d()
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
  "fiddly"
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
  err = meep()
  if err != nil {
    return fmt.Errorf("meep: %w", err)
  }
  err = fiddly.Widdly().Weddly().Woddly()
  if err != nil {
    return fmt.Errorf("fiddly.Widdly.Weddly.Woddly: %w", err)
  }
  err = a().b().c().d()
  if err != nil {
    return fmt.Errorf("a.b.c.d: %w", err)
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
