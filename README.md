# lintkit

Static analysis utilities that output SARIF.

## Overview

A collection of lightweight linters and checks for artifacts that don't fit traditional AST-based analysis. All tools emit [SARIF](https://sarifweb.azurewebsites.net/) for interoperability with editors, CI systems, and visualization tools.

## Tools

### dbschema

Compare a SQLite database's schema against an expected DDL file and emit SARIF findings for missing/extra tables or columns.

Usage:

```
lintkit dbschema --expected schema.sql path/to/app.sqlite
```

## License

MIT
