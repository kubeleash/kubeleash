// SPDX-License-Identifier: Apache-2.0

// Package audit records every kubeleash policy decision as a structured JSON
// line via [log/slog]. It is intentionally small and stdlib-only.
//
// Output goes to an injected [io.Writer] (defaulting to stderr) — NEVER stdout,
// which is reserved for the MCP stdio transport. Injecting the writer lets tests
// capture records into a buffer.
//
// Records are emitted at [slog.LevelInfo] regardless of outcome: a policy denial
// is normal, expected operation (the whole point of the gate), not an error
// condition, so logging denials at error/warn would create alarm fatigue and
// muddy genuine failures. The outcome field distinguishes allowed from denied.
//
// The package imports [internal/policy] for the decision facts ([policy.Decision],
// [policy.Resource], [policy.Verb]) rather than re-deriving them, keeping the
// caller's hook a single struct literal and the outcome→string mapping in one
// authoritative place next to the policy outcomes it mirrors.
package audit

import (
	"context"
	"io"
	"log/slog"
	"os"

	"github.com/kubeleash/kubeleash/internal/policy"
)

// Stable outcome strings logged in the "outcome" field. They mirror the
// exported [policy.Outcome] values one-to-one.
const (
	outcomeAllowed      = "allowed"
	outcomeExplicitDeny = "explicit_deny"
	outcomeNotGranted   = "not_granted"
)

// Logger writes structured audit records. Construct it with [New]. A nil
// *Logger is a safe no-op so call sites may hold an optional logger.
type Logger struct {
	log *slog.Logger
}

// Record carries the facts of a single policy decision to be audited. The
// caller fills it in after [policy.Engine.Evaluate] returns, for both allowed
// and denied outcomes.
type Record struct {
	Context   string
	Resource  policy.Resource
	Namespace string // "" for cluster-scoped; omitted from the record
	Verb      policy.Verb
	Decision  policy.Decision
	DryRun    bool
}

// New builds a Logger writing JSON to w at the given level. A nil w defaults to
// stderr (never stdout — that is the MCP transport). The level lets a future
// --log-level flag tune verbosity; records are emitted at info, so a level above
// info suppresses them.
func New(w io.Writer, level slog.Level) *Logger {
	if w == nil {
		w = os.Stderr
	}

	handler := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level})

	return &Logger{log: slog.New(handler)}
}

// Record logs the decision facts as one JSON line. It is a no-op on a nil
// receiver. Records are emitted at info for every outcome (see package doc).
func (l *Logger) Record(rec Record) {
	if l == nil {
		return
	}

	attrs := []slog.Attr{
		slog.String("context", rec.Context),
		slog.String("resource", resourceString(rec.Resource)),
		slog.String("verb", string(rec.Verb)),
		slog.String("outcome", outcomeString(rec.Decision.Outcome)),
		slog.String("reason", rec.Decision.Reason),
		slog.Bool("dry_run", rec.DryRun),
	}

	// Omit namespace for cluster-scoped / unset to keep the record clean.
	if rec.Namespace != "" {
		attrs = append(attrs, slog.String("namespace", rec.Namespace))
	}

	l.log.LogAttrs(context.Background(), slog.LevelInfo, "policy_decision", attrs...)
}

// outcomeString maps a policy outcome to its stable audit string. An unknown
// value fails safe to not_granted (the default-deny zero value).
func outcomeString(o policy.Outcome) string {
	switch o {
	case policy.Allowed:
		return outcomeAllowed
	case policy.ExplicitDeny:
		return outcomeExplicitDeny
	case policy.NotGranted:
		return outcomeNotGranted
	default:
		return outcomeNotGranted
	}
}

// resourceString renders a resource in a human-readable "group/version/Kind
// (plural)" form. The core group (empty group) yields "version/Kind (plural)".
func resourceString(r policy.Resource) string {
	gvk := r.Version + "/" + r.Kind
	if r.Group != "" {
		gvk = r.Group + "/" + gvk
	}

	if r.Plural != "" {
		return gvk + " (" + r.Plural + ")"
	}

	return gvk
}
