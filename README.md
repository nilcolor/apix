# apix

`apix` runs YAML-defined HTTP request sequences — resolving includes for shared auth config,
interpolating variables, extracting response values for use in later steps, and evaluating
assertions with structured exit codes suited for CI pipelines.

## Installation

_In progress — installation instructions will be added here._

## Usage

### Command line

```
apix invoke <scroll> [options]

Application Options:
      --version                         Print version and exit
  -h, --help                            Show this help message

[invoke command options]
          --var=                        Set or override a variable (key=value, repeatable)
          --env=                        Load variables from a .env file
          --step=                       Run only this step (repeatable / comma-separated)
          --from=                       Start execution at this step
          --skip=                       Skip this step (repeatable)
          --dry-run                     Print resolved requests without executing
      -v, --verbose                     Show full request/response details
      -o, --output=[pretty|json|silent] Output format (default: pretty)
          --no-color                    Disable ANSI color output
          --timeout=                    Override global timeout (e.g. 10s, 1m)
          --fail-fast                   Stop on first failure

[invoke command arguments]
  scroll:                               Scroll (YAML request file) to invoke
```

```sh
# Run with credentials from a .env file
apix invoke requests.yaml --env .env.staging

# Override a variable at the CLI
apix invoke requests.yaml --var username=admin

# Run (or skip) individual steps
apix invoke requests.yaml --step get_profile
apix invoke requests.yaml --skip get_profile
apix invoke requests.yaml --from update_profile

# Machine-readable output for CI
apix invoke requests.yaml --output json | jq '.summary'

# Preview resolved requests without executing them
apix invoke requests.yaml --dry-run
```

`--step`, `--from`, and `--skip` always run included files first — includes are part of the
dependency chain (e.g. a login step that produces a token) and can't be skipped independently.

### Scroll file structure

A "scroll" is a YAML file with up to four top-level sections, all optional:

```yaml
include: [...]       # other scrolls to run first; their extracted vars flow into this scope
config: {...}        # HTTP settings: base_url, timeout, headers, redirects, cookie jar
variables: {...}     # static values and $VAR references resolved from the environment
steps: [...]         # ordered list of requests to execute
```

### `config`

```yaml
config:
  base_url: https://api.example.com
  timeout: 30s
  follow_redirects: true   # follow HTTP redirects globally (default: false)
  tls_verify: true
  use_cookie_jar: true     # share cookies across all steps in the run (default: false)
  headers:
    Content-Type: application/json
    Accept: application/json
```

`follow_redirects` defaults to `false` — apix returns the raw response (e.g. a `302`) so
assertions run against the actual server reply, not the final redirected page. Override it
per step with `follow_redirect: true`/`false`.

Config from included files acts as the base; the current file overrides it.

### `variables`

```yaml
variables:
  username: user@example.com
  password: $PASSWORD      # resolved from the environment via the leading $
  page_size: 20
```

Priority (highest wins): `--var key=value` on the CLI → the current file's `variables:` →
included files' `variables:`. Use `--env <file>` to load a dotenv file into the environment
before variables are resolved.

### `include`

```yaml
include:
  - ./auth.yaml          # runs auth steps first; extracted vars become available here
  - ./base-config.yaml   # a file with no steps: only contributes config/variables
```

Included files execute before the current file's steps, their extracted variables enter the
same flat namespace, and config merges bottom-up (deepest include is the base). Circular
includes are a hard error.

### `steps`

```yaml
steps:
  - name: login                    # identifier used in output and error messages
    method: POST
    path: /auth/login              # appended to base_url; use `url:` to override entirely
    headers:
      X-Request-ID: "{{ $uuid }}"
    query:
      debug: true
    body:                          # object → serialized as JSON
      username: "{{ username }}"
      password: "{{ password }}"
    extract: {...}
    assert: {...}
    ask: [...]
    print: "$.body"
    on_error: continue             # stop (default) | continue
    follow_redirect: true          # override config's follow_redirects for this step
```

#### Body variants

At most one of these may be set per step:

```yaml
    body: {...}                    # JSON object

    body_file: payloads/big-request.json   # JSON from a file; {{ }} interpolated before sending

    form:                          # application/x-www-form-urlencoded
      grant_type: password
      client_id: abc

    multipart:                     # multipart/form-data
      file: "@./report.pdf"        # @ prefix = read file from disk
      description: monthly report

    body_raw: "plain text payload" # sent as-is, Content-Type: text/plain
```

### `extract` — pulling values from a response

```yaml
    extract:
      token:       $.body.data.access_token    # JSONPath into the parsed body
      first_item:  $.body.items[0].id          # array indexing
      session_id:  header.X-Session-Id         # response header by name
      status_code: status                      # HTTP status code as a variable
```

| Prefix | Source |
|---|---|
| `$.body.*` | JSONPath into the parsed response body |
| `header.<Name>` | Response header, case-insensitive |
| `status` | HTTP status code |

Extracted variables enter the same flat namespace as `variables:` and are available to every
later step in the run, including in files that include this one.

### `ask` — prompting for user input

```yaml
    ask:
      - var: otp_code
        prompt: "Enter the OTP sent to {{ email }}:"
```

For values that can't be known ahead of time — an OTP sent mid-flow, for example — `ask`
pauses the step and reads a line from stdin, storing it under `var` in the same flat
namespace `extract` uses. It's a list (not a map) so multiple prompts run in a defined order,
and `prompt` may reference `{{ }}` variables extracted earlier in the same step.

