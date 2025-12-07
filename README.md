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

- **wikifmt**: Recursively scans wiki-style Markdown files for frontmatter validity, broken wikilinks/Markdown links, and basic tag hygiene. Results are emitted as SARIF for easy consumption by editors or CI systems.

```bash
lintkit wikifmt ROOT...
```

- **stale**: Detect derived artifacts that are older than their sources based on a YAML configuration file. Emits SARIF with `ruleId` of `stale-artifact` and a driver name of `lintkit-stale`.

Example rules file:

```yaml
rules:
  - derived: "go.sum"
    source: "go.mod"
  - derived: "*_gen.go"
    source: "*.proto"
  - derived: ".orca/views/*.jsonl"
    source: ".orca/knowledge.db"
```

Run against one or more paths (defaults to the current directory):

```bash
lintkit stale --rules staleness.yml ./...
```

- **nuglint**: Validate knowledge nuggets stored in `.orca/kg/*.jsonl` files. Outputs SARIF findings to stdout.

```bash
lintkit nuglint path/to/repo
```

- **filesize**: Reports file metrics and enforces simple size budgets. Outputs SARIF only.

```bash
lintkit filesize --rules .filesize.yml [PATH...]
```

Example rules file:

```yaml
rules:
  - pattern: "*.go"
    max: 500        # lines
  - pattern: "*.json"
    max: 100KB      # bytes
  - pattern: "go.sum"
    max: 50KB       # bytes
```

Patterns use Go's filepath.Match semantics. When a file exceeds its configured limit, the linter emits a `filesize-budget` result identifying the file, actual size, and allowed maximum.

- **nobackups**: Detects backup or temporary files that should not be committed to a repository. Findings are emitted in SARIF format with the `ruleId` set to `nobackups`.

```bash
lintkit nobackups [PATH...]
```

- **jsonl**: Validate JSONL files against a JSON Schema. Emits SARIF findings.

```bash
lintkit jsonl --schema schema.json file.jsonl [file2.jsonl...]
```

## License

MIT
