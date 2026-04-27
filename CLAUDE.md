# apix — CLAUDE.md

## Project purpose

`apix` is a CLI tool that executes YAML-defined sequences of HTTP requests.
It resolves file includes for shared config and auth, interpolates variables,
extracts values from responses, and evaluates assertions.

## Commands

```sh
make build          # go build -o bin/apix ./cmd/apix
make test           # go test ./...
make vet            # go vet ./...
make fmt            # gofmt -w .
make lint           # staticcheck ./...
```

Or directly:

```sh
go build ./cmd/apix/...
go test ./...
staticcheck ./...
```

## Package layout

```
cmd/apix/
  main.go        — CLI entry: go-flags parser, RunCommand struct, subcommand registration
  run.go         — RunCommand.Execute body: flag wiring → pipeline → output → exit code
  run_test.go    — integration tests via runCmd() (no binary build required)
  main_test.go   — binary-level smoke tests (builds binary via exec.Command)

internal/
  schema/        — Go types mirroring the YAML schema; custom UnmarshalYAML on Assertion/Duration
  loader/        — Load(): YAML parse + recursive include resolution + config merge + Origin tagging
  vars/          — Store (map), BuildStore(), Interpolate(), built-in generators ($uuid etc.)
  runner/        — Execute(): HTTP client, body variants, sensitive-field masking in snapshots
  extract/       — Extract(): $.body.* JSONPath, header.*, status source prefixes
  assert/        — Evaluate(): all operators (equals, contains, matches, exists, in, gte…)
  pipeline/      — Run(): step filtering, on_error/fail-fast, dry-run, extraction→store update
  output/        — Pretty/PrettyVerbose/JSON/Silent formatters; StepResult and Summary types
```

## Key dependencies

| Package | Purpose |
|---|---|
| `github.com/jessevdk/go-flags` | Struct-tag subcommand CLI parsing |
| `gopkg.in/yaml.v3` | YAML parsing |
| `github.com/ohler55/ojg` | JSONPath evaluation (jp + oj packages) |
| `github.com/fatih/color` | Coloured terminal output; `color.NoColor` for `--no-color` |
| `github.com/joho/godotenv` | `.env` file loading for `--env` flag |
| `github.com/google/uuid` | `$uuid` built-in variable generator |

## Test conventions

- Unit tests live alongside source as `*_test.go` in the same package.
- `internal/loader/testdata/` holds YAML fixtures for loader edge cases.
- HTTP tests use `httptest.NewServer` — no mocking of the HTTP client.
- `cmd/apix/run_test.go` calls `runCmd()` directly (no binary build) for fast integration tests.
- `cmd/apix/main_test.go` builds the binary via `exec.Command("go", "build", ...)` for smoke tests.
- Table-driven tests are used in `assert` and `schema` for operator/unmarshal coverage.
- `output` tests set `color.NoColor = true` and use `t.Cleanup` to restore it.
- `pipeline` tests use `sync/atomic` counters to verify HTTP call counts.

## Sensitive field masking

Fields named `password`, `secret`, `token`, or `authorization` (case-insensitive substring match)
are masked to `***` in request snapshots at capture time inside `runner`. This applies to both
request headers and JSON body keys. The masking happens before the snapshot is stored in
`Response.Request`, so verbose output and JSON output never expose raw secrets.

## Variable interpolation

Syntax: `{{ varname }}` (whitespace-tolerant). Lookup order: built-ins → store. Built-ins
(`$uuid`, `$timestamp`, `$iso_date`, `$random_int`) are generated fresh on every interpolation
call and cannot be shadowed by user variables.

**Note:** assertion values in `assert:` blocks are NOT interpolated — they are compared as-is
against the actual response values.

## Assert body path format

Body assertion keys and extract sources both require the `$.body.` prefix:

```yaml
extract:
  token: "$.body.data.access_token"   # ✓
assert:
  body:
    "$.body.data.role": admin          # ✓
    "$.data.role": admin               # ✗ — missing $.body. prefix
```

## Exit codes

| Code | Meaning |
|---|---|
| `0` | All steps passed |
| `1` | One or more assertion failures |
| `2` | Execution error (missing file, network error, parse failure, bad flag) |

## Deferred features (not implemented)

- `validate` command
- `inspect` command
- Retry execution (`retry:` block is parsed and warned about, not executed)
- `condition:` on steps