If `var` is already set (via `--var`, or an earlier `extract`/`ask`), the prompt is skipped
and the existing value is reused — the non-interactive escape hatch for CI.

### `print` — showing a value

```yaml
    print: "$.body"                  # same source prefixes as extract: $.body.*, header.*, status
    print: "clearance is {{ a_clearance_id }}"   # a {{ }} template instead of a source
```

If `print` contains `{{`, it's treated as a template and interpolated; otherwise it's treated
as a source path, evaluated the same way as `extract`, and pretty-printed (JSON objects/arrays
get indentation).

### `assert` — verifying responses

`assert:` accepts two forms.

**Mapping form** — group assertions under `status`, `body`, and `headers`; a bare scalar means
equality, a single-key object selects an operator:

```yaml
    assert:
      status: 200
      body:
        "$.body.data.email": "user@example.com"      # equality shorthand
        "$.body.data.role":
          in: [admin, editor]                        # operator form
        "$.body.data.items":
          length_gte: 1
      headers:
        Content-Type:
          contains: application/json
```

**Expression form** — a list of `"<source> <operator> <operand>"` strings:

```yaml
    assert:
      - "status == 200"
      - "$.body.age gte 18"
      - "$.body.roles contains admin"
      - "$.body.status in [pending, active]"
      - "$.body.clearance_id == {{ a_clearance_id }}"   # compare against a variable
                                                          # extracted by an earlier step
```

Sources use the same prefixes as `extract`: `status`, `$.body.<path>`, `header.<Name>`. The
operator must be its own whitespace-separated token; operands with spaces or special
characters (a regex, a multi-word phrase) need matching single or double quotes.

Both forms populate the same underlying checks, and **`{{ }}` is interpolated in assertion
values/operands in both forms** — so an assertion can compare a response value against any
variable in the store, including one extracted earlier in the run. This is what makes the
last line above possible: it's how you assert that two different steps' responses agree on a
value, without either side being a hard-coded literal.

| Meaning | Symbol | Keyword |
|---|---|---|
| equals / not equals | `==` `!=` | `equals` `not_equals` |
| gte / lte / gt / lt | `>=` `<=` `>` `<` | `gte` `lte` `gt` `lt` |
| substring / list membership | — | `contains` |
| regex match | — | `matches` |
| existence | — | `exists` (operand is literal `true`/`false`) |
| value in list | — | `in` (operand is a bracket literal, e.g. `[pending, active]`) |
| length compare | — | `length_gte` `length_lte` |

### Built-in variables

Available in any `{{ }}` interpolation without declaration, generated fresh on every use:

| Variable | Value |
|---|---|
| `$uuid` | Random UUID v4 |
| `$timestamp` | Unix timestamp (seconds) |
| `$iso_date` | Current datetime in ISO 8601 |
| `$random_int` | Random integer |

### Output modes

- `pretty` (default) — colored, human-readable, one block per step, summary line at the end.
  The `←` line is the raw HTTP response (status + timing) and is always neutral; `✓`/`✗` are
  reserved for actual assertion results, so a passing status next to a failing assertion never
  gets miscolored:

  ```
  ● get_uuid   GET https://httpbin.org/uuid
    ← 200 OK  (940ms)
    ✓ status
    ✓ body $.body.uuid

  ● get_sample_json   GET https://httpbin.org/json
    ← 200 OK  (947ms)
    ✓ status
    ✓ body $.body.slideshow.title
    ✓ body $.body.slideshow.author

  ──────────────────────────────────────
    2 passed · 0 failed · 1887ms total
  ```

- `pretty --verbose` — adds the full request/response (headers + body) per step, with
  `password`/`secret`/`token`/`authorization`-like fields masked to `***`.
- `json` — structured `{steps: [...], summary: {...}}` on stdout, for piping to `jq` or a CI
  step. Pretty/verbose output always goes to stderr so `-o json | jq ...` stays clean.
- `silent` — no output at all; rely on the exit code.

### Sensitive field masking

Any header or JSON body field whose name contains `password`, `secret`, `token`, or
`authorization` (case-insensitive) is masked to `***` in verbose and JSON output. The same
heuristic masks matching variable names reported by `ask`. Masking happens before the
snapshot is captured, so raw secrets never reach the output layer.

### Exit codes

| Code | Meaning |
|---|---|
| `0` | All steps passed |
| `1` | One or more assertion failures |
| `2` | Execution error (missing file, network error, parse failure, bad flag) |

### Not yet implemented

`retry:` is parsed and warned about on stderr but not executed. `condition:` on steps is not
supported. These are reserved for future work.

## Examples

```yaml
# auth.yaml — shared login, produces {{ token }}
config:
  base_url: https://api.example.com
variables:
  username: $USERNAME
  password: $PASSWORD
steps:
  - name: login
    method: POST
    path: /auth/login
    body:
      username: "{{ username }}"
      password: "{{ password }}"
    extract:
      token: "$.body.data.access_token"
    assert:
      status: 200

# requests.yaml — depends on auth.yaml
include:
  - auth.yaml
steps:
  - name: get_profile
    method: GET
    path: /me
    headers:
      Authorization: "Bearer {{ token }}"
    assert:
      - "status == 200"
      - "$.body.data.email exists true"
```

```sh
apix invoke requests.yaml --env .env.staging
```

See `scrolls/` for runnable scrolls against the public `httpbin.org` service.
