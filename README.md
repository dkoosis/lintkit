# lintkit

Static analysis utilities that output SARIF.

## Overview

A collection of lightweight linters and checks for artifacts that don't fit traditional AST-based analysis. All tools emit [SARIF](https://sarifweb.azurewebsites.net/) for interoperability with editors, CI systems, and visualization tools.

## Tools

### filesize

Reports file metrics and enforces simple size budgets. Outputs SARIF only.

**Usage**

```
go run ./cmd/lintkit filesize --rules .filesize.yml [PATH...]
```

If no `PATH` arguments are provided, the current directory is analyzed.

**Rules file**

```
rules:
  - pattern: "*.go"
    max: 500        # lines
  - pattern: "*.json"
    max: 100KB      # bytes
  - pattern: "go.sum"
    max: 50KB       # bytes
```

Patterns use Go's filepath.Match semantics. When a file exceeds its configured
limit, the linter emits a `filesize-budget` result identifying the file,
actual size, and allowed maximum.

## License

MIT
