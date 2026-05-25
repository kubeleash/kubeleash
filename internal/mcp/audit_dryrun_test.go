// SPDX-License-Identifier: Apache-2.0

package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kubeleash/kubeleash/internal/audit"
	"github.com/kubeleash/kubeleash/internal/kube"
	"github.com/kubeleash/kubeleash/internal/mcp"
	"github.com/kubeleash/kubeleash/internal/policy"
)

// connectWith wires an mcp.Server with the given options and returns the client
// session.
func connectWith(t *testing.T, engine *policy.Engine, fac mcp.ClientFactory, opts ...mcp.Option) *mcpsdk.ClientSession {
	t.Helper()

	srv := mcp.New(engine, fac, opts...)

	serverT, clientT := mcpsdk.NewInMemoryTransports()

	ctx := context.Background()

	ss, err := srv.MCP().Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { _ = ss.Close() })

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test", Version: "v0"}, nil)

	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })

	return cs
}

// auditRecords parses every JSON line written to buf.
func auditRecords(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()

	var out []map[string]any

	for _, line := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}

		var m map[string]any
		if err := json.Unmarshal(line, &m); err != nil {
			t.Fatalf("audit line not JSON: %v\nline: %s", err, line)
		}

		out = append(out, m)
	}

	return out
}

// lastRecord returns the final audit record, failing if none were written.
func lastRecord(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()

	recs := auditRecords(t, buf)
	if len(recs) == 0 {
		t.Fatalf("no audit records written")
	}

	return recs[len(recs)-1]
}

// ---------------------------------------------------------------------------
// Dry-run: allowed path does ZERO cluster I/O
// ---------------------------------------------------------------------------

func TestDryRun_AllowedGet_ZeroIO(t *testing.T) {
	t.Parallel()

	res := policy.Resource{Version: "v1", Kind: "Pod", Plural: "pods"}
	// allowGet stays FALSE: dry-run must not reach the cluster even though
	// allowed; if Get runs the fake fails the test.
	fc := &fakeClient{t: t, res: res, scope: kube.ScopeNamespaced}

	var buf bytes.Buffer
	lg := audit.New(&buf, slog.LevelInfo)

	cs := connectWith(t, mustEngine(t, allowReadProd), &fakeFactory{client: fc},
		mcp.WithAudit(lg), mcp.WithDryRun(true))

	out := call(t, cs, "k8s_get", map[string]any{"resource": "pods", "name": "p1", "namespace": "default"})
	if out.IsError {
		t.Fatalf("dry-run get should succeed: %s", resultText(t, out))
	}

	if fc.getN != 0 {
		t.Errorf("dry-run must not call Get; getN=%d", fc.getN)
	}

	text := resultText(t, out)
	if !strings.Contains(text, "dry-run") || !strings.Contains(text, "get") {
		t.Errorf("dry-run result should describe the would-do action, got %q", text)
	}

	rec := lastRecord(t, &buf)
	if rec["dry_run"] != true {
		t.Errorf("audit dry_run = %v, want true", rec["dry_run"])
	}

	if rec["outcome"] != "allowed" {
		t.Errorf("audit outcome = %v, want allowed", rec["outcome"])
	}

	if rec["verb"] != "get" {
		t.Errorf("audit verb = %v, want get", rec["verb"])
	}
}

func TestDryRun_AllowedList_ZeroIO(t *testing.T) {
	t.Parallel()

	res := policy.Resource{Version: "v1", Kind: "Pod", Plural: "pods"}
	fc := &fakeClient{t: t, res: res, scope: kube.ScopeNamespaced}

	var buf bytes.Buffer
	lg := audit.New(&buf, slog.LevelInfo)

	cs := connectWith(t, mustEngine(t, allowReadProd), &fakeFactory{client: fc},
		mcp.WithAudit(lg), mcp.WithDryRun(true))

	out := call(t, cs, "k8s_list", map[string]any{"resource": "pods", "namespace": "default"})
	if out.IsError {
		t.Fatalf("dry-run list should succeed: %s", resultText(t, out))
	}

	if fc.listN != 0 {
		t.Errorf("dry-run must not call List; listN=%d", fc.listN)
	}

	if !strings.Contains(resultText(t, out), "dry-run") {
		t.Errorf("expected dry-run text, got %q", resultText(t, out))
	}
}

