# apix — CLAUDE.md

## Project purpose

`apix` is a CLI tool that executes YAML-defined sequences of HTTP requests.
It resolves file includes for shared config and auth, interpolates variables,
extracts values from responses, and evaluates assertions.

## Commands

```sh
make build          # go build -o bin/apix ./cmd/apix
make test           # go test ./...
make race           # go test -race ./...
make vet            # go vet ./...
make fmt            # gofmt -w .
make lint           # golangci-lint run
```

Or directly:

```sh
go build ./cmd/apix/...
go test ./...
```

`make lint` runs **golangci-lint**, not `staticcheck`. staticcheck is only one of the
linters `.golangci.yml` enables, so `staticcheck ./...` can pass while `make lint` fails.
Gate on `make lint`.

The linter version is pinned in `.golangci-version`, read by both the `Makefile` and
`.github/workflows/ci.yml`. `make lint` installs that exact version when the local one is
missing or mismatched, so a green `make lint` means a green CI lint. Change the linter
version by editing that file only.

## Package layout

```
cmd/apix/
  main.go        — CLI entry: go-flags parser, InvokeCommand struct, subcommand registration
  invoke.go      — InvokeCommand.Execute body: flag wiring → pipeline → output → exit code
  invoke_test.go — integration tests via invokeCmd() (no binary build required)
  main_test.go   — binary-level smoke tests (builds binary via exec.Command)

internal/
  schema/        — Go types mirroring the YAML schema; custom UnmarshalYAML on Assertion/Assert/Duration
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

## Code style

No explanatory comments — not even a brief one describing why a fix or change was made
(referencing a past bug, prior behavior, etc.). This project's default is zero such comments,
full stop, stricter than "only comment non-obvious rationale."

## Test conventions

- Unit tests live alongside source as `*_test.go` in the same package.
- `internal/loader/testdata/` holds YAML fixtures for loader edge cases.
- HTTP tests use `httptest.NewServer` — no mocking of the HTTP client.
- `cmd/apix/invoke_test.go` calls `invokeCmd()` directly (no binary build) for fast integration tests.
- `cmd/apix/main_test.go` builds the binary via `exec.Command("go", "build", ...)` for smoke tests.
- Table-driven tests are used in `assert` and `schema` for operator/unmarshal coverage.
- `output` tests set `color.NoColor = true` and use `t.Cleanup` to restore it.
- `pipeline` tests use `sync/atomic` counters to verify HTTP call counts.

## Sensitive field masking

Fields named `password`, `secret`, `token`, or `authorization` (case-insensitive substring match)
are masked to `***` in request snapshots at capture time inside `runner`. This applies to both
request headers and JSON body keys. The masking happens before the snapshot is stored in
`Response.Request`, so verbose output and JSON output never expose raw secrets. The same
heuristic (`runner.IsSensitive`) also masks matching variable names in the `Asked` map reported
for `ask:` steps, and in `Extracted` and hook variables.

Separately, the *values* of sensitive-named variables are scrubbed at the **output boundary** — every
formatter writes through a redacting writer, so verbose request/response dumps and silent mode's
`print:` pass-through are covered too. Scrubbing chosen fields instead left each new field
unprotected by default. Values under 6 characters are skipped so a short value cannot blank unrelated
output, and longer values are replaced first so the result does not depend on map order.

This is output hygiene, not a security control: it protects the terminal of whoever supplied the
secret. A value that is re-encoded rather than embedded is not caught.

## Variable interpolation

Syntax: `{{ varname }}` (whitespace-tolerant). Lookup order: built-ins → store. Built-ins cannot be
shadowed by user variables.

`$uuid` and `$random_int` are generated fresh on every interpolation call. The time-derived built-ins
(`$timestamp`, `$timestamp_ms`, `$iso_date`) are pinned once per request attempt, so every token in
the same request resolves to the same instant — a signature computed over a body containing
`{{ $timestamp }}` matches the `{{ $timestamp }}` sent in a header.

**Note:** assertion values/operands in `assert:` blocks ARE interpolated (both YAML forms below),
so `{{ }}` can reference any variable in the store, including one extracted by an earlier step.

## Request hooks

`before_send:` computes variables just before a request is sent. It exists for APIs that
require a signature over the rendered body, and it is valid on a step or under `config:` —
a step-level hook replaces the config one wholesale rather than merging per variable.

```yaml
config:
  before_send:
    ts: "builtin.timestamp"
    canonical: "request.method + request.path + request.body + ts"
    sig: "hex(hmac_sha256(canonical, api_secret))"
  headers:
    X-Timestamp: "{{ ts }}"
    X-Signature: "{{ sig }}"
