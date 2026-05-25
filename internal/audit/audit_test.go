// SPDX-License-Identifier: Apache-2.0

package audit_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/kubeleash/kubeleash/internal/audit"
	"github.com/kubeleash/kubeleash/internal/policy"
)

// decode parses the single JSON log line in buf into a generic map.
func decode(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()

	line := bytes.TrimSpace(buf.Bytes())
	if len(line) == 0 {
		t.Fatalf("no audit line written")
	}

	if bytes.Count(line, []byte("\n")) != 0 {
		t.Fatalf("expected exactly one audit line, got:\n%s", line)
	}

	var m map[string]any
	if err := json.Unmarshal(line, &m); err != nil {
		t.Fatalf("audit line is not valid JSON: %v\nline: %s", err, line)
	}

	return m
}

func TestRecord_Fields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		decision    policy.Decision
		dryRun      bool
		namespace   string
		wantOutcome string
		wantReason  string
	}{
		{
			name:        "allowed",
			decision:    policy.Decision{Outcome: policy.Allowed, Reason: "allowed by rule for context \"prod\""},
			namespace:   "team-a",
			wantOutcome: "allowed",
			wantReason:  "allowed by rule for context \"prod\"",
		},
		{
			name:        "explicit deny",
			decision:    policy.Decision{Outcome: policy.ExplicitDeny, Reason: "denied by deny rule"},
			namespace:   "team-a",
			wantOutcome: "explicit_deny",
			wantReason:  "denied by deny rule",
		},
		{
			name:        "not granted",
			decision:    policy.Decision{Outcome: policy.NotGranted, Reason: "not granted: nope"},
			namespace:   "team-a",
			wantOutcome: "not_granted",
			wantReason:  "not granted: nope",
		},
		{
			name:        "dry run true",
			decision:    policy.Decision{Outcome: policy.Allowed, Reason: "ok"},
			dryRun:      true,
			namespace:   "team-a",
			wantOutcome: "allowed",
			wantReason:  "ok",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			lg := audit.New(&buf, slog.LevelInfo)

			lg.Record(audit.Record{
				Context:   "prod",
				Resource:  policy.Resource{Group: "apps", Version: "v1", Kind: "Deployment", Plural: "deployments"},
				Namespace: tc.namespace,
				Verb:      policy.VerbGet,
				Decision:  tc.decision,
				DryRun:    tc.dryRun,
			})

			m := decode(t, &buf)

			if _, ok := m["time"]; !ok {
				t.Errorf("missing time field")
			}

			if m["context"] != "prod" {
				t.Errorf("context = %v, want prod", m["context"])
			}

			if m["verb"] != "get" {
				t.Errorf("verb = %v, want get", m["verb"])
			}

			if m["outcome"] != tc.wantOutcome {
				t.Errorf("outcome = %v, want %v", m["outcome"], tc.wantOutcome)
			}

			if m["reason"] != tc.wantReason {
				t.Errorf("reason = %v, want %v", m["reason"], tc.wantReason)
			}

			if m["dry_run"] != tc.dryRun {
				t.Errorf("dry_run = %v, want %v", m["dry_run"], tc.dryRun)
			}

			if m["namespace"] != "team-a" {
				t.Errorf("namespace = %v, want team-a", m["namespace"])
			}

			res, ok := m["resource"].(string)
			if !ok || res == "" {
				t.Errorf("resource = %v, want non-empty string", m["resource"])
			}
		})
	}
}

// TestRecord_NamespaceOmittedWhenEmpty asserts cluster-scoped (empty namespace)
// records omit the namespace key entirely.
func TestRecord_NamespaceOmittedWhenEmpty(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	lg := audit.New(&buf, slog.LevelInfo)

	lg.Record(audit.Record{
		Context:  "prod",
		Resource: policy.Resource{Version: "v1", Kind: "Node", Plural: "nodes"},
		Verb:     policy.VerbList,
		Decision: policy.Decision{Outcome: policy.Allowed, Reason: "ok"},
	})

	m := decode(t, &buf)

	if _, ok := m["namespace"]; ok {
		t.Errorf("namespace should be omitted for empty/cluster-scoped, got %v", m["namespace"])
	}
}

// TestRecord_ResourceReadableForm asserts the resource renders the GVK plus
// plural so the log is human-readable.
func TestRecord_ResourceReadableForm(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	lg := audit.New(&buf, slog.LevelInfo)

	lg.Record(audit.Record{
		Context:  "prod",
		Resource: policy.Resource{Group: "apps", Version: "v1", Kind: "Deployment", Plural: "deployments"},
		Verb:     policy.VerbGet,
		Decision: policy.Decision{Outcome: policy.Allowed, Reason: "ok"},
	})

	m := decode(t, &buf)

	res, _ := m["resource"].(string)
	if res != "apps/v1/Deployment (deployments)" {
		t.Errorf("resource = %q, want %q", res, "apps/v1/Deployment (deployments)")
	}
}

// TestRecord_CoreGroupResource asserts the core group (empty group) renders
// without a leading slash artifact going wrong.
func TestRecord_CoreGroupResource(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	lg := audit.New(&buf, slog.LevelInfo)

	lg.Record(audit.Record{
		Context:  "prod",
		Resource: policy.Resource{Version: "v1", Kind: "Pod", Plural: "pods"},
		Verb:     policy.VerbGet,
		Decision: policy.Decision{Outcome: policy.Allowed, Reason: "ok"},
	})

	m := decode(t, &buf)

	res, _ := m["resource"].(string)
	if res != "v1/Pod (pods)" {
		t.Errorf("resource = %q, want %q", res, "v1/Pod (pods)")
	}
}

// TestNew_NilWriterDefaultsStderr asserts a nil writer is accepted (defaults to
// stderr) without panicking. We cannot easily capture stderr here, so we only
// assert construction and Record do not panic.
func TestNew_NilWriterDefaultsStderr(t *testing.T) {
	t.Parallel()

	lg := audit.New(nil, slog.LevelInfo)
	lg.Record(audit.Record{
		Context:  "prod",
		Resource: policy.Resource{Version: "v1", Kind: "Pod", Plural: "pods"},
		Verb:     policy.VerbGet,
		Decision: policy.Decision{Outcome: policy.Allowed, Reason: "ok"},
	})
}

// TestLevelFiltering asserts that raising the level above info suppresses
// records (they are emitted at info).
func TestLevelFiltering(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	lg := audit.New(&buf, slog.LevelWarn)

	lg.Record(audit.Record{
		Context:  "prod",
		Resource: policy.Resource{Version: "v1", Kind: "Pod", Plural: "pods"},
		Verb:     policy.VerbGet,
		Decision: policy.Decision{Outcome: policy.Allowed, Reason: "ok"},
	})

	if buf.Len() != 0 {
		t.Errorf("expected no output at level warn, got: %s", buf.String())
	}
}

// TestNilLogger asserts a nil *Logger is safe to call (no-op), so call sites can
// hold an optional logger.
func TestNilLogger(t *testing.T) {
	t.Parallel()

	var lg *audit.Logger
	lg.Record(audit.Record{}) // must not panic
}