func TestDryRun_AllowedDelete_ZeroIO(t *testing.T) {
	t.Parallel()

	const cfg = `
policies:
  - contexts: ".*"
    allow:
      resources: ["pods"]
      verbs: [delete]
`
	res := policy.Resource{Version: "v1", Kind: "Pod", Plural: "pods"}
	fc := &fakeClient{t: t, res: res, scope: kube.ScopeNamespaced}

	var buf bytes.Buffer
	lg := audit.New(&buf, slog.LevelInfo)

	cs := connectWith(t, mustEngine(t, cfg), &fakeFactory{client: fc},
		mcp.WithAudit(lg), mcp.WithDryRun(true))

	out := call(t, cs, "k8s_delete", map[string]any{"resource": "pods", "name": "p1", "namespace": "default"})
	if out.IsError {
		t.Fatalf("dry-run delete should succeed: %s", resultText(t, out))
	}

	if fc.deleteN != 0 {
		t.Errorf("dry-run must not call Delete; deleteN=%d", fc.deleteN)
	}

	if !strings.Contains(resultText(t, out), "dry-run") {
		t.Errorf("expected dry-run text, got %q", resultText(t, out))
	}
}

// TestDryRun_Apply_NoExistenceGet asserts dry-run apply does NOT probe existence
// (the existence Get is itself cluster I/O) and reports a create-or-update would.
func TestDryRun_Apply_NoExistenceGet(t *testing.T) {
	t.Parallel()

	const cfg = `
policies:
  - contexts: ".*"
    allow:
      resources: ["*"]
      verbs: [create, update]
`
	res := policy.Resource{Version: "v1", Kind: "ConfigMap", Plural: "configmaps"}
	// no allow* set: any Get or Apply fails the test.
	fc := &fakeClient{t: t, res: res, scope: kube.ScopeNamespaced}

	var buf bytes.Buffer
	lg := audit.New(&buf, slog.LevelInfo)

	cs := connectWith(t, mustEngine(t, cfg), &fakeFactory{client: fc},
		mcp.WithAudit(lg), mcp.WithDryRun(true))

	out := call(t, cs, "k8s_apply", map[string]any{
		"resource":  "configmaps",
		"namespace": "default",
		"manifest": map[string]any{
			"apiVersion": "v1", "kind": "ConfigMap",
			"metadata": map[string]any{"name": "cm1"},
		},
	})
	if out.IsError {
		t.Fatalf("dry-run apply should succeed: %s", resultText(t, out))
	}

	if fc.getN != 0 {
		t.Errorf("dry-run apply must NOT probe existence; getN=%d", fc.getN)
	}

	if fc.applyN != 0 {
		t.Errorf("dry-run apply must NOT apply; applyN=%d", fc.applyN)
	}

	if !strings.Contains(resultText(t, out), "dry-run") {
		t.Errorf("expected dry-run text, got %q", resultText(t, out))
	}

	rec := lastRecord(t, &buf)
	if rec["dry_run"] != true || rec["outcome"] != "allowed" {
		t.Errorf("apply dry-run audit: dry_run=%v outcome=%v", rec["dry_run"], rec["outcome"])
	}
}

// ---------------------------------------------------------------------------
// Dry-run must NOT relax the gate: a denied call is still denied, zero I/O.
// ---------------------------------------------------------------------------

