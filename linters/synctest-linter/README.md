# Synctest Linter Module

This module implements a linter for golangci-lint that detects `time.Sleep` usage in test files without the `testing/synctest` package.

## Purpose

Encourage the use of Go's experimental synctest package for deterministic timing tests instead of using `time.Sleep` which can lead to slow and flaky tests.

## Structure

- `synctest.go` - Main linter implementation using the golangci-lint plugin interface
- `go.mod` - Module dependencies

## Integration with golangci-lint

This linter is designed to work as a custom module plugin for golangci-lint. Due to changes in golangci-lint v2's plugin system, it currently runs as a standalone tool via `cmd/synctest-linter/`.

### Configuration

The linter can be configured in `.golangci.yml`:

```yaml
linters-settings:
  custom:
    synctest:
      type: module
      description: Checks that test files using time.Sleep import and use the synctest package
      original-url: github.com/ahrav/go-judgy/linters/synctest-linter
      settings:
        documentation-path: docs/SYNCTEST_INTEGRATION.md
```

## Building

When golangci-lint's custom module support is available:

```bash
golangci-lint custom -v
```

This will create a custom golangci-lint binary with the synctest linter included.

## Current Status

The linter is implemented and ready for integration, but currently runs as a standalone tool at `cmd/synctest-linter/` due to golangci-lint v2 plugin system changes.

## See Also

- [SYNCTEST_LINTER.md](../../docs/SYNCTEST_LINTER.md) - Full documentation
- [SYNCTEST_INTEGRATION.md](../../docs/SYNCTEST_INTEGRATION.md) - Synctest usage guide