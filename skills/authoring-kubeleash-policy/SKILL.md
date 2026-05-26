---
name: authoring-kubeleash-policy
description: Use when writing or adjusting a kubeleash policy file — the YAML that constrains what an AI agent may do on Kubernetes per context. Covers the context × resource × verb model, deny-wins / default-deny, namespace narrowing, the known verbs, and how to validate.
---

# Authoring a kubeleash policy

A kubeleash policy is local YAML, loaded via `--policy <file>` (or the
`K8S_MCP_POLICY` env var). It constrains what the agent may do, keyed on
**context (regex) × resource × verb**, with an optional **namespace** narrowing
axis.

## Structure

```yaml
policies:
  - contexts: ".*prod.*"          # regex over the active kube context name (required)
    namespaces: ["kube-system"]   # optional; omit = all namespaces + cluster-scoped
    allow:
      resources: ["*"]            # plural ("pods") or group/version/kind; "*" = any
      verbs: [get, list, watch]
    deny:
      verbs: [exec]               # deny wins, regardless of credentials
```

## How evaluation works

- **Deny wins.** Among all rules whose `contexts` (and `namespaces`) match, if
  any `deny` covers the `(resource, verb)`, the call is **denied** — even if
  another rule allows it.
- **Default deny.** Anything not explicitly allowed is denied. There is no
  "allow everything" escape hatch; grant access explicitly.
- **Subtractive only.** Effective access = `kubeconfig-grants ∩ policy-allows`.
  A policy can never grant more than the credentials already do.
- **Namespaces narrow.** Omit `namespaces` and a rule matches all namespaced
  resources **and** cluster-scoped ones. Set `namespaces` and the rule matches
  **only** namespaced resources in those namespaces — cluster-scoped resources
  (nodes, PVs, namespaces, ClusterRoles, …) **never** match a rule that
  specifies `namespaces`. *To govern cluster-scoped resources, write a rule with
  no `namespaces`.*
- **Verbs** come from a known set: `get, list, create, update, delete, logs,
  exec, scale`. `watch` and `patch` are accepted but **reserved** — no v1 tool
  emits them yet, so a rule mentioning them validates but never matches.
  Writing the idiomatic `[get, list, watch]` is safe and forward-compatible.
- `resources` is a plural name (`pods`) or a `group/version/kind`
  (`apps/v1/Deployment`); `"*"` matches any. An omitted `resources` defaults to
  `["*"]`.

## Practices

- **Start read-only, widen deliberately.** A solid prod rule is
  `allow: { verbs: [get, list, watch] }` plus an explicit
  `deny: { verbs: [exec, delete] }`.
- **Spell out destructive denies** even when `allow` doesn't grant them. An
  explicit `deny` yields a clear "denied by deny rule" reason, so the agent
  stops rather than treating it as a not-yet-granted gap.
- **Validate before trusting it.** `kubeleash --policy p.yaml
  --print-effective-policy` prints the normalized rules. A broken or
  unparseable policy **refuses to start** — it never fails open, which (with
  default-deny) is exactly what you want.
- See [`examples/policy.yaml`](../../examples/policy.yaml) for a fuller,
  commented policy (read-only prod, broader staging, namespace-scoped dev).