func TestDryRun_Denied_StillDeniedZeroIO(t *testing.T) {
	t.Parallel()

	// only read granted; delete is not granted.
	fc := &fakeClient{t: t, res: policy.Resource{Version: "v1", Kind: "Pod", Plural: "pods"}, scope: kube.ScopeNamespaced}

	var buf bytes.Buffer
	lg := audit.New(&buf, slog.LevelInfo)

	cs := connectWith(t, mustEngine(t, allowReadProd), &fakeFactory{client: fc},
		mcp.WithAudit(lg), mcp.WithDryRun(true))

	out := call(t, cs, "k8s_delete", map[string]any{"resource": "pods", "name": "p1", "namespace": "default"})

	if !out.IsError {
		t.Fatalf("denied delete must still error in dry-run")
	}

	if !strings.Contains(resultText(t, out), "not granted") {
		t.Errorf("expected not-granted reason, got %q", resultText(t, out))
	}

	if fc.getN+fc.listN+fc.applyN+fc.deleteN != 0 {
		t.Errorf("denied dry-run did cluster I/O")
	}

	rec := lastRecord(t, &buf)
	if rec["outcome"] != "not_granted" {
		t.Errorf("audit outcome = %v, want not_granted", rec["outcome"])
	}

	if rec["dry_run"] != true {
		t.Errorf("audit dry_run = %v, want true (reflects mode)", rec["dry_run"])
	}
}

// ---------------------------------------------------------------------------
// Audit on normal (non-dry-run) allowed and denied calls.
// ---------------------------------------------------------------------------

func TestAudit_NormalAllowed(t *testing.T) {
	t.Parallel()

	res := policy.Resource{Version: "v1", Kind: "Pod", Plural: "pods"}
	fc := &fakeClient{t: t, res: res, scope: kube.ScopeNamespaced, allowGet: true}

	var buf bytes.Buffer
	lg := audit.New(&buf, slog.LevelInfo)

	cs := connectWith(t, mustEngine(t, allowReadProd), &fakeFactory{client: fc}, mcp.WithAudit(lg))

	out := call(t, cs, "k8s_get", map[string]any{"resource": "pods", "name": "p1", "namespace": "default"})
	if out.IsError {
		t.Fatalf("allowed get errored: %s", resultText(t, out))
	}

	if fc.getN != 1 {
		t.Errorf("expected real Get in non-dry-run, getN=%d", fc.getN)
	}

	rec := lastRecord(t, &buf)
	if rec["outcome"] != "allowed" {
		t.Errorf("outcome = %v, want allowed", rec["outcome"])
	}

	if rec["dry_run"] != false {
		t.Errorf("dry_run = %v, want false", rec["dry_run"])
	}

	if rec["verb"] != "get" {
		t.Errorf("verb = %v, want get", rec["verb"])
	}

	if rec["namespace"] != "default" {
		t.Errorf("namespace = %v, want default", rec["namespace"])
	}

	if !strings.Contains(rec["reason"].(string), "allowed by rule") {
		t.Errorf("reason = %v, want allowed-by-rule", rec["reason"])
	}
}

func TestAudit_NormalDenied(t *testing.T) {
	t.Parallel()

	const cfg = `
policies:
  - contexts: ".*"
    allow:
      resources: ["*"]
      verbs: [get, list, delete]
    deny:
      resources: ["*"]
      verbs: [delete]
`
	fc := &fakeClient{t: t, res: policy.Resource{Version: "v1", Kind: "Pod", Plural: "pods"}, scope: kube.ScopeNamespaced}

	var buf bytes.Buffer
	lg := audit.New(&buf, slog.LevelInfo)

	cs := connectWith(t, mustEngine(t, cfg), &fakeFactory{client: fc}, mcp.WithAudit(lg))

	out := call(t, cs, "k8s_delete", map[string]any{"resource": "pods", "name": "p1", "namespace": "default"})
	if !out.IsError {
		t.Fatalf("expected explicit-deny error")
	}

	rec := lastRecord(t, &buf)
	if rec["outcome"] != "explicit_deny" {
		t.Errorf("outcome = %v, want explicit_deny", rec["outcome"])
	}

	if rec["dry_run"] != false {
		t.Errorf("dry_run = %v, want false", rec["dry_run"])
	}

	if !strings.Contains(rec["reason"].(string), "denied by deny rule") {
		t.Errorf("reason = %v, want denied-by-deny-rule", rec["reason"])
	}
}