```

Values are **expressions**, not `{{ }}` templates — variables are referenced by bare name.
`{{` inside a hook expression is a load-time error. Expressions evaluate in document order,
each result visible to the next, and are committed to the store only if all of them succeed.

### Render order

```
URL/path → query → body → before_send → headers → send
```

Headers are the only fields that can reference hook results. URL, path, query, body, `form`,
`multipart` and `body_file` all render first and are what the hook signs over.

### Hook environment

| Name | |
|---|---|
| `request.method` `.url` `.path` `.query` `.body` | strings |
| every store variable, by bare name | string |
| `builtin.timestamp` `.timestamp_ms` `.iso_date` | frozen per attempt, identical to `{{ $timestamp }}` |

`request.headers` is absent — headers render after the hook. Headers Go's client adds at send
time (`Host`, `Content-Length`, `User-Agent`, `Accept-Encoding`) are not signable. `$uuid` and
`$random_int` are absent because they are not frozen and could not be matched to a `{{ $uuid }}`
elsewhere in the same request.

### Hook functions

`expr` supplies `upper lower trim replace split join keys sort map filter hasPrefix hasSuffix
indexOf len string int`. apix adds only crypto and encoding:

```
hmac_sha256(data, key) -> bytes     sha256(data) -> bytes
hex(b) -> string    base64(b) -> string    json(v) -> string
```

Digest functions return bytes and must be wrapped in an encoder — a hook result must be a string,
and raw digest bytes in a header value are rejected rather than stringified. `hmac_sha256`'s key
accepts a string or bytes, so derivation chains compose.

Hook variable names, and store variable names, may not collide with `request`, `builtin`, or a
function name; both are rejected rather than silently shadowed.

Hook results are strings. Numbers are formatted without an exponent — `expr` yields a float from
any division, and scientific notation in a header value is a wire bug.

Hook variables are committed to the store and persist past their step, the same way extracted
values do. A later step with no hook still resolves `{{ sig }}` to the previous step's value rather
than erroring.

Hooks are not evaluated under `--dry-run`: no body is built, so any signature shown would be wrong.

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

## Assert expression form

`assert:` also accepts a sequence of expression strings instead of the mapping form above.
Both forms populate the same underlying Status/Body/Headers and can't be mixed within a
single `assert:` block, but a scroll can use either form on a step-by-step basis:

```yaml
assert:
  - "status == 200"
  - "$.body.age gte 18"
  - "$.body.clearance_id == {{ a_clearance_id }}"   # compare against a variable extracted earlier
```

Each line is `<source> <operator> <operand>`, always in that order, with the operator as its
own whitespace-separated token. Sources use the same prefixes as `extract`: `status`,
`$.body.<path>`, `header.<Name>`. Operands with spaces or special characters (a regex, a
multi-word phrase) must be wrapped in matching single or double quotes; otherwise quoting is
optional.

| Meaning | Symbol | Keyword |
|---|---|---|
| equals / not equals | `==` `!=` | `equals` `not_equals` |
| gte / lte / gt / lt | `>=` `<=` `>` `<` | `gte` `lte` `gt` `lt` |
| substring / list membership | — | `contains` |
| regex match | — | `matches` |
| existence | — | `exists` (operand is literal `true`/`false`) |
| value in list | — | `in` (operand is a bracket literal, e.g. `[pending, active]`) |
| length compare | — | `length_gte` `length_lte` |

There's no support for comparing two bare values with no response lookup (e.g. `{{ a }} != {{ b }}`)
— every expression's source must resolve to `status`, a body path, or a header.

## Exit codes

| Code | Meaning |
|---|---|
| `0` | All steps passed |
| `1` | One or more assertion failures |
| `2` | Execution error (missing file, network error, parse failure, bad flag) |

## Deferred features (not implemented)

- `validate` command
- `inspect` command
- Retry execution (`retry:` block is parsed and warned about, not executed). When it lands,
  `before_send` must re-run per attempt **and** the time built-ins must be re-frozen with it,
  or a retried request signs a stale timestamp.
- `condition:` on steps
- `after_receive` hook and a `$.var.<name>.<path>` source for extract/assert — needed only when a
  response must be decoded before assertions see it
- Query-parameter signing: `schema.Step.Query` is `map[string]string`, so repeated parameters
  cannot be expressed; that is the expensive part of the change
