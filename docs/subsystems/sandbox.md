# Sandbox

A box of its own for untrusted work: a command an agent asks to run in the
sandbox goes to a fresh container — not on magpie's filesystem, not on its
network reach, with nothing of magpie's inside.

## Where the privilege sits

magpie never talks to a container engine. It POSTs a command to a broker —
`magpie sandbox serve`, a program beside magpie that owns the Docker (or
Podman) socket. The broker starts the box, runs the command, answers with
what came back, and the box goes away (`--rm`).

- A box cannot reach magpie's `/config`, keys or the engine socket: the
  root is read-only, nothing of the host is mounted, capabilities are
  dropped (`--cap-drop ALL`, `no-new-privileges`), `pids-limit 512`,
  `/work` and `/tmp` are tmpfs.
- A box has no network unless the plan says (`network: none | egress |
  host`); `egress` is the default bridge (out only), `host` is explicit.
- The broker listens on loopback only (`internal/sandbox.Save` refuses a
  broker URL off this computer), and reads nothing from the request but the
  command line and an image name — the limits and mounts are the plan's,
  never the caller's.
- Output is capped at 1 MiB a stream; one command runs at most the plan's
  `seconds`; at most `maxBoxes` (1–4) commands run at once; the rest are
  refused, not queued.

## The pieces

| Path | What it is |
| --- | --- |
| `internal/sandbox` | the plan (`sandbox.json`), the client, the broker, the tests |
| `internal/gateway/sandbox_mcp.go` | the MCP server at `/mcp/magpie/sandbox` (`sandbox_exec`, `sandbox_boxes`, `sandbox_reset`) |
| `internal/gui/sandbox_api.go` | the page's API (`/api/sandbox`, `/save`, `/try`, `/reset`) |
| `plugins/sandbox` | the page (off by default: the plugin only draws it) |
| `internal/cli/sandbox_cli.go` | `magpie sandbox serve / status / on / off` |

The MCP server lists no tool while the plan is off, so an agent is not
offered a tool that answers "the box is off". The API and the page say the
same: off is a state, not an error to work around by running the command
here — nothing runs outside the box.

## Turning it on

1. Start the broker beside magpie (a compose sidecar, a systemd unit, or a
   shell): `magpie sandbox serve --addr 127.0.0.1:8794`.
2. Turn the sandbox on in the window: Sandbox → the switch (it writes
   `sandbox.json`, `on: true`).
3. Try a command on the page.

Restore (the page's button, or `sandbox_reset`) removes every labelled box,
including one a command still holds: the next command starts from the
image again.

## Defaults and cost

Off, and nothing runs: no broker, no box, no image pulled. On, the broker
is a small idle process; a box exists only while its command runs. The
plan starts at 1024 MB, 2 CPUs, 300 s, 2 boxes — the page holds `maxBoxes`
to 4.
