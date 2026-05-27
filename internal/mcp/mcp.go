// SPDX-License-Identifier: Apache-2.0

// Package mcp is kubeleash's MCP tool surface and the policy gate. It composes
// the kube layer (resource resolution + dynamic CRUD) with the pure policy
// engine, registering one MCP tool per policy verb.
//
// The security keystone lives here: every resource tool runs the identical
// flow of resolve -> evaluate -> execute through a single choke-point ([gate]),
// so that a policy-denied call performs ZERO cluster I/O. The kube Resolve call
// (discovery only) is the sole cluster-touching method allowed to run before
// the gate; Get/List/Apply/Delete only ever run on an allowed decision.
//
// Both the policy engine and the kube client factory are injected via [New] so
// the whole surface is unit-testable over the SDK's in-memory transport with a
// fake kube client that fails the test if it is reached on a denied path.
//
// See docs/design.md "Tool surface", "Request flow", "Deny verbosity",
// "Capabilities tool" and "Testing strategy — MCP layer" for the spec.
package mcp

import (
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kubeleash/kubeleash/internal/audit"
	"github.com/kubeleash/kubeleash/internal/kube"
	"github.com/kubeleash/kubeleash/internal/policy"
)

// defaultVersion is the reported MCP server implementation version when none is
// injected via [WithVersion] (e.g. unit tests). Production wires in the
// build-stamped version from main so it tracks the release tag automatically.
const defaultVersion = "dev"

const (
	defaultLogTailLines int64 = 100
	defaultMaxTailLines int64 = 2000
	defaultMaxLogBytes  int64 = 256 * 1024
)

// LogLimits bounds k8s_logs output. Non-positive fields fall back to the code
// defaults; a default tail above the max is clamped down to the max.
type LogLimits struct {
	DefaultTailLines int64
	MaxTailLines     int64
	MaxBytes         int64
}

func normalizeLogLimits(l LogLimits) LogLimits {
	if l.DefaultTailLines <= 0 {
		l.DefaultTailLines = defaultLogTailLines
	}
	if l.MaxTailLines <= 0 {
		l.MaxTailLines = defaultMaxTailLines
	}
	if l.MaxBytes <= 0 {
		l.MaxBytes = defaultMaxLogBytes
	}
	if l.DefaultTailLines > l.MaxTailLines {
		l.DefaultTailLines = l.MaxTailLines
	}

	return l
}

const (
	defaultExecTimeout        = 30 * time.Second
	defaultExecMaxBytes int64 = 256 * 1024
)

// ExecLimits bounds k8s_exec. Non-positive fields fall back to the code
// defaults. These are operator ceilings; there is no per-call override.
type ExecLimits struct {
	Timeout  time.Duration
	MaxBytes int64
}

func normalizeExecLimits(l ExecLimits) ExecLimits {
	if l.Timeout <= 0 {
		l.Timeout = defaultExecTimeout
	}
	if l.MaxBytes <= 0 {
		l.MaxBytes = defaultExecMaxBytes
	}

	return l
}

// ClientFactory is the subset of [kube.Factory] the MCP layer depends on: it
// hands out a [kube.Client] for a (possibly empty) context name. *kube.Factory
// satisfies it; tests inject a fake.
type ClientFactory interface {
	Client(contextName string) (kube.Client, error)
	// ResolveContext returns the concrete context name (current-context when
	// given ""), without building a client or touching a cluster. Used by
	// k8s_capabilities so it can report on the effective context while staying
	// cluster-free.
	ResolveContext(contextName string) (string, error)
}

// Server wires the policy engine and kube client factory into an MCP server.
// Construct it with [New], then obtain the underlying *mcp.Server via
// [Server.MCP] to connect it over a transport.
type Server struct {
	engine  *policy.Engine
	factory ClientFactory
	srv     *mcp.Server

	// version is the implementation version reported in the MCP initialize
	// handshake. Defaults to defaultVersion; set via [WithVersion].
	version string

	// audit records every decision (allowed and denied). A nil *audit.Logger is
	// a safe no-op, so the zero value of Server never panics on logging.
	audit *audit.Logger
	// dryRun, when true, suppresses cluster mutation/read I/O on ALLOWED
	// decisions and returns a would-do result instead. It NEVER relaxes the
	// gate: denied calls remain denied with zero I/O.
	dryRun bool
	// logLimits controls the tail-line and byte caps applied to k8s_logs output.
	// Normalized by New via normalizeLogLimits so the zero value is safe.
	logLimits LogLimits
	// execLimits controls the timeout and byte cap applied to k8s_exec output.
	// Normalized by New via normalizeExecLimits so the zero value is safe.
	execLimits ExecLimits
}

// Option configures a [Server] at construction. Use functional options so the
// minimal New(engine, factory) call site keeps compiling as features are added.
type Option func(*Server)

// WithAudit injects the audit logger used to record every policy decision. If
// omitted (or given nil), auditing is a no-op.
func WithAudit(logger *audit.Logger) Option {
	return func(s *Server) { s.audit = logger }
}

// WithDryRun enables dry-run mode: allowed calls are logged and reported as
// would-do without touching the cluster. It does not affect denials.
func WithDryRun(dryRun bool) Option {
	return func(s *Server) { s.dryRun = dryRun }
}

// WithVersion sets the implementation version reported to MCP clients. An empty
// value is ignored, keeping the default. Production passes the build-stamped
// version so the reported version tracks the release tag.
func WithVersion(v string) Option {
	return func(s *Server) {
		if v != "" {
			s.version = v
		}
	}
}

// WithLogLimits sets the k8s_logs caps. Non-positive fields keep the default.
func WithLogLimits(l LogLimits) Option {
	return func(s *Server) { s.logLimits = l }
}

// WithExecLimits sets the k8s_exec timeout and output cap. Non-positive fields
// keep the default.
func WithExecLimits(l ExecLimits) Option {
	return func(s *Server) { s.execLimits = l }
}

// New builds a Server, registering all kubeleash tools on a fresh MCP server.
// Both engine and factory are required; passing nil for either is a programming
// error and will surface as a panic on first use rather than a silent
// fail-open. Audit and dry-run are configured via functional options; with no
// audit option, auditing is a nil-safe no-op.
func New(engine *policy.Engine, factory ClientFactory, opts ...Option) *Server {
	s := &Server{
		engine:  engine,
		factory: factory,
		version: defaultVersion,
	}

	for _, opt := range opts {
		opt(s)
	}

	s.logLimits = normalizeLogLimits(s.logLimits)
	s.execLimits = normalizeExecLimits(s.execLimits)

	// Build the server after options so the reported version reflects WithVersion.
	s.srv = mcp.NewServer(&mcp.Implementation{Name: "kubeleash", Version: s.version}, nil)

	s.registerTools()

	return s
}

// MCP returns the underlying *mcp.Server so callers can connect it over a
// transport (stdio in production, in-memory in tests).
func (s *Server) MCP() *mcp.Server {
	return s.srv
}
