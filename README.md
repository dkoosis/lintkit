# lintkit

Static analysis utilities that output SARIF.

## Overview

A collection of lightweight linters and checks for artifacts that don't fit traditional AST-based analysis. All tools emit [SARIF](https://sarifweb.azurewebsites.net/) for interoperability with editors, CI systems, and visualization tools.

## Tools

### nuglint

Validate knowledge nuggets stored in `.orca/kg/*.jsonl` files.

```
go run ./cmd/lintkit nuglint path/to/repo
```

Outputs SARIF findings to stdout.

## License

MIT
