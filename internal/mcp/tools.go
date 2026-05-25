// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kubeleash/kubeleash/internal/kube"
	"github.com/kubeleash/kubeleash/internal/policy"
)

// ---------------------------------------------------------------------------
// Tool argument types (typed input structs; the SDK infers their JSON schema)
// ---------------------------------------------------------------------------

// resourceArgs are the common arguments shared by every resource-targeting
// tool. resource is a plural ("pods") or a "group/version/kind" string. context
// defaults to the kubeconfig current-context when empty.
type resourceArgs struct {
	Resource  string `json:"resource" jsonschema:"resource as a plural name (\"pods\") or group/version/kind (\"apps/v1/Deployment\")"`
	Name      string `json:"name,omitempty" jsonschema:"object name (required for get/delete/logs/exec/scale)"`
	Namespace string `json:"namespace,omitempty" jsonschema:"namespace; omit for cluster-scoped resources or to span all namespaces"`
	Context   string `json:"context,omitempty" jsonschema:"kube context name; defaults to the kubeconfig current-context"`
}

type listArgs struct {
	resourceArgs
	LabelSelector string `json:"labelSelector,omitempty" jsonschema:"label selector to filter the list"`
	FieldSelector string `json:"fieldSelector,omitempty" jsonschema:"field selector to filter the list"`
}

type applyArgs struct {
	resourceArgs
	Manifest map[string]any `json:"manifest" jsonschema:"the object manifest to server-side apply"`
}

type logsArgs struct {
	resourceArgs
	Container string `json:"container,omitempty" jsonschema:"container name within the pod"`
}

type execArgs struct {
	resourceArgs
	Container string   `json:"container,omitempty" jsonschema:"container name within the pod"`
	Command   []string `json:"command" jsonschema:"command and arguments to execute"`
}

type scaleArgs struct {
	resourceArgs
	Replicas int32 `json:"replicas" jsonschema:"desired replica count"`
}

