# NeoHome MCP integration

NeoHome can expose its simulated world to external AI clients through the
Model Context Protocol (MCP). The AI joins as the persistent, unprivileged
`mcp-agent` character on its own simulated PC on the household LAN. Its shell
uses NeoHome builtins and virtual state; it cannot execute host binaries or
send commands to the host shell.

The initial tool set is deliberately small:

* `world_status` reports simulation time and the AI character's own device.
* `run_shell` runs one virtual shell command as `mcp-agent`. Commands can
  change persistent simulated state, so treat this tool as a game-control
  capability, not a read-only chat interface. There is no per-command
  allowlist: the tool carries every NeoHome builtin, which includes the
  network-facing ones (`whois`, `scan`, `recon`, `ftp`, `sftp`, `scp`, …), so
  additions to the shell surface need no change here.

All MCP clients currently share this one character and its permissions;
per-client identities and authorization policies are not implemented yet.

## Streamable HTTP server

Set both variables to enable the HTTP listener. It is disabled when
`NEOHOME_MCP_ADDR` is unset.

```sh
export NEOHOME_MCP_ADDR=127.0.0.1:8765
export NEOHOME_MCP_TOKEN="$(openssl rand -hex 32)"
./neohome
```

The endpoint is `http://127.0.0.1:8765/mcp`. Requests require a bearer token,
JSON content, and an `Accept` header containing both `application/json` and
`text/event-stream`. Browser `Origin` headers are rejected unless the exact
origin is configured with `NEOHOME_MCP_ORIGIN`; browser CORS preflight is not
enabled.

For remote clients, bind only on a trusted interface, use a firewall and a TLS
reverse proxy, and keep the bearer token secret:

```sh
NEOHOME_MCP_ADDR=127.0.0.1:8765 \
NEOHOME_MCP_TOKEN='<at least 32 random characters>' \
./neohome
```

Do not expose the HTTP listener directly to the public internet. The built-in
transport uses a static bearer token; it does not implement the MCP OAuth
authorization flow.

## stdio clients

The stdio mode is a bridge to the running HTTP listener, so both transports
operate on the same authoritative world rather than creating separate world
copies. Configure an MCP client to launch:

```sh
NEOHOME_MCP_URL=http://127.0.0.1:8765/mcp \
NEOHOME_MCP_TOKEN='<same bearer token>' \
./neohome mcp stdio
```

The bridge forwards newline-delimited JSON-RPC messages and keeps protocol
output on stdout; application errors are reported on stderr.
