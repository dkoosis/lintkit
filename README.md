# lintkit

Static analysis utilities that output SARIF.

## Overview

A collection of lightweight linters and checks for artifacts that don't fit traditional AST-based analysis. All tools emit [SARIF](https://sarifweb.azurewebsites.net/) for interoperability with editors, CI systems, and visualization tools.

## Tools

### nobackups

Detects backup or temporary files that should not be committed to a repository.

```
lintkit nobackups [PATH...]
```

When no paths are provided, the current directory is scanned. Findings are emitted in SARIF format with the `ruleId` set to `nobackups`.

## License

MIT
