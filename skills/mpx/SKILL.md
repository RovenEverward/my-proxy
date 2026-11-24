---
name: mpx
description: Operate MPX — an Agent-Native pure-Go multi-node proxy orchestration core (xray / sing-box) and multi-format subscription server. Use when managing proxy nodes, inbounds (VLESS/REALITY, Hysteria2, TUIC, Trojan, WS/gRPC/XHTTP), hosts (including on-demand CDN edge IP optimization), users, plans, routing rules, external subscription sources, or backups via the mpx CLI.
---

# MPX Agent Skill

MPX is a single-binary pure-Go control plane + CLI for multi-node `xray` / `sing-box` proxy fleets.
All state lives in one SQLite database on the control plane. Nodes run no MPX agent — the control plane manages them over SSH and their native core APIs.

## Core Mental Model

Keep these three invariants straight before running any write command:

1. **Three propagation paths (do not confuse them):**
   - **Instant (subscription only, no node restart):** `mpx host`, `mpx rule`, `mpx group`, `mpx source`, `mpx settings`, `mpx node drain/undrain`. Next client pull sees the change immediately.
   - **Auto-converged every 30s (no node restart for Xray):** `mpx user`, `mpx plan`. Control plane adds/removes users via Xray gRPC `adu`/`rmu` without dropping active connections. (Note: `sing-box` QUIC inbounds restart on membership change.)
   - **Explicit structural deploy (restarts node core, briefly drops connections):** `mpx node sync <node>`. Required after `mpx inbound add/edit/rm`, `mpx inbound cert`, `mpx node edit --outbound-ip`, or `mpx mirror`. Always pre-flight with `mpx node sync --dry-run <node>` first.
2. **Always prefer `-o json`:** Every `mpx` command accepts `-o json` / `--output json`. On failure with `-o json`, `stdout` stays empty and `stderr` receives a structured JSON error `{"error": "...", "status_code": ...}` with a non-zero exit code.
3. **Never touch SQLite directly:** Always talk through the `mpx` CLI (or control plane `/api`). The control plane owns database locking and subscription cache invalidation.

## Connection & Auth

Prefer SSH channel mode (no stored tokens, no cert fingerprints, no password expiry):

```bash
mpx connect https://203.0.113.10:8080 --ssh my-cp-alias
```

- `--ssh` uses your local `~/.ssh/config` (keys, agent, ProxyJump, custom port) and runs `mpx api-relay` on the control plane host.
- If SSH is unavailable, use password login non-interactively via `--password-stdin`:
  ```bash
  printf '%s\n' "$MPX_ADMIN_PASS" | mpx connect 203.0.113.10:8080 --fp SHA256:... --password-stdin
  ```
- Multiple control planes are supported via `--profile <name>` or `MPX_PROFILE`.

## Read-Only Triage Sequence

Before making changes, inspect current state read-only:

```bash
mpx status -o json        # Control plane uptime, tick heartbeat, counts, backoffs (no SSH)
mpx doctor -o json        # Full health check: DB, every node live SSH check, 6 sub formats (exit 3 on fail)
mpx node list -o json     # Cached node list, display names, drift, cert expiry
mpx inbound list -o json  # All inbounds across nodes
mpx user list -o json     # Users, plans, quota usage, expiry
mpx rule list -o json     # Global routing rules and final target
```

Exit codes:
- `0`: success (for `doctor`, includes `warn`-only results)
- `1`: CLI usage, network, or API error
- `3`: `mpx doctor` completed and found at least one `fail` check

## Safe Structural Changes (Dry-Run First)

Whenever modifying inbounds, TLS certificates, mirrors, or node outbound IP strategy:

```bash
# 1. Inspect rendered config & diff hashes without touching the remote node
mpx node sync --dry-run <node> -o json

# 2. Optional: inspect full rendered JSON config
mpx node config <node>

# 3. Deploy atomically (validates with xray -test / sing-box check; rolls back on failure)
mpx node sync <node> -o json

# 4. Verify live status
mpx node status <node> -o json
```

When maintaining a node that carries active users, drain it from subscriptions before disruptive work:

```bash
mpx node drain <node> -o json
# ... perform maintenance / sync ...
mpx node undrain <node> -o json
```

## Common Workflows

### Nodes & Inbounds

