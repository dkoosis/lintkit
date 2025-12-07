# lintkit

Static analysis utilities that output SARIF.

## Overview

A collection of lightweight linters and checks for artifacts that don't fit traditional AST-based analysis. All tools emit [SARIF](https://sarifweb.azurewebsites.net/) for interoperability with editors, CI systems, and visualization tools.

## Tools

### wikifmt

`lintkit wikifmt ROOT...` recursively scans wiki-style Markdown files for frontmatter validity, broken wikilinks/Markdown links, and basic tag hygiene. Results are emitted as SARIF for easy consumption by editors or CI systems.

## License

MIT
