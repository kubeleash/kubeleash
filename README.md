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

> ⚠️ **Pre-alpha, no releases yet.** The design is settling and there is no
> working binary on a tag yet. Watch/star to follow along. Install instructions
> below describe the intended distribution once `v0.1` ships.

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

## Install (planned for v0.1)

<!-- These channels are wired and fire on the first tag. -->

**One-click (planned):** a Claude Code plugin (`/plugin install kubeleash@…`), a
Claude Desktop `.mcpb` extension (double-click), and "Add to Cursor / VS Code"
buttons — all run kubeleash locally over stdio.

**Manual:**

```bash
# Homebrew
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
