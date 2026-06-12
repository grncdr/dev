# Named ports per process — design

Lets a single process declare multiple named listening targets. Each named
port allocates independently (random / fixed / unix) and is reachable from
proxy matchers, health checks, the process itself, and its dependents
through env vars that append the port name to today's `PORT` / `DEV_PORT`
conventions.

## Config schema

```toml
[process.rails]
command = "rails server"

[process.rails.ports]
http  = "random"
grpc  = 50051
cable = "unix"

[process.rails.health]
type = "http"
path = "/up"
port = "http"          # required when ports has > 1 entry

[[process.rails.proxy]]
subdomain = null
path = "/"
port = "http"          # required when ports has > 1 entry

[[process.rails.proxy]]
subdomain = "grpc"
path = "/"
port = "grpc"
```

- `ports` and the existing `port = ...` are **mutually exclusive**. A process
  uses exactly one form.
- `ports.<name>` accepts the same values as today's `port`: `<int>` (fixed
  TCP), `"random"` (allocated localhost port), or `"unix"` (socket).
- Port names: trimmed, lowercase ASCII letters / digits / underscores only.
  Invalid or empty names are rejected at config load.
- Existing `port = <int> | "random" | "unix"` configs keep working unchanged.
  The implicit `port = "random"` (when a `proxy` block is declared without
  an explicit port) also stays — `ports` is always explicit.
- `tcp_listen` matchers (`[[process.X.proxy]] tcp_listen = N`) still require
  `port = "<name>"` on the matcher when the process uses `ports`.

## Env vars

For a process using `ports`, no bare `PORT` / `DEV_PORT` / `DEV_SOCKET` /
`DEV_PORT_<PROC>` / `DEV_SOCKET_<PROC>` are injected. Each named port gets:

| Audience       | TCP / random              | Unix                          |
|----------------|---------------------------|-------------------------------|
| Self           | `PORT_<NAME>=<port>`      | `SOCKET_<NAME>=<path>`        |
| Dependents     | `DEV_PORT_<PROC>_<NAME>`  | `DEV_SOCKET_<PROC>_<NAME>`    |

Port-name segments in env vars are uppercased; non-alphanumerics in process
names map to `_` (the existing `envKey` rule). Single-port (`port = ...`)
configs keep emitting the existing bare vars; nothing changes for them.

## Allocation & unix paths

- Fixed `int` — that TCP port, no allocation.
- `"random"` — high localhost TCP port allocated once per `(process, name)`.
- `"unix"` — socket at `${WORKTREE_STATE}/<process>-<name>.sock`.

## Validation (hard errors at config load)

1. `port` and `ports` on the same process.
2. Empty `ports = {}`; any port name that is blank or fails the
   letters / digits / underscores rule.
3. Matcher `port = "X"` references a name not in that process's `ports`.
4. Matcher omits `port` when `ports` has more than one entry.
5. `health.port = "X"` references a name not in `ports`, **or**
   `health.port` is omitted when `ports` has more than one entry.
6. Matchers in a single-port (`port = ...`) process MUST NOT set
   `port = "<name>"` (there is no name to reference).

When `ports` has exactly one entry, matcher `port` and `health.port` are
optional and resolve implicitly to that sole entry; specifying the name
explicitly is also valid.

Each error names the process and the offending key. Every CLI command that
loads the config (`dev start`, `dev restart`, `dev status`, `dev config`,
`dev share`, `dev daemon start`) fails with the same message. The running
daemon rejects a hot reload that fails validation and keeps the previously
loaded config.

## Module changes

- `internal/config/` — parse `ports` and matcher / health `port = "<name>"`;
  add the cross-key validation rules. Likely a new
  `internal/config/process_ports.go` to keep parsing + validation isolated.
- `internal/daemon/manager.go`:
  - `resolveProcessTarget` returns a set of `(name, network, address, port)`
    tuples instead of one. The unnamed single-port form keeps returning the
    same shape it does today (one tuple with `name = ""`).
  - Process env injection emits `PORT_<NAME>` / `SOCKET_<NAME>` for each
    named port; the bare-name single-port form is unchanged.
  - `dependencyPortEnvVars` emits one entry per named port of each dep
    (`DEV_PORT_<DEP>_<NAME>` / `DEV_SOCKET_<DEP>_<NAME>`).
- `internal/daemon/health.go` — health probe resolves the selected port by
  name and uses its target.
- Router / matcher resolution — picks the right target per matcher's
  `port` field. Single-port matchers (no `port` field) still resolve to the
  process's only target.
- `docs/config.md` — document `ports` and the matcher / health `port` field.

## Tests

- `internal/config`:
  - `ports.<name>` parses each value type (int, "random", "unix").
  - Validation rules 1–5 each produce a clear error referencing the process
    and the offending key.
  - Invalid / empty port names rejected.
- `internal/daemon`:
  - `PORT_<NAME>` and `SOCKET_<NAME>` injected for self; `DEV_PORT_<PROC>_<NAME>`
    and `DEV_SOCKET_<PROC>_<NAME>` injected for dependents.
  - Matchers route to the correct named port; mixed `random` / `unix` /
    fixed-int targets all dial correctly.
  - Single-port (`port = ...`) configs continue to emit bare `PORT` /
    `DEV_PORT` / `DEV_PORT_<PROC>` exactly as before (regression guard).
  - Health probe targets the named port.

## Out of scope

- Per-port `health` blocks. One process has one health check; it picks the
  port to probe.
- Per-port `gateway.expose` granularity. Expose stays process-level; the
  matchers already route per-port via the new `port = "<name>"` field.
- Auto-migration of existing `port = ...` configs to the `ports` form.
- Renaming or deprecating the existing `port` form.
