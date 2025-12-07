# lintkit

Static analysis utilities that output SARIF.

## Overview

A collection of lightweight linters and checks for artifacts that don't fit traditional AST-based analysis. All tools emit [SARIF](https://sarifweb.azurewebsites.net/) for interoperability with editors, CI systems, and visualization tools.

## Tools

- **mdsanity**: Checks markdown hygiene across a repository, reporting orphaned files, misplaced root-level docs, and ephemeral content that should live in dedicated subtrees. Emits SARIF for easy consumption by CI and IDE integrations.

- **docsprawl**: Analyze markdown sprawl and emit SARIF for documentation hygiene issues.

- **dbsanity**: Compare SQLite table row counts against a JSON baseline and emit SARIF when drift exceeds a threshold.

```bash
lintkit dbsanity --baseline counts.json --threshold 20 path/to/db.sqlite
```

Baseline format:

```json
{
  "tables": {
    "nugs": 1000,
    "tags": 2000
  }
}
```

Tables present in the database but missing from the baseline are ignored. Tables missing from the database are treated as a 100% drop.

## License

MIT
