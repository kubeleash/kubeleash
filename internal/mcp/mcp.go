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
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kubeleash/kubeleash/internal/kube"
	"github.com/kubeleash/kubeleash/internal/policy"
)

// version is the reported MCP server implementation version.
const version = "0.1.0"

// ClientFactory is the subset of [kube.Factory] the MCP layer depends on: it
// hands out a [kube.Client] for a (possibly empty) context name. *kube.Factory
// satisfies it; tests inject a fake.
type ClientFactory interface {
	Client(contextName string) (kube.Client, error)
}

// Server wires the policy engine and kube client factory into an MCP server.
// Construct it with [New], then obtain the underlying *mcp.Server via
// [Server.MCP] to connect it over a transport.
type Server struct {
	engine  *policy.Engine
	factory ClientFactory
	srv     *mcp.Server
}

// New builds a Server, registering all kubeleash tools on a fresh MCP server.
// Both engine and factory are required; passing nil for either is a programming
// error and will surface as a panic on first use rather than a silent
// fail-open.
func New(engine *policy.Engine, factory ClientFactory) *Server {
	s := &Server{
		engine:  engine,
		factory: factory,
		srv:     mcp.NewServer(&mcp.Implementation{Name: "kubeleash", Version: version}, nil),
	}

	s.registerTools()

	return s
}

// MCP returns the underlying *mcp.Server so callers can connect it over a
// transport (stdio in production, in-memory in tests).
func (s *Server) MCP() *mcp.Server {
	return s.srv
}
