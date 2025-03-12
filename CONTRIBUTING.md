- for adding new CLI commands and flags, use https://github.com/spf13/cobra-cli

- makes life easier for testing against other Go projects you may have locally
```
go build -o eh main.go && cp -f eh ~/.local/bin && rm eh
```
