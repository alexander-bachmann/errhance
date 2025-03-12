# TODO
- run goimports per file
    - install goimports if missing
- output given single line (for neovim integration)
  - errhance "client, err := NewClient()"
    - can use `generateWrappedErr()` but need to still check the number of args in the surrounding function, then how will it know the zero values for the other returned values .. func Foo() (int, error) , may be more complex
    - returns: if err != nil { return fmt.Errorf("NewClient: %w", err) }
  - will need to think about how to get the current functions return type to know how what to return
- revert option for replace all `return fmt.Errorfs` that seem to have been added by errhance with `return err`
- readme
  - why? wrapping errors with additional context is annoying
  - well-written function names provides better errors

# Completed
- cli wrapper
- more efficient file traversal
- //noeh or //noerrhance comment for not applying change
  - if two forward slashes exist in line and noeh or noerrhance exists
    - want to check separately to support //nosec noeh
- force option for wiping old `return fmt.Errorfs`
- ignore generated code (read first 5 lines for generated keyword comments)
