---
name: using-kubeleash
description: Use when querying or acting on a Kubernetes cluster through the kubeleash MCP server (k8s_list/get/apply/delete/logs/exec/scale/capabilities) — explains the policy-gated workflow, how to read allow/deny outcomes, and resource/namespace/context conventions.
---

# Using kubeleash

kubeleash is a **policy-gated** Kubernetes MCP server. Every call is checked
against a local, per-context policy *before* it touches the cluster. Your
effective access is `what the kubeconfig grants ∩ what the policy allows` —
kubeleash only ever subtracts.

**A denial is intentional policy, not a transient error.** Do not route around
it with a different verb, resource, or context. If something is denied, report
it plainly and stop — or ask the operator to widen the policy.

## Start by asking what you're allowed to do

Before acting in a context, call **`k8s_capabilities`** (optionally with a
`context`). It returns the allowed/denied verbs and resources for that context
and performs **no cluster call**. Doing this first avoids blind denied calls and
shows you the shape of the guardrail you're working inside.

## The tools

Every resource tool shares a common shape:

- `resource` — a plural (`pods`) or a `group/version/kind` (`apps/v1/Deployment`).
  Use the GVK form to disambiguate CRDs or when a plural is ambiguous.
- `name`, `namespace`, `context` — optional. `context` defaults to the kubeconfig
  current-context; pass it explicitly to target a specific cluster/context.
- Omit `namespace` for **cluster-scoped** resources (nodes, namespaces, PVs,
  ClusterRoles, …); set it to scope **namespaced** resources.

| Tool | What it does |
|------|--------------|
| `k8s_capabilities` | What the policy allows in a context (no cluster call). Start here. |
| `k8s_list` | List objects; optional `namespace`, `labelSelector`, `fieldSelector`. |
| `k8s_get` | Fetch one object by `name`. |
| `k8s_apply` | Server-side apply a `manifest` — **creates or updates** by existence. |
| `k8s_delete` | Delete one object by `name`. |
| `k8s_logs`, `k8s_exec`, `k8s_scale` | **Gated but not yet implemented in v0.1** — they pass policy and return "not yet implemented." Don't rely on them. |

## Reading outcomes

A denied call returns one of two reasons — respond to them differently:

- **"denied by deny rule …"** (explicit deny): the operator has explicitly
  forbidden this. **Stop.** Do not retry or look for a workaround.
- **"not granted: no allow rule matches …"** (default deny): nothing permitted
  it. The operator may simply not have granted it — surface this to the human;
  they may choose to widen the policy.

## Work safely — this is a leash

- **Read before you write.** Use `k8s_get` / `k8s_list` to understand current
  state before any `k8s_apply` or `k8s_delete`.
- **Confirm destructive intent.** Before `k8s_delete` (and `exec` / `scale` once
  available), state what you are about to change and why, and prefer the
  narrowest action that accomplishes the goal.
- **`apply` is create-or-update.** It server-side-applies the manifest: an
  existing object is updated, a missing one is created — each gated by the
  matching `create`/`update` verb, so a policy can grant create-but-not-update.
- **Be explicit about context** when it matters. You can reach prod and dev in
  one session; each call is evaluated against its resolved context.

To author or adjust the policy itself, see the `authoring-kubeleash-policy` skill.
