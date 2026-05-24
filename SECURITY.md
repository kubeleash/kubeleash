# Security Policy

kubeleash is a security tool, so we hold its own posture to a high bar — and we
are honest about what it does and does not protect against.

## Reporting a vulnerability

**Please do not open public issues for security problems.**

Report privately via GitHub Security Advisories:
**https://github.com/kubeleash/kubeleash/security/advisories/new**

We aim to acknowledge within 72 hours and to provide a remediation timeline
after triage. Coordinated disclosure is appreciated; we will credit reporters
who wish to be credited.

Supported versions: while pre-1.0, only the latest tagged release is supported.

## What kubeleash guarantees

- **It only ever subtracts privilege.** Effective permission is always
  `kubeconfig-grants ∩ policy-allows`. kubeleash can never grant more than the
  credentials already allow — it only narrows.
- **Policy is evaluated before any cluster call.** A denied call performs zero
  cluster I/O.
- **Default deny.** A missing or unparseable policy refuses to start rather than
  failing open.

## What kubeleash does NOT do (non-goals / assumptions)

Being explicit here is part of the security model:

- **Not a replacement for Kubernetes RBAC.** It is a complementary guardrail for
  AI agents, not a cluster authorization system.
- **Prompt injection is mitigated, not eliminated.** Gating destructive verbs
  reduces blast radius, but kubeleash cannot reason about intent.
- **Policy-file integrity is assumed.** If an attacker can rewrite your policy
  file or env, they control the leash. Protect it like any other secret config.
- **The kubeconfig is trusted input.** kubeleash constrains what the agent does
  with it; it does not vet the credentials themselves.

## Privacy

kubeleash performs **no telemetry and no phone-home**. It talks only to the
Kubernetes API server(s) you point it at. This is intentional: people run it
against production clusters with privileged credentials.