type capabilitiesArgs struct {
	Context string `json:"context,omitempty" jsonschema:"kube context name; defaults to the kubeconfig current-context"`
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// registerTools registers all kubeleash tools on the server. Each resource tool
// is pinned to a single policy verb (apply is special — see applyHandler).
func (s *Server) registerTools() {
	mcp.AddTool(s.srv, &mcp.Tool{
		Name:        "k8s_list",
		Description: "List Kubernetes objects of a resource type, optionally filtered by namespace and selectors.",
	}, s.listHandler)

	mcp.AddTool(s.srv, &mcp.Tool{
		Name:        "k8s_get",
		Description: "Get a single Kubernetes object by name.",
	}, s.getHandler)

	mcp.AddTool(s.srv, &mcp.Tool{
		Name:        "k8s_apply",
		Description: "Server-side apply a Kubernetes manifest (create or update depending on existence).",
	}, s.applyHandler)

	mcp.AddTool(s.srv, &mcp.Tool{
		Name:        "k8s_delete",
		Description: "Delete a single Kubernetes object by name.",
	}, s.deleteHandler)

	mcp.AddTool(s.srv, &mcp.Tool{
		Name:        "k8s_logs",
		Description: "Read logs from a pod.",
	}, s.logsHandler)

	mcp.AddTool(s.srv, &mcp.Tool{
		Name:        "k8s_exec",
		Description: "Execute a command in a pod container.",
	}, s.execHandler)

	mcp.AddTool(s.srv, &mcp.Tool{
		Name:        "k8s_scale",
		Description: "Scale a workload to a desired replica count.",
	}, s.scaleHandler)

	mcp.AddTool(s.srv, &mcp.Tool{
		Name:        "k8s_capabilities",
		Description: "Report which actions the policy allows in a given kube context. Does not contact the cluster.",
	}, s.capabilitiesHandler)
}

// ---------------------------------------------------------------------------
// The gate — the single security choke-point
// ---------------------------------------------------------------------------

// gateResult carries the resolved, policy-approved values a handler needs to
// execute. It is only ever returned once the decision is Allowed.
type gateResult struct {
	client    kube.Client
	resource  policy.Resource
	scope     kube.Scope
	namespace string
}

// gate is THE policy choke-point. It resolves the context's client, resolves
// the resource reference via discovery (the only pre-gate cluster touch),
// evaluates the policy for verb, and returns a gateResult ONLY if the decision
// is Allowed. On any deny or error it returns an error and the caller MUST NOT
// perform cluster I/O. A future audit hook belongs here (one place, both
// outcomes).
//
// INVARIANT: no caller reaches Get/List/Apply/Delete unless gate returns nil
// error.
func (s *Server) gate(ctx context.Context, args resourceArgs, verb policy.Verb) (gateResult, error) {
	c, err := s.factory.Client(args.Context)
	if err != nil {
		return gateResult{}, fmt.Errorf("mcp: resolve context %q: %w", args.Context, err)
	}

	res, scope, err := c.Resolve(ctx, args.Resource)
	if err != nil {
		return gateResult{}, fmt.Errorf("mcp: resolve resource %q: %w", args.Resource, err)
	}

	decision := s.evaluate(args, res, scope, verb)
	if !decision.Allowed() {
		return gateResult{}, fmt.Errorf("mcp: %s", decision.Reason)
	}

	return gateResult{
		client:    c,
		resource:  res,
		scope:     scope,
		namespace: namespaceFor(scope, args.Namespace),
	}, nil
}

// evaluate builds the policy.Request and evaluates it. Factored out so the apply
// dual-verb logic can reuse it without re-resolving.
func (s *Server) evaluate(args resourceArgs, res policy.Resource, scope kube.Scope, verb policy.Verb) policy.Decision {
	return s.engine.Evaluate(policy.Request{
		Context:       args.Context,
		Resource:      res,
		Namespace:     namespaceFor(scope, args.Namespace),
		ClusterScoped: scope.ClusterScoped(),
		Verb:          verb,
	})
}

// namespaceFor forces an empty namespace for cluster-scoped resources so the
// kube layer and policy engine never see a stray namespace on a cluster object.
func namespaceFor(scope kube.Scope, ns string) string {
	if scope.ClusterScoped() {
		return ""
	}

	return ns
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

func (s *Server) listHandler(ctx context.Context, _ *mcp.CallToolRequest, args listArgs) (*mcp.CallToolResult, any, error) {
	g, err := s.gate(ctx, args.resourceArgs, policy.VerbList)
	if err != nil {
		return nil, nil, err
	}

	list, err := g.client.List(ctx, g.resource, g.namespace)
	if err != nil {
		return nil, nil, fmt.Errorf("mcp: list: %w", err)
	}

	return jsonResult(list.Object), nil, nil
}

func (s *Server) getHandler(ctx context.Context, _ *mcp.CallToolRequest, args resourceArgs) (*mcp.CallToolResult, any, error) {
	if args.Name == "" {
		return nil, nil, fmt.Errorf("mcp: get: name is required")
	}

	g, err := s.gate(ctx, args, policy.VerbGet)
	if err != nil {
		return nil, nil, err
	}

	obj, err := g.client.Get(ctx, g.resource, g.namespace, args.Name)
	if err != nil {
		return nil, nil, fmt.Errorf("mcp: get: %w", err)
	}

	return jsonResult(obj.Object), nil, nil
}

// applyHandler implements the existence-ordering APPLY RULE from the task spec.
//
//  1. Resolve the resource (scope) via discovery.
//  2. Evaluate BOTH create and update. If neither is allowed, deny immediately
//     with ZERO cluster I/O — no existence Get is performed.
//  3. Only if at least one of create/update is permittable, do the existence
//     check, pick the concrete verb (exists -> update, absent -> create),
//     evaluate THAT specific verb, and apply (or deny if that specific verb is
//     the denied one — e.g. create allowed but update denied and the object
//     already exists -> deny update).
//
// This preserves "fully-denied => zero I/O" while still enabling the
// grant-create-but-not-update policy shape.
func (s *Server) applyHandler(ctx context.Context, _ *mcp.CallToolRequest, args applyArgs) (*mcp.CallToolResult, any, error) {
	if len(args.Manifest) == 0 {
		return nil, nil, fmt.Errorf("mcp: apply: manifest is required")
	}

	c, err := s.factory.Client(args.Context)
	if err != nil {
		return nil, nil, fmt.Errorf("mcp: resolve context %q: %w", args.Context, err)
	}

	res, scope, err := c.Resolve(ctx, args.Resource)
	if err != nil {
		return nil, nil, fmt.Errorf("mcp: resolve resource %q: %w", args.Resource, err)
	}

	createDecision := s.evaluate(args.resourceArgs, res, scope, policy.VerbCreate)
	updateDecision := s.evaluate(args.resourceArgs, res, scope, policy.VerbUpdate)

	// Step 2: fully denied => zero cluster I/O (no existence Get).
	if !createDecision.Allowed() && !updateDecision.Allowed() {
		// Surface the more actionable reason: prefer an explicit deny over a
		// bare not-granted so the agent learns it should stop retrying.
		reason := updateDecision.Reason
		if createDecision.Outcome == policy.ExplicitDeny {
			reason = createDecision.Reason
		}

		return nil, nil, fmt.Errorf("mcp: apply: %s", reason)
	}

	obj := &unstructured.Unstructured{Object: args.Manifest}

	name := obj.GetName()
	if name == "" {
		return nil, nil, fmt.Errorf("mcp: apply: manifest has no metadata.name")
	}

	ns := namespaceFor(scope, args.Namespace)

	// Step 3: existence check (cluster I/O, justified because at least one verb
	// is permittable), then concrete-verb evaluation.
	exists := true

	if _, gerr := c.Get(ctx, res, ns, name); gerr != nil {
		// Treat any Get error as "does not exist" for verb selection. A real
		// not-found yields create; other errors will resurface from Apply.
		exists = false
	}

	concrete := policy.VerbCreate
	concreteDecision := createDecision

	if exists {
		concrete = policy.VerbUpdate
		concreteDecision = updateDecision
	}

	if !concreteDecision.Allowed() {
		return nil, nil, fmt.Errorf("mcp: apply (%s): %s", concrete, concreteDecision.Reason)
	}

	applied, err := c.Apply(ctx, res, ns, obj)
	if err != nil {
		return nil, nil, fmt.Errorf("mcp: apply: %w", err)
	}

	return jsonResult(applied.Object), nil, nil
}

func (s *Server) deleteHandler(ctx context.Context, _ *mcp.CallToolRequest, args resourceArgs) (*mcp.CallToolResult, any, error) {
	if args.Name == "" {
		return nil, nil, fmt.Errorf("mcp: delete: name is required")
	}

	g, err := s.gate(ctx, args, policy.VerbDelete)
	if err != nil {
		return nil, nil, err
	}

	if derr := g.client.Delete(ctx, g.resource, g.namespace, args.Name); derr != nil {
		return nil, nil, fmt.Errorf("mcp: delete: %w", derr)
	}

	return textResult(fmt.Sprintf("deleted %s %q", g.resource.Plural, args.Name)), nil, nil
}

// logsHandler, execHandler and scaleHandler run the FULL policy gate (so the
// verb wiring and the zero-I/O-on-deny invariant are exercised today) but
// return a clear "not yet implemented" error AFTER policy approval. The kube
// Client interface deliberately does not yet expose logs/exec/scale
// subresources (see internal/kube/kube.go); wiring the gate now lets execution
// land later without re-touching the security path. See report (decision (b)).

func (s *Server) logsHandler(ctx context.Context, _ *mcp.CallToolRequest, args logsArgs) (*mcp.CallToolResult, any, error) {
	if args.Name == "" {
		return nil, nil, fmt.Errorf("mcp: logs: name is required")
	}

	if _, err := s.gate(ctx, args.resourceArgs, policy.VerbLogs); err != nil {
		return nil, nil, err
	}

	return nil, nil, fmt.Errorf("mcp: logs: not yet implemented in v0.1 (policy permitted)")
}

func (s *Server) execHandler(ctx context.Context, _ *mcp.CallToolRequest, args execArgs) (*mcp.CallToolResult, any, error) {
	if args.Name == "" {
		return nil, nil, fmt.Errorf("mcp: exec: name is required")
	}

	if len(args.Command) == 0 {
		return nil, nil, fmt.Errorf("mcp: exec: command is required")
	}

	if _, err := s.gate(ctx, args.resourceArgs, policy.VerbExec); err != nil {
		return nil, nil, err
	}

	return nil, nil, fmt.Errorf("mcp: exec: not yet implemented in v0.1 (policy permitted)")
}

func (s *Server) scaleHandler(ctx context.Context, _ *mcp.CallToolRequest, args scaleArgs) (*mcp.CallToolResult, any, error) {
	if args.Name == "" {
		return nil, nil, fmt.Errorf("mcp: scale: name is required")
	}

	// Scale acts on the scale subresource and is gated as an update.
	if _, err := s.gate(ctx, args.resourceArgs, policy.VerbUpdate); err != nil {
		return nil, nil, err
	}

	return nil, nil, fmt.Errorf("mcp: scale: not yet implemented in v0.1 (policy permitted)")
}

// capabilitiesHandler answers "what am I allowed to do in context X?" purely
// from the policy engine. It performs ZERO cluster calls and never touches the
// kube factory.
func (s *Server) capabilitiesHandler(_ context.Context, _ *mcp.CallToolRequest, args capabilitiesArgs) (*mcp.CallToolResult, any, error) {
	caps := s.engine.Capabilities(args.Context)

	lines := make([]string, 0, len(caps)+1)
	if len(caps) == 0 {
		lines = append(lines, fmt.Sprintf("context %q: no actions are allowed by policy", args.Context))
	} else {
		lines = append(lines, fmt.Sprintf("context %q allows:", args.Context))
		lines = append(lines, caps...)
	}

	return textResult(strings.Join(lines, "\n")), nil, nil
}
