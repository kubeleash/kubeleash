# kubeleash — Design

> Status: pre-alpha. This describes the intended design; implementation is in
> progress and details may change.

## What it is

`kubeleash` is a Kubernetes MCP (Model Context Protocol) server whose
differentiator is **RBAC-style, context-scoped access control for AI agents**.
You point it at a kubeconfig — even an over-privileged, cluster-admin one — and
a local policy file constrains what the agent can actually do, per kube context.

The one-line pitch: *"Point it at your over-privileged kubeconfig and it still
can't nuke prod — policy is scoped per-context, with destructive actions gated
before any call reaches the cluster."*

## Why (problem statement)

Existing Kubernetes MCP servers (mcp-server-kubernetes, kagent, etc.) inherit
the kubeconfig's permissions wholesale — whatever the credentials grant, the
agent can do. kubeleash addresses three gaps that native Kubernetes RBAC does
not cover for this use case:

1. **Over-privileged kubeconfig** — constrain what the *agent* can do
   independently of what the credentials technically allow.
2. **Context-aware guardrails** — policy varies by kube context (prod vs
   staging vs dev). Native RBAC is per-cluster and cannot express "this
   connection."
3. **Prevent destructive actions** — block `delete`/`exec`/etc. regardless of
   cluster permissions, as a safety net against agent mistakes or prompt
   injection.

Effective permission is always `kubeconfig-grants ∩ policy-allows`. kubeleash
can never grant more than the credentials already do; it only subtracts.

## Policy model

Policy is keyed on **context (regex) × resource × verb**, with an **optional
namespace** narrowing axis. Rules can **allow** or **deny**, and **deny wins**.

### Config (YAML)

Loaded via `--policy <file>` or `K8S_MCP_POLICY` env.

```yaml
policies:
  - contexts: ".*prod.*"        # regex, required
    namespaces: ["kube-system"] # optional; omit = all ns + cluster-scoped
    allow:
      resources: ["*"]          # resource plural ("pods") or GVK; "*" = any
      verbs: [get, list, watch]
    deny:
      verbs: [exec]
```

### Axes

- **`contexts`** — required regex matched against the active kube context name.
- **`namespaces`** — optional. **Acts as a narrowing filter:**
  - Omitted ⇒ rule matches **all** resources (namespaced in any namespace, plus
    cluster-scoped resources).
  - Present ⇒ rule matches **only** namespaced resources whose namespace
    matches. Cluster-scoped resources (nodes, PVs, namespaces, ClusterRoles,
    …) **never** match a rule that specifies `namespaces`.
  - Rule of thumb: *to govern cluster-scoped resources, write a rule with no
    `namespaces`.*
- **`resources`** — resource plural name (`pods`) or `group/version/kind`;
  `"*"` matches any.
- **`verbs`** — from the known set. v1 tools emit: `get, list, create, update,
  delete, logs, exec, scale`. `watch` and `patch` are **accepted in policy but
  reserved** — no v1 tool emits them, so a rule mentioning them is valid (and
  validates) but currently never matches. They become live if/when a streaming
  or patch tool is added, so writing the idiomatic `[get, list, watch]` is safe
  and forward-compatible.

### Evaluation (deny-wins, default-deny)

1. Collect every rule whose `contexts` regex matches the active context **and**
   whose `namespaces` matches (omitted = matches all; if a rule specifies
   `namespaces` and the resource is cluster-scoped, the rule does not apply).
2. If any matched rule's `deny` covers `(resource, verb)` → **DENY**.
3. Else if any matched rule's `allow` covers `(resource, verb)` → **ALLOW**.
4. Else → **DENY** (default deny).

There are two distinct "no" outcomes, and the audit reason distinguishes them:
- *explicitly denied* (a `deny` rule fired) — the agent should not retry.
- *not granted* (nothing allowed it) — the operator may simply not have granted
  it.

## Architecture

