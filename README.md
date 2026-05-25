# 🐶🦮 kubeleash

> **Point it at your over-privileged kubeconfig — it still can't nuke prod.**

[![CI](https://github.com/kubeleash/kubeleash/actions/workflows/ci.yml/badge.svg)](https://github.com/kubeleash/kubeleash/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/kubeleash/kubeleash)](https://goreportcard.com/report/github.com/kubeleash/kubeleash)
[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/kubeleash/kubeleash/badge)](https://scorecard.dev/viewer/?uri=github.com/kubeleash/kubeleash)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue)](LICENSE)
![Status](https://img.shields.io/badge/status-pre--alpha-orange)

**kubeleash is a Kubernetes [MCP](https://modelcontextprotocol.io) server with a
twist: RBAC-style, context-scoped access control for AI agents.** You point it
at a kubeconfig — even a cluster-admin one — and a local policy file constrains
what the agent can actually do, *per kube context*. Destructive actions are
gated **before any call reaches the cluster**.

> ⚠️ **Pre-release.** The v0.1 server is implemented and runs today **from
> source** (see [Quickstart](#quickstart)), but there is no tagged release yet —
> the Homebrew / `go install` / container channels below light up on the first
> tag. Watch/star to follow along.

## Why

Existing Kubernetes MCP servers inherit the kubeconfig's permissions wholesale.
kubeleash adds three things native RBAC can't express for this use case:

- **Constrain the agent independently of the credentials.** Effective access is
  always `kubeconfig-grants ∩ policy-allows` — kubeleash only ever *subtracts*.
- **Context-aware guardrails.** Policy varies by context (prod vs staging vs
  dev); native RBAC is per-cluster.
- **Block destructive verbs** (`delete`/`exec`/…) as a safety net against agent
  mistakes and prompt injection.

## Policy in 10 seconds

```yaml
policies:
  - contexts: ".*prod.*"          # regex over the active context name
    allow:
      resources: ["*"]
      verbs: [get, list, watch]   # read-only in prod
    deny:
      verbs: [exec]               # never, regardless of credentials
```

Deny wins. Default deny. A broken policy refuses to start — it never fails open.

See [`examples/policy.yaml`](examples/policy.yaml) for a fuller, commented policy
(read-only prod, broader staging, namespace-scoped dev).

## Quickstart

No release yet, so run it from source (Go 1.26+):

```bash
git clone https://github.com/kubeleash/kubeleash && cd kubeleash
go build -o kubeleash ./cmd/kubeleash

# See how kubeleash validates and normalizes a policy (no cluster needed):
./kubeleash --policy examples/policy.yaml --print-effective-policy

# Try it without touching any cluster — every decision is logged, nothing runs:
./kubeleash --policy examples/policy.yaml --dry-run
```

Then point an MCP client at it (see [below](#use-it-as-an-mcp-server)). kubeleash
speaks MCP over stdio, so it's launched by your client, not run as a daemon.

| Flag | Purpose |
|------|---------|
| `--policy <path>` | Policy file. **Required** (or set `K8S_MCP_POLICY`); with neither, kubeleash refuses to start — default-deny never fails open. |
| `--kubeconfig <path>` | Explicit kubeconfig. Omit to use the standard client-go rules (`$KUBECONFIG`, `~/.kube/config`). |
| `--dry-run` | Evaluate + log every decision, but never execute against a cluster. |
| `--print-effective-policy` | Print the resolved/normalized rules and exit. |
| `--log-level <level>` | `debug` / `info` / `warn` / `error` (default `info`). The audit log is JSON on **stderr** (stdout is the MCP transport). |
| `--version` | Print version, commit, and build date. |

## Install

All channels run kubeleash **locally over stdio** — your client launches the
binary; nothing is hosted.

### One-click

**Claude Code plugin** — this repo is its own plugin marketplace. With the
`kubeleash` binary on your PATH (see [Manual](#manual)):

```shell
/plugin marketplace add kubeleash/kubeleash
/plugin install kubeleash@kubeleash
```

You'll be prompted for your policy file (and optionally a kubeconfig).

**Cursor** — paste this into your browser address bar (Cursor can't prompt, so
edit the placeholder path afterward in *Settings → MCP*):

```
cursor://anysphere.cursor-deeplink/mcp/install?name=kubeleash&config=eyJjb21tYW5kIjoia3ViZWxlYXNoIiwiYXJncyI6WyItLXBvbGljeSIsIi9hYnNvbHV0ZS9wYXRoL3RvL3BvbGljeS55YW1sIl19
```

(Decodes to `{"command":"kubeleash","args":["--policy","/absolute/path/to/policy.yaml"]}`
— requires `kubeleash` on PATH.)

**VS Code** — VS Code prompts you for the policy path:

```
vscode:mcp/install?%7B%22name%22%3A%22kubeleash%22%2C%22command%22%3A%22kubeleash%22%2C%22args%22%3A%5B%22--policy%22%2C%22%24%7Binput%3ApolicyPath%7D%22%5D%2C%22inputs%22%3A%5B%7B%22id%22%3A%22policyPath%22%2C%22type%22%3A%22promptString%22%2C%22description%22%3A%22Path%20to%20your%20kubeleash%20policy.yaml%22%7D%5D%7D
```

Or the web-redirect badge URL:
`https://insiders.vscode.dev/redirect/mcp/install?name=kubeleash&config=%7B%22name%22%3A%22kubeleash%22%2C%22command%22%3A%22kubeleash%22%2C%22args%22%3A%5B%22--policy%22%2C%22%24%7Binput%3ApolicyPath%7D%22%5D%2C%22inputs%22%3A%5B%7B%22id%22%3A%22policyPath%22%2C%22type%22%3A%22promptString%22%2C%22description%22%3A%22Path%20to%20your%20kubeleash%20policy.yaml%22%7D%5D%7D`

**Claude Desktop** *(on first release)* — download `kubeleash.mcpb` from the
[releases page](https://github.com/kubeleash/kubeleash/releases) and
double-click it. The bundle ships the binary, so no separate install is needed;
Claude Desktop prompts you for the policy and kubeconfig.

### Manual

```bash
# Homebrew (on first release)
brew install kubeleash/tap/kubeleash

# Go
go install github.com/kubeleash/kubeleash/cmd/kubeleash@latest

# Container (great for running the MCP server sandboxed)
docker run --rm -i -v ~/.kube:/root/.kube:ro -v ./policy.yaml:/policy.yaml:ro \
  ghcr.io/kubeleash/kubeleash --policy /policy.yaml
```

> kubeleash runs **locally over stdio** and talks only to your clusters. There
> is intentionally **no remote/hosted URL connector** — it would mean handing
> your cluster credentials to a third party.

## Use it as an MCP server

kubeleash speaks MCP over stdio. Point your client at the binary (or container):

```jsonc
// Claude Desktop / Cursor / VS Code MCP config
{
  "mcpServers": {
    "kubeleash": {
      "command": "kubeleash",
      "args": ["--policy", "/absolute/path/to/policy.yaml"],
      "env": { "KUBECONFIG": "/absolute/path/to/kubeconfig" }
    }
  }
}
```

## Privacy

**Zero telemetry. No phone-home.** kubeleash talks only to the Kubernetes API
servers you point it at — by design, because you'll run it against real clusters
with privileged credentials.

## Project

- [Design](docs/design.md) · [Contributing](CONTRIBUTING.md) ·
  [Security & threat model](SECURITY.md) · [Code of Conduct](CODE_OF_CONDUCT.md)
- License: [Apache-2.0](LICENSE)
