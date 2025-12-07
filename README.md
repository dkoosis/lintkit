# lintkit

Static analysis utilities that output SARIF.

## Overview

A collection of lightweight linters and checks for artifacts that don't fit traditional AST-based analysis. All tools emit [SARIF](https://sarifweb.azurewebsites.net/) for interoperability with editors, CI systems, and visualization tools.

## Tools

### stale

Detect derived artifacts that are older than their sources based on a YAML configuration file. Emits SARIF with `ruleId` of `stale-artifact` and a driver name of `lintkit-stale`.

Example rules file:

```
rules:
  - derived: "go.sum"
    source: "go.mod"
  - derived: "*_gen.go"
    source: "*.proto"
  - derived: ".orca/views/*.jsonl"
    source: ".orca/knowledge.db"
```

Run against one or more paths (defaults to the current directory):

```
lintkit stale --rules staleness.yml ./...
```

## License

MIT
