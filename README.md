# apix

`apix` runs YAML-defined HTTP request sequences — resolving includes for shared auth config,
interpolating variables, extracting response values for use in later steps, and evaluating
assertions with structured exit codes suited for CI pipelines.

## Install

```sh
go install github.com/nilcolor/apix/cmd/apix@latest
```

Or build from source:

```sh
git clone https://github.com/nilcolor/apix
cd apix
make build          # produces bin/apix
```

## Example

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
      status: 200
      body:
        "$.body.data.email":
          exists: true
```

```sh
# Run with credentials from a .env file
apix invoke requests.yaml --env .env.staging

# Override a variable at the CLI
apix invoke requests.yaml --var username=admin

# Machine-readable output for CI
apix invoke requests.yaml --output json | jq '.summary'

# Preview resolved requests without executing
apix invoke requests.yaml --dry-run
```

See `scrolls/` for runnable files against the public `httpbin.org` service.
See `docs/` for the full schema reference and CLI design.
