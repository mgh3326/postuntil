# postuntil

`postuntil` POSTs a JSON request exactly once, obtains an identifier from the response, polls a JSON status endpoint, and exits with the honest result. It is intended for cron and systemd timers.

## 30-second example

```sh
postuntil run \
  --post https://example.internal/api/jobs --body '{"kind":"example"}' \
  --header-env Authorization=JOB_TOKEN \
  --id-path .task_id --poll 'https://example.internal/api/jobs/{{.id}}' \
  --until .state=done --fail-when .state=error
```

`--body @request.json` reads a JSON file. `postuntil run -f examples/job.toml` reads the equivalent TOML; explicitly supplied flags take precedence. Use `--dry-run` to inspect the resolved request without making network calls. Header values sourced with `--header-env`, and `Authorization`, are printed as `***`.

## Flags

| Flag | Meaning |
| --- | --- |
| `--post URL`, `--body JSON` | Required POST endpoint and JSON body. |
| `--header K=V`, `--header-env K=ENV` | Request headers; use the latter for secrets. |
| `--idempotency-key auto\|VALUE` | Defaults to a daily deterministic key. |
| `--idempotency-header NAME` | Header carrying the idempotency key (default `Idempotency-Key`). |
| `--id-path .path` | Identifier in the POST response. |
| `--poll URL_TEMPLATE` | Status URL; it must contain `{{.id}}`. |
| `--until .path=value`, `--fail-when .path=value` | Simple JSON predicates, including `.items[0].state`. |
| `--interval`, `--timeout`, `--max-polls` | Fixed polling cadence and limits. |
| `--hc-ping URL` | Final success ping uses URL; all other final outcomes use `URL/fail`. |
| `--dry-run`, `--quiet`, `--json` | Safe request preview, quieter progress output, JSON summary mode. |

The final stdout line is always a JSON summary with `outcome`, `id`, `polls`, `elapsed_ms`, and `last_state`; all operational logs go to stderr. Polling retries network and 5xx errors only three consecutive times. A 4xx response fails immediately.

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | Success predicate matched. |
| 1 | Failure predicate matched. |
| 2 | Timeout or maximum polls reached. |
| 3 | POST/poll transport error, HTTP 4xx, repeated 5xx, or missing identifier. |
| 4 | Invalid configuration. |

## systemd

Copy the example [service](deploy/systemd/postuntil-example.service) and [timer](deploy/systemd/postuntil-example.timer), put `JOB_TOKEN=...` in `/etc/postuntil/example.env`, then place a job file at `/etc/postuntil/job.toml`. `EnvironmentFile` keeps the token out of process arguments.

## Why the idempotency key?

With `auto`, the key is SHA-256 of the POST URL, canonical JSON body, and UTC date, truncated to 32 hexadecimal characters. The same request on the same day therefore has the same key after a timer retry or reboot. A changed body receives a different key.

## Build

```sh
CGO_ENABLED=0 go build ./...
postuntil version
```
