// SPDX-License-Identifier: Apache-2.0

// Package kube is kubeleash's Kubernetes access layer. It sits between the MCP
// tools and a real cluster: it loads kubeconfigs, builds a per-context dynamic
// client and RESTMapper, resolves a user-supplied resource reference into a
// fully-qualified [policy.Resource] plus its scope (namespaced vs
// cluster-scoped), and performs the dynamic CRUD operations the MCP tools need.
//
// The package deliberately exposes a narrow [Client] interface so the MCP layer
// above it can be unit-tested with a fake that records calls and never touches
// a cluster. Scope discovery is security-critical: the policy engine's
// namespace-narrowing axis depends on [Request.ClusterScoped] being correct, so
// any ambiguity in resolution is reported as an error rather than guessed.
//
// Logs and Scale reach subresources the dynamic client cannot: Logs uses a
// typed clientset (the log subresource), Scale merge-patches the scale
// subresource. Exec streams a one-shot command via client-go's SPDY
// remotecommand executor, capturing stdout/stderr (each capped) and the exit
// code.
package kube

import (
	"context"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/kubeleash/kubeleash/internal/policy"
)

// Scope describes whether a resource is namespaced or cluster-scoped.
//
// The zero value [ScopeUnknown] is intentionally not a valid resolved scope:
// resolution never returns it on success, and downstream code must treat an
// unknown scope as a failure rather than defaulting to either axis. This keeps
// the layer fail-safe — a dropped or uninitialised Scope can never be silently
// interpreted as "namespaced" (which would let namespace-scoped allow rules
// match) nor "cluster-scoped".
type Scope int

const (
	// ScopeUnknown is the zero value and is never returned on a successful
	// resolution. It exists so an uninitialised Scope is obviously invalid.
	ScopeUnknown Scope = iota
	// ScopeNamespaced means the resource lives inside a namespace.
	ScopeNamespaced
	// ScopeClusterScoped means the resource is cluster-scoped (no namespace).
	ScopeClusterScoped
)

// ClusterScoped reports whether the scope is cluster-scoped. It maps directly
// onto [policy.Request.ClusterScoped]. ScopeUnknown reports false, but callers
// must never reach this with an unknown scope — Resolve errors instead of
// returning ScopeUnknown.
func (s Scope) ClusterScoped() bool {
	return s == ScopeClusterScoped
}

// String implements fmt.Stringer.
func (s Scope) String() string {
	switch s {
	case ScopeNamespaced:
		return "namespaced"
	case ScopeClusterScoped:
		return "cluster-scoped"
	case ScopeUnknown:
		return "unknown"
	default:
		return "unknown"
	}
}

// ExecOptions bounds a one-shot, non-interactive command exec. There is no TTY
// and no stdin; following/streaming back-and-forth is intentionally unsupported.
type ExecOptions struct {
	Container string   // concrete container; the caller resolves it
	Command   []string // argv; must be non-empty
	MaxBytes  int64    // hard cap applied to EACH of stdout/stderr captured
}

// ExecResult is the outcome of a command that actually ran. A non-zero ExitCode
// is normal data, not an error.
type ExecResult struct {
	Stdout          string
	Stderr          string
	ExitCode        int
	StdoutTruncated bool
	StderrTruncated bool
}

// LogsOptions bounds a one-shot pod log read. Following/streaming is
// intentionally unsupported (MCP is request/response).
type LogsOptions struct {
	Container    string
	TailLines    *int64
	Previous     bool
	SinceSeconds *int64
	Timestamps   bool
	LimitBytes   *int64
}

// Client is the kube-layer surface the MCP layer depends on. One Client is
// scoped to exactly one kube context. Implementations must perform zero cluster
// I/O until a method that needs the cluster is called, so a denied request
// (gated above this layer) never reaches the cluster.
type Client interface {
	// Context returns the resolved kube context name this client is scoped to
	// (the kubeconfig current-context when the caller asked for ""). Callers
	// must use this for the policy decision so an omitted context evaluates
	// against the real context name, not the empty string.
	Context() string

	// Resolve turns a user resource reference into a fully-populated
	// policy.Resource plus its Scope, using this context's RESTMapper and
	// discovery. The reference is either a plural ("pods") or a
	// "group/version/kind" string ("apps/v1/Deployment"; core group is empty,
	// e.g. "/v1/Pod" or just "Pod" via the plural form). A "*" wildcard is a
	// policy concept and is rejected here.
	Resolve(ctx context.Context, resourceRef string) (policy.Resource, Scope, error)

	// Get fetches a single object. namespace must be "" for cluster-scoped
	// resources.
	Get(ctx context.Context, res policy.Resource, namespace, name string) (*unstructured.Unstructured, error)

	// List lists objects. namespace must be "" for cluster-scoped resources or
	// to list across all namespaces.
	List(ctx context.Context, res policy.Resource, namespace string) (*unstructured.UnstructuredList, error)

	// Apply performs a server-side apply of obj. namespace must be "" for
	// cluster-scoped resources.
	Apply(ctx context.Context, res policy.Resource, namespace string, obj *unstructured.Unstructured) (*unstructured.Unstructured, error)

	// Delete removes a single object. namespace must be "" for cluster-scoped
	// resources.
	Delete(ctx context.Context, res policy.Resource, namespace, name string) error

	// Scale sets the desired replica count via the resource's scale subresource.
	// res must have a scale subresource (Deployment, StatefulSet, ReplicaSet, RC,
	// or a scalable CRD); otherwise the API returns a clear error.
	Scale(ctx context.Context, res policy.Resource, namespace, name string, replicas int32) error

	// Logs reads a pod's logs (one-shot, bounded by opts).
	Logs(ctx context.Context, namespace, name string, opts LogsOptions) (string, error)

	// Exec runs Command in the named pod/container (one-shot, no TTY, no stdin),
	// capturing stdout/stderr (each capped at opts.MaxBytes) and the command's
	// exit code. A non-zero exit is returned as ExecResult.ExitCode with a nil
	// error; only transport/timeout/auth failures return an error.
	Exec(ctx context.Context, namespace, name string, opts ExecOptions) (ExecResult, error)
}
