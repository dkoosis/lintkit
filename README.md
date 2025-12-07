# lintkit

Static analysis utilities that output SARIF.

## Overview

A collection of lightweight linters and checks for artifacts that don't fit traditional AST-based analysis. All tools emit [SARIF](https://sarifweb.azurewebsites.net/) for interoperability with editors, CI systems, and visualization tools.

## Tools

### jsonl

Validate JSON Lines (JSONL) files against a JSON Schema and emit SARIF results. Each failing line is reported with its line number.

```
lintkit jsonl --schema path/to/schema.json path/to/data.jsonl [more.jsonl]
```

Exits with a non-zero status when validation errors are present.

## License

MIT