```bash
# Register a node (assumes control plane SSH key is authorized; omit --ssh-password in non-interactive mode)
mpx node add tokyo-1 --address 198.51.100.7 -o json
mpx node install tokyo-1 --core xray -o json          # or --core sing-box

# Add a VLESS + REALITY inbound
mpx inbound add --node tokyo-1 --tag vless-reality --protocol vless --port 443 \
  --network tcp --security reality --sni www.microsoft.com --reality-dest www.microsoft.com:443 \
  --flow xtls-rprx-vision -o json

# Push a managed TLS cert (e.g. from acme.sh) to an inbound, then sync
mpx inbound cert set <node>/<tag> --cert /path/fullchain.pem --key /path/privkey.pem -o json
mpx node sync --dry-run <node> -o json && mpx node sync <node> -o json
```

### Hosts & On-Demand Agent CDN Optimization

MPX intentionally does **not** run a background CDN scanner or probe daemon. Instead, when the user wants better CDN edge IPs (e.g. Cloudflare WS/TLS inbounds), the Agent benchmarks directly from the user's real local network and writes the winners via `mpx host`:

1. **Discover CDN inbounds and current hosts:**
   ```bash
   mpx inbound list -o json
   mpx host list --inbound <id> -o json
   ```
   Identify the template host (SNI, `Host` header, WS path, port, remark template).
2. **Benchmark directly on the user's physical network (bypassing any active VPN/TUN):**
   - Bind sockets/curl to the physical network interface (e.g. `en0`/`en1`/`eth0`) so measurements reflect the user's real ISP path instead of looping through the proxy.
   - Sample candidate Cloudflare edge CIDRs on port `443`:
     - Stage 1: concurrent TCP connect latency + 0% loss filter (e.g. 4 probes per IP).
     - Stage 2: TLS handshake + HTTP request with SNI (`--resolve <host>:443:<ip>`) verifying genuine edge response (`CF-Ray` / `cf-ray` header and datacenter colo).
     - Stage 3: sustained 8–10 s HTTPS download throughput test (not a single tiny ping!) to rank steady-state speed and stability.
3. **Update hosts via `mpx host` (instant effect, no `node sync` required):**
   ```bash
   mpx host add --inbound <id> --address <top-ip> --port 443 \
     --sni <sni> --host <ws-host> --path <ws-path> --remark "{NODE_DISPLAY} · CDN {HOST_IP}" -o json
   mpx host rm <stale-host-id> -o json
   ```
4. **Verify rendered subscriptions:**
   Fetch a test user's Clash / sing-box subscription URL (`mpx user links <user> -o json`) and confirm the new edge IPs appear cleanly and old IPs are gone.

### Users, Plans & Subscriptions

```bash
mpx plan list -o json
mpx user add alice --plan 默认套餐 --limit 100GB --reset-strategy monthly --expire 2026-12-31 -o json
mpx user links alice -o json
mpx user edit alice --limit 200GB -o json
mpx user revoke-sub alice -o json      # Rotate compromised subscription token
```

### Routing Rules & Proxy Groups

```bash
mpx group list -o json
mpx rule list -o json
mpx rule catalog -o json               # Built-in ruleset keys
mpx rule add cidr 203.0.113.10/32 DIRECT --at 1 --note "Bypass control plane VPS" -o json
mpx rule add suffix example.com 代理组名 -o json
mpx rule test api.github.com -o json   # Dry-test which rule matches a domain
```

### External Sources, Backups & Control Plane Maintenance

```bash
mpx source list -o json
mpx source refresh <source> -o json
mpx backup -o ./mpx-backup.tar.gz      # Online SQLite hot backup + SSH key (mode 0600; treat as secret!)
mpx update show -o json                # Read-only version check across CLI, control plane, and nodes
```

## Safety Guardrails

- **Never expose secrets:** Do not echo `MPX_TOKEN`, subscription tokens, Reality/WireGuard private keys, backup archives, or Cloudflare API tokens into logs, commits, or public chat transcripts.
- **Avoid interactive prompts:** Pass explicit flags (`--password-stdin`, `--cf-token-stdin`, `-y` on `mpx update apply`) so commands never hang waiting on a TTY.
- **Respect `node sync` blast radius:** Never run `mpx node sync` for host, user, plan, or routing changes — it restarts the proxy core unnecessarily. Always check `mpx node sync --dry-run <node> -o json` first.
- **Never delete nodes carelessly:** `mpx node rm` cascades inbounds and hosts in SQLite immediately without confirmation. Use `mpx node drain` when taking a node out of rotation temporarily.