A single static Go binary run as an MCP server over **stdio**. Stack: **Go +
official MCP `go-sdk` + `client-go`** (dynamic client + RESTMapper). kubeleash
is local-only by design — see [Out of scope](#out-of-scope-v1) for why there is
no HTTP/remote transport.

### Packages

```
cmd/kubeleash/main.go    — flag parsing, kubeconfig load, server wiring
internal/mcp/            — MCP tool registration + request decoding (go-sdk)
internal/kube/           — multi-context client factory, dynamic client,
                           RESTMapper, GVK scope discovery (ns vs cluster)
internal/policy/         — config load, rule matching, allow/deny eval
internal/audit/          — structured decision log (allow/deny + reason)
```

### Request flow (one tool call)

```
agent → MCP tool (k8s_list, ...)
  → decode (resource, namespace, context?, body)
  → resolve context (arg or kubeconfig current-context)
  → resolve GVK→GVR + scope via RESTMapper for that context
  → derive verb from the tool
  → policy.Evaluate(context, gvk, namespace, verb)   ←── THE GATE
       allow → execute via dynamic client for that context
       deny  → return rule-explaining error; cluster is never touched
  → audit log the decision either way
```

**Key invariant:** policy is evaluated **before any cluster call**. A denied
call performs **zero** cluster I/O.

## Tool surface

Seven generic, GVK-agnostic tools, each pinned to one policy verb. Works for any
resource including CRDs (dynamic client + RESTMapper).

| Tool           | Verb(s)                      | Notes                              |
|----------------|------------------------------|------------------------------------|
| `k8s_list`     | `list`                       | label/field selectors, optional ns |
| `k8s_get`      | `get`                        | single object by name              |
| `k8s_apply`    | `create` / `update`          | server-side apply; verb derived from existence (enables grant create-but-not-update) |
| `k8s_delete`   | `delete`                     |                                    |
| `k8s_logs`     | `logs`                       | pods subresource                   |
| `k8s_exec`     | `exec`                       | guarded hard; most operators `deny`|
| `k8s_scale`    | `update` (scale subresource) |                                    |

Common args for every tool: `{ resource, apiVersion?, name?, namespace?,
context? }`. `resource` accepts a plural name (`pods`) or `group/version/kind`.
GVK→GVR and scope are resolved per-context via the RESTMapper.

### Context model

Per-call, defaulting to current. Each tool takes an optional `context` arg; if
omitted, the kubeconfig `current-context` is used. Stateless — no hidden session
state. The agent can hit prod and dev in one session; each call is evaluated
against its resolved context.

### Deny verbosity

Denied calls **explain the rule** (e.g. "denied by deny rule for context
`.*prod.*`, verb `exec`"). This lets the LLM self-correct and avoid blind
retries. The same reason is written to the audit log.

### Capabilities tool

`k8s_capabilities` — lets the agent ask "what am I allowed to do in context X?"
up front, reducing blind denied calls. Low cost; included.

## Config validation & distribution

- **Startup validation (fail fast):** every `contexts` regex compiles, verbs
  are from the known set, no empty rules. A broken/unparseable policy must
  **refuse to start**, never fail open (default-deny makes a silent parse
  failure especially dangerous).
- **No hot-reload in v1** (YAGNI) — restart to change policy.
- **Distribution:** `go install`, a `ko`/Docker image, a Homebrew formula.
- **Operator ergonomics:** `--print-effective-policy` dumps resolved rules;
  `--dry-run` logs decisions without executing.

## Testing strategy

- **Policy engine — pure unit tests (crown jewel).** Table-driven over
  `(context, gvk, scope, namespace, verb) → expect allow/deny/reason`.
  Exhaustive: deny-wins, default-deny, namespace-filter-vs-cluster-scoped, the
  two distinct deny reasons.
- **Kube layer — envtest.** Real RESTMapper/discovery against a control plane;
  assert scope detection and dynamic CRUD for both built-ins and a sample CRD.
- **MCP layer — in-memory transport.** go-sdk client/server in-memory pipe;
  assert tool registration, arg decoding, and the security invariant: a denied
  call returns the rule-explaining error and makes **zero** cluster calls
  (a fake dynamic client fails the test if invoked).

## Out of scope (v1)

- Hot-reload of policy.
- Deny/allow precedence beyond simple deny-wins.
- A `scope:` field (the namespace-as-narrowing-filter rule covers cluster vs
  namespaced without it).
- Per-resource bespoke tools (generic dynamic CRUD covers everything).
- **HTTP/SSE transport and any remote/hosted mode.** Out of scope entirely, not
  merely deferred: a remote, add-by-URL server would route your cluster
  credentials through a third party, contradicting kubeleash's whole premise.
  kubeleash runs locally over stdio against your own kubeconfig.
- Multi-tenant namespace scoping as a first-class concern beyond the optional
  namespace axis (run a separate server per kubeconfig if needed).
