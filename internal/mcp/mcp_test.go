// SPDX-License-Identifier: Apache-2.0

package mcp_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kubeleash/kubeleash/internal/kube"
	"github.com/kubeleash/kubeleash/internal/mcp"
	"github.com/kubeleash/kubeleash/internal/policy"
)

// ---------------------------------------------------------------------------
// Fakes
// ---------------------------------------------------------------------------

// fakeClient is a kube.Client whose CRUD methods fail the test if invoked. Only
// the methods explicitly allowed by a test (recorded via the call counters)
// should ever run; Resolve is always allowed (it is pre-gate discovery).
type fakeClient struct {
	t *testing.T

	// context is the resolved context name this client reports via Context().
	// The gate must use it (not the raw tool arg) for the policy decision.
	context string

	res   policy.Resource
	scope kube.Scope

	// listItems, when set, are returned as the .Items of the List result so the
	// rendering of a non-empty list can be asserted.
	listItems []any

	resolveErr error

	// allow* gate which CRUD methods are permitted to run without failing the
	// test. The security-invariant tests leave these false so any cluster call
	// on a denied path fails the test.
	allowGet    bool
	allowList   bool
	allowApply  bool
	allowDelete bool
	allowScale  bool
	allowLogs   bool

	// getReturnsNotFound makes Get behave as "object absent" for apply existence
	// checks (a genuine apierrors NotFound).
	getReturnsNotFound bool

	// getErr, when non-nil, is returned from Get (and takes precedence over
	// getReturnsNotFound). Used to exercise non-NotFound existence-check
	// failures, which must fail closed.
	getErr error

	mu      sync.Mutex
	getN    int
	listN   int
	applyN  int
	deleteN int
	scaleN  int
	logsN   int

	gotResource  policy.Resource
	gotNamespace string
	gotName      string
	gotReplicas  int32
	gotTail      *int64
	logsOut      string
}

func (f *fakeClient) Context() string { return f.context }

func (f *fakeClient) Resolve(_ context.Context, _ string) (policy.Resource, kube.Scope, error) {
	if f.resolveErr != nil {
		return policy.Resource{}, kube.ScopeUnknown, f.resolveErr
	}

	return f.res, f.scope, nil
}

func (f *fakeClient) Get(_ context.Context, res policy.Resource, ns, name string) (*unstructured.Unstructured, error) {
	f.mu.Lock()
	f.getN++
	f.gotResource, f.gotNamespace, f.gotName = res, ns, name
	f.mu.Unlock()

	if !f.allowGet {
		f.t.Errorf("SECURITY INVARIANT VIOLATED: kube Get called on a path that must not reach the cluster")
	}

	if f.getErr != nil {
		return nil, f.getErr
	}

	if f.getReturnsNotFound {
		return nil, apierrors.NewNotFound(schema.GroupResource{Resource: res.Plural}, name)
	}

	return &unstructured.Unstructured{Object: map[string]any{"kind": res.Kind, "metadata": map[string]any{"name": name}}}, nil
}

func (f *fakeClient) List(_ context.Context, res policy.Resource, ns string) (*unstructured.UnstructuredList, error) {
	f.mu.Lock()
	f.listN++
	f.gotResource, f.gotNamespace = res, ns
	f.mu.Unlock()

	if !f.allowList {
		f.t.Errorf("SECURITY INVARIANT VIOLATED: kube List called on a denied path")
	}

	list := &unstructured.UnstructuredList{Object: map[string]any{"kind": res.Kind + "List", "apiVersion": "v1"}}
	for _, it := range f.listItems {
		if m, ok := it.(map[string]any); ok {
			list.Items = append(list.Items, unstructured.Unstructured{Object: m})
		}
	}

	return list, nil
}

func (f *fakeClient) Apply(_ context.Context, res policy.Resource, ns string, obj *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	f.mu.Lock()
	f.applyN++
	f.gotResource, f.gotNamespace = res, ns
	f.mu.Unlock()

	if !f.allowApply {
		f.t.Errorf("SECURITY INVARIANT VIOLATED: kube Apply called on a denied path")
	}

	return obj, nil
}

func (f *fakeClient) Delete(_ context.Context, res policy.Resource, ns, name string) error {
	f.mu.Lock()
	f.deleteN++
	f.gotResource, f.gotNamespace, f.gotName = res, ns, name
	f.mu.Unlock()

	if !f.allowDelete {
		f.t.Errorf("SECURITY INVARIANT VIOLATED: kube Delete called on a denied path")
	}

	return nil
}

func (f *fakeClient) Scale(_ context.Context, res policy.Resource, ns, name string, replicas int32) error {
	f.mu.Lock()
	f.scaleN++
	f.gotResource, f.gotNamespace, f.gotName, f.gotReplicas = res, ns, name, replicas
	f.mu.Unlock()

	if !f.allowScale {
		f.t.Errorf("SECURITY INVARIANT VIOLATED: kube Scale called on a denied path")
	}

	return nil
}

func (f *fakeClient) Exec(_ context.Context, _, _ string, _ kube.ExecOptions) (kube.ExecResult, error) {
	f.t.Errorf("SECURITY INVARIANT VIOLATED: kube Exec called unexpectedly")
	return kube.ExecResult{}, nil
}

func (f *fakeClient) Logs(_ context.Context, ns, name string, opts kube.LogsOptions) (string, error) {
	f.mu.Lock()
	f.logsN++
	f.gotNamespace, f.gotName, f.gotTail = ns, name, opts.TailLines
	f.mu.Unlock()

	if !f.allowLogs {
		f.t.Errorf("SECURITY INVARIANT VIOLATED: kube Logs called on a denied path")
	}

	if f.logsOut == "" {
		return "hello from pod", nil
	}

	return f.logsOut, nil
}

// fakeFactory hands out a single fakeClient regardless of context.
type fakeFactory struct {
	client *fakeClient
	err    error
}

func (f *fakeFactory) Client(_ string) (kube.Client, error) {
	if f.err != nil {
		return nil, f.err
	}

	return f.client, nil
}

func (f *fakeFactory) ResolveContext(name string) (string, error) {
	if f.err != nil {
		return "", f.err
	}

	if name != "" {
		return name, nil
	}

	if f.client != nil {
		return f.client.context, nil
	}

	return "", nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func mustEngine(t *testing.T, yaml string) *policy.Engine {
	t.Helper()

	e, err := policy.Load(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("policy.Load: %v", err)
	}

	return e
}

// connect wires the mcp.Server to an in-memory client session and returns it.
func connect(t *testing.T, engine *policy.Engine, fac mcp.ClientFactory, opts ...mcp.Option) *mcpsdk.ClientSession {
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

func call(t *testing.T, cs *mcpsdk.ClientSession, name string, args map[string]any) *mcpsdk.CallToolResult {
	t.Helper()

	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool %s: protocol error: %v", name, err)
	}

	return res
}

// resultText concatenates all text content blocks of a result.
func resultText(t *testing.T, res *mcpsdk.CallToolResult) string {
	t.Helper()

	var b strings.Builder

	for _, c := range res.Content {
		if tc, ok := c.(*mcpsdk.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}

	return b.String()
}

const allowReadProd = `
policies:
  - contexts: ".*"
    allow:
      resources: ["*"]
      verbs: [get, list]
`

const allowScale = `
policies:
  - contexts: ".*"
    allow:
      resources: ["*"]
      verbs: [scale]
`

const allowUpdateOnly = `
policies:
  - contexts: ".*"
    allow:
      resources: ["*"]
      verbs: [update]
`

const allowLogsPolicy = `
policies:
  - contexts: ".*"
    allow:
      resources: ["*"]
      verbs: [logs]
`

// ---------------------------------------------------------------------------
// 1. Tool registration
// ---------------------------------------------------------------------------

// The version reported in the MCP initialize handshake must come from
// WithVersion (the build-stamped value in production), and fall back to a
// non-empty default when unset — never an empty string.
func TestServerReportsVersion(t *testing.T) {
	t.Parallel()

	fac := &fakeFactory{client: &fakeClient{t: t}}

	t.Run("from WithVersion", func(t *testing.T) {
		t.Parallel()
		cs := connect(t, mustEngine(t, allowReadProd), fac, mcp.WithVersion("9.9.9"))
		if got := cs.InitializeResult().ServerInfo.Version; got != "9.9.9" {
			t.Errorf("reported version = %q, want 9.9.9", got)
		}
	})

	t.Run("default when unset", func(t *testing.T) {
		t.Parallel()
		cs := connect(t, mustEngine(t, allowReadProd), fac)
		if got := cs.InitializeResult().ServerInfo.Version; got == "" {
			t.Error("reported version is empty; want a non-empty default")
		}
	})
}

func TestToolRegistration(t *testing.T) {
	t.Parallel()

	cs := connect(t, mustEngine(t, allowReadProd), &fakeFactory{client: &fakeClient{t: t}})

	res, err := cs.ListTools(context.Background(), &mcpsdk.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	got := map[string]bool{}
	for _, tool := range res.Tools {
		got[tool.Name] = true
	}

	want := []string{
		"k8s_list", "k8s_get", "k8s_apply", "k8s_delete",
		"k8s_logs", "k8s_exec", "k8s_scale", "k8s_capabilities",
	}

	for _, name := range want {
		if !got[name] {
			t.Errorf("tool %q not registered; got %v", name, got)
		}
	}

	if len(res.Tools) != len(want) {
		t.Errorf("got %d tools, want %d", len(res.Tools), len(want))
	}
}

// ---------------------------------------------------------------------------
// 2. Arg decoding (plural + GVK forms, namespace/context)
// ---------------------------------------------------------------------------

func TestArgDecoding(t *testing.T) {
	t.Parallel()

	res := policy.Resource{Group: "apps", Version: "v1", Kind: "Deployment", Plural: "deployments"}
	fc := &fakeClient{t: t, res: res, scope: kube.ScopeNamespaced, allowList: true}

	cs := connect(t, mustEngine(t, allowReadProd), &fakeFactory{client: fc})

	// GVK form, explicit namespace + context.
	out := call(t, cs, "k8s_list", map[string]any{
		"resource":  "apps/v1/Deployment",
		"namespace": "team-a",
		"context":   "prod",
	})
	if out.IsError {
		t.Fatalf("k8s_list returned error: %s", resultText(t, out))
	}

	if fc.listN != 1 {
		t.Fatalf("expected 1 List call, got %d", fc.listN)
	}

	if fc.gotResource != res {
		t.Errorf("List got resource %+v, want %+v", fc.gotResource, res)
	}

	if fc.gotNamespace != "team-a" {
		t.Errorf("List got namespace %q, want team-a", fc.gotNamespace)
	}
}

func TestClusterScopedNamespaceForcedEmpty(t *testing.T) {
	t.Parallel()

	res := policy.Resource{Version: "v1", Kind: "Node", Plural: "nodes"}
	fc := &fakeClient{t: t, res: res, scope: kube.ScopeClusterScoped, allowList: true}

	cs := connect(t, mustEngine(t, allowReadProd), &fakeFactory{client: fc})

	// A stray namespace on a cluster-scoped resource must be dropped.
	out := call(t, cs, "k8s_list", map[string]any{"resource": "nodes", "namespace": "oops"})
	if out.IsError {
		t.Fatalf("k8s_list error: %s", resultText(t, out))
	}

	if fc.gotNamespace != "" {
		t.Errorf("cluster-scoped List got namespace %q, want empty", fc.gotNamespace)
	}
}

// ---------------------------------------------------------------------------
// 3. THE SECURITY INVARIANT — denied calls do zero cluster I/O
// ---------------------------------------------------------------------------

func TestSecurityInvariant_NotGranted(t *testing.T) {
	t.Parallel()

	// Policy grants only get/list; a delete is not granted (default deny).
	fc := &fakeClient{
		t:     t,
		res:   policy.Resource{Version: "v1", Kind: "Pod", Plural: "pods"},
		scope: kube.ScopeNamespaced,
		// all CRUD left disallowed: any call fails the test.
	}

	cs := connect(t, mustEngine(t, allowReadProd), &fakeFactory{client: fc})

	out := call(t, cs, "k8s_delete", map[string]any{"resource": "pods", "name": "p1", "namespace": "default"})

	if !out.IsError {
		t.Fatalf("expected denied delete to be an error result")
	}

	msg := resultText(t, out)
	if !strings.Contains(msg, "not granted") {
		t.Errorf("not-granted reason missing from message: %q", msg)
	}

	if fc.deleteN != 0 || fc.getN != 0 || fc.listN != 0 || fc.applyN != 0 {
		t.Errorf("ZERO-I/O invariant violated: get=%d list=%d apply=%d delete=%d",
			fc.getN, fc.listN, fc.applyN, fc.deleteN)
	}
}

func TestSecurityInvariant_ExplicitDeny(t *testing.T) {
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
	fc := &fakeClient{
		t:     t,
		res:   policy.Resource{Version: "v1", Kind: "Pod", Plural: "pods"},
		scope: kube.ScopeNamespaced,
	}

	cs := connect(t, mustEngine(t, cfg), &fakeFactory{client: fc})

	out := call(t, cs, "k8s_delete", map[string]any{"resource": "pods", "name": "p1", "namespace": "default"})

	if !out.IsError {
		t.Fatalf("expected explicit-deny delete to be an error result")
	}

	msg := resultText(t, out)
	if !strings.Contains(msg, "denied by deny rule") {
		t.Errorf("explicit-deny reason missing from message: %q", msg)
	}

	if fc.deleteN != 0 {
		t.Errorf("ZERO-I/O invariant violated: delete called %d times", fc.deleteN)
	}
}

func TestSecurityInvariant_ExecDeniedZeroIO(t *testing.T) {
	t.Parallel()

	const cfg = `
policies:
  - contexts: ".*"
    deny:
      resources: ["*"]
      verbs: [exec]
`
	fc := &fakeClient{t: t, res: policy.Resource{Version: "v1", Kind: "Pod", Plural: "pods"}, scope: kube.ScopeNamespaced}

	cs := connect(t, mustEngine(t, cfg), &fakeFactory{client: fc})

	out := call(t, cs, "k8s_exec", map[string]any{
		"resource": "pods", "name": "p1", "namespace": "default", "command": []any{"sh"},
	})

	if !out.IsError {
		t.Fatalf("expected denied exec to be an error result")
	}

	if !strings.Contains(resultText(t, out), "denied by deny rule") {
		t.Errorf("exec deny reason missing: %q", resultText(t, out))
	}

	if fc.getN+fc.listN+fc.applyN+fc.deleteN != 0 {
		t.Errorf("exec deny did cluster I/O")
	}
}

// ---------------------------------------------------------------------------
//  APPLY RULE — fully-denied does NOT do the existence Get
// ---------------------------------------------------------------------------

func TestApply_FullyDenied_NoExistenceGet(t *testing.T) {
	t.Parallel()

	// Only read verbs granted: both create and update are not-granted.
	fc := &fakeClient{
		t:     t,
		res:   policy.Resource{Version: "v1", Kind: "ConfigMap", Plural: "configmaps"},
		scope: kube.ScopeNamespaced,
		// no allow* set: any Get/Apply fails the test.
	}

	cs := connect(t, mustEngine(t, allowReadProd), &fakeFactory{client: fc})

	out := call(t, cs, "k8s_apply", map[string]any{
		"resource":  "configmaps",
		"namespace": "default",
		"manifest": map[string]any{
			"apiVersion": "v1", "kind": "ConfigMap",
			"metadata": map[string]any{"name": "cm1"},
		},
	})

	if !out.IsError {
		t.Fatalf("expected fully-denied apply to be an error")
	}

	if fc.getN != 0 {
		t.Errorf("APPLY RULE violated: existence Get ran on a fully-denied apply (getN=%d)", fc.getN)
	}

	if fc.applyN != 0 {
		t.Errorf("Apply ran on a fully-denied apply (applyN=%d)", fc.applyN)
	}
}

func TestApply_CreateAllowedUpdateDenied_ExistingObjectDeniesUpdate(t *testing.T) {
	t.Parallel()

	const cfg = `
policies:
  - contexts: ".*"
    allow:
      resources: ["*"]
      verbs: [get, create]
`
	// create allowed, update NOT granted. Object exists -> must deny update,
	// and the existence Get IS allowed (a verb was permittable).
	fc := &fakeClient{
		t:        t,
		res:      policy.Resource{Version: "v1", Kind: "ConfigMap", Plural: "configmaps"},
		scope:    kube.ScopeNamespaced,
		allowGet: true, // existence check permitted because create is permittable
		// allowApply false: the eventual update must NOT apply.
	}

	cs := connect(t, mustEngine(t, cfg), &fakeFactory{client: fc})

	out := call(t, cs, "k8s_apply", map[string]any{
		"resource":  "configmaps",
		"namespace": "default",
		"manifest": map[string]any{
			"apiVersion": "v1", "kind": "ConfigMap",
			"metadata": map[string]any{"name": "cm1"},
		},
	})

	if !out.IsError {
		t.Fatalf("expected update-denied apply (existing object) to be an error")
	}

	if fc.getN != 1 {
		t.Errorf("expected exactly 1 existence Get, got %d", fc.getN)
	}

	if fc.applyN != 0 {
		t.Errorf("Apply must not run when concrete verb (update) is denied; applyN=%d", fc.applyN)
	}
}

func TestApply_CreateAllowed_AbsentObjectCreates(t *testing.T) {
	t.Parallel()

	const cfg = `
policies:
  - contexts: ".*"
    allow:
      resources: ["*"]
      verbs: [get, create]
`
	fc := &fakeClient{
		t:                  t,
		res:                policy.Resource{Version: "v1", Kind: "ConfigMap", Plural: "configmaps"},
		scope:              kube.ScopeNamespaced,
		allowGet:           true,
		allowApply:         true,
		getReturnsNotFound: true, // absent -> create
	}

	cs := connect(t, mustEngine(t, cfg), &fakeFactory{client: fc})

	out := call(t, cs, "k8s_apply", map[string]any{
		"resource":  "configmaps",
		"namespace": "default",
		"manifest": map[string]any{
			"apiVersion": "v1", "kind": "ConfigMap",
			"metadata": map[string]any{"name": "cm1"},
		},
	})

	if out.IsError {
		t.Fatalf("expected allowed create apply to succeed: %s", resultText(t, out))
	}

	if fc.applyN != 1 {
		t.Errorf("expected 1 Apply, got %d", fc.applyN)
	}
}

// A non-NotFound existence-check error must fail closed: apply does not fall
// through to the create verb (which would be fail-open on verb selection), and
// no Apply runs.
func TestApply_NonNotFoundExistenceError_FailsClosed(t *testing.T) {
	t.Parallel()

	const cfg = `
policies:
  - contexts: ".*"
    allow:
      resources: ["*"]
      verbs: [get, create]
`
	// A genuinely non-NotFound error (Forbidden). Must NOT be misread as absent.
	forbidden := apierrors.NewForbidden(
		schema.GroupResource{Resource: "configmaps"}, "cm1", errors.New("nope"))
	if apierrors.IsNotFound(forbidden) {
		t.Fatalf("test setup invalid: synthetic error must NOT be IsNotFound")
	}

	fc := &fakeClient{
		t:        t,
		res:      policy.Resource{Version: "v1", Kind: "ConfigMap", Plural: "configmaps"},
		scope:    kube.ScopeNamespaced,
		allowGet: true, // existence check permitted because create is permittable
		// allowApply stays false: any Apply on this path fails the test.
		getErr: forbidden,
	}

	cs := connect(t, mustEngine(t, cfg), &fakeFactory{client: fc})

	out := call(t, cs, "k8s_apply", map[string]any{
		"resource":  "configmaps",
		"namespace": "default",
		"manifest": map[string]any{
			"apiVersion": "v1", "kind": "ConfigMap",
			"metadata": map[string]any{"name": "cm1"},
		},
	})

	if !out.IsError {
		t.Fatalf("expected non-NotFound existence error to fail closed, got success")
	}

	if !strings.Contains(resultText(t, out), "existence check") {
		t.Errorf("expected error mentioning %q, got %q", "existence check", resultText(t, out))
	}

	if fc.applyN != 0 {
		t.Errorf("Apply must not run when existence check fails closed; applyN=%d", fc.applyN)
	}
}

// ---------------------------------------------------------------------------
// 4. Allowed path executes the right method with the resolved resource/ns
// ---------------------------------------------------------------------------

func TestAllowedGet(t *testing.T) {
	t.Parallel()

	res := policy.Resource{Version: "v1", Kind: "Pod", Plural: "pods"}
	fc := &fakeClient{t: t, res: res, scope: kube.ScopeNamespaced, allowGet: true}

	cs := connect(t, mustEngine(t, allowReadProd), &fakeFactory{client: fc})

	out := call(t, cs, "k8s_get", map[string]any{"resource": "pods", "name": "p1", "namespace": "default"})
	if out.IsError {
		t.Fatalf("allowed get errored: %s", resultText(t, out))
	}

	if fc.getN != 1 {
		t.Fatalf("expected 1 Get, got %d", fc.getN)
	}

	if fc.gotName != "p1" || fc.gotNamespace != "default" || fc.gotResource != res {
		t.Errorf("Get got name=%q ns=%q res=%+v", fc.gotName, fc.gotNamespace, fc.gotResource)
	}

	if !strings.Contains(resultText(t, out), "Pod") {
		t.Errorf("expected object body in result, got %q", resultText(t, out))
	}
}

func TestAllowedDelete(t *testing.T) {
	t.Parallel()

	const cfg = `
policies:
  - contexts: ".*"
    allow:
      resources: ["pods"]
      verbs: [delete]
`
	res := policy.Resource{Version: "v1", Kind: "Pod", Plural: "pods"}
	fc := &fakeClient{t: t, res: res, scope: kube.ScopeNamespaced, allowDelete: true}

	cs := connect(t, mustEngine(t, cfg), &fakeFactory{client: fc})

	out := call(t, cs, "k8s_delete", map[string]any{"resource": "pods", "name": "p1", "namespace": "default"})
	if out.IsError {
		t.Fatalf("allowed delete errored: %s", resultText(t, out))
	}

	if fc.deleteN != 1 {
		t.Errorf("expected 1 Delete, got %d", fc.deleteN)
	}
}

func TestLogsAllowedRendersAndClamps(t *testing.T) {
	t.Parallel()
	fc := &fakeClient{t: t, context: "c", res: policy.Resource{Plural: "pods"}, scope: kube.ScopeNamespaced, allowLogs: true}
	cs := connect(t, mustEngine(t, allowLogsPolicy), &fakeFactory{client: fc})

	// no tailLines -> default 100 (connect() uses default LogLimits)
	res := call(t, cs, "k8s_logs", map[string]any{"resource": "pods", "name": "web", "namespace": "default"})
	if got := resultText(t, res); !strings.Contains(got, "hello from pod") {
		t.Errorf("result = %q", got)
	}
	if fc.gotTail == nil || *fc.gotTail != 100 {
		t.Errorf("default tail = %v, want 100", fc.gotTail)
	}

	// tailLines above the max (2000) -> clamped to 2000
	call(t, cs, "k8s_logs", map[string]any{"resource": "pods", "name": "web", "namespace": "default", "tailLines": 99999})
	if fc.gotTail == nil || *fc.gotTail != 2000 {
		t.Errorf("clamped tail = %v, want 2000", fc.gotTail)
	}
}

func TestLogsDeniedZeroIO(t *testing.T) {
	t.Parallel()
	fc := &fakeClient{t: t, context: "c", res: policy.Resource{Plural: "pods"}, scope: kube.ScopeNamespaced, allowLogs: false}
	cs := connect(t, mustEngine(t, allowReadProd), &fakeFactory{client: fc}) // allowReadProd grants get/list, NOT logs
	call(t, cs, "k8s_logs", map[string]any{"resource": "pods", "name": "web", "namespace": "default"})
	if fc.logsN != 0 {
		t.Errorf("logsN=%d, want 0 (zero I/O on deny)", fc.logsN)
	}
}

func TestScaleAllowed(t *testing.T) {
	t.Parallel()
	fc := &fakeClient{t: t, context: "c", res: policy.Resource{Plural: "deployments"}, scope: kube.ScopeNamespaced, allowScale: true}
	cs := connect(t, mustEngine(t, allowScale), &fakeFactory{client: fc})
	res := call(t, cs, "k8s_scale", map[string]any{"resource": "deployments", "name": "web", "namespace": "default", "replicas": 3})
	if got := resultText(t, res); !strings.Contains(got, "scaled") {
		t.Errorf("result = %q, want a scaled confirmation", got)
	}
	if fc.scaleN != 1 || fc.gotReplicas != 3 {
		t.Errorf("scaleN=%d replicas=%d, want 1 and 3", fc.scaleN, fc.gotReplicas)
	}
}

func TestScaleGatedAsScaleNotUpdate(t *testing.T) {
	t.Parallel()
	// Policy grants only `update`; scale must be DENIED (zero I/O).
	fc := &fakeClient{t: t, context: "c", res: policy.Resource{Plural: "deployments"}, scope: kube.ScopeNamespaced, allowScale: false}
	cs := connect(t, mustEngine(t, allowUpdateOnly), &fakeFactory{client: fc})
	res := call(t, cs, "k8s_scale", map[string]any{"resource": "deployments", "name": "web", "namespace": "default", "replicas": 3})
	if !res.IsError {
		t.Fatalf("expected denied scale to be an error result")
	}
	if got := resultText(t, res); !strings.Contains(strings.ToLower(got), "denied") && !strings.Contains(strings.ToLower(got), "not granted") {
		t.Errorf("result = %q, want a denial", got)
	}
	if fc.scaleN != 0 {
		t.Errorf("scaleN=%d, want 0 (zero I/O on deny)", fc.scaleN)
	}
}

// ---------------------------------------------------------------------------
// 5. k8s_capabilities — pure policy read, zero kube calls
// ---------------------------------------------------------------------------

func TestCapabilitiesZeroKubeCalls(t *testing.T) {
	t.Parallel()

	const cfg = `
policies:
  - contexts: "prod"
    allow:
      resources: ["pods"]
      verbs: [get, list]
    deny:
      resources: ["*"]
      verbs: [exec]
`
	// factory with no client: if capabilities touches kube, Client() returns an
	// error and the result would surface it.
	fac := &fakeFactory{err: errFactoryShouldNotBeCalled}

	cs := connect(t, mustEngine(t, cfg), fac)

	out := call(t, cs, "k8s_capabilities", map[string]any{"context": "prod"})
	if out.IsError {
		t.Fatalf("capabilities errored: %s", resultText(t, out))
	}

	text := resultText(t, out)
	if !strings.Contains(text, "allow") || !strings.Contains(text, "pods") {
		t.Errorf("capabilities missing allow rule: %q", text)
	}

	if !strings.Contains(text, "deny") || !strings.Contains(text, "exec") {
		t.Errorf("capabilities missing deny rule: %q", text)
	}
}

// ---------------------------------------------------------------------------
// 6. Regressions found by end-to-end testing against a live API server
// ---------------------------------------------------------------------------

// When the `context` arg is omitted, the gate must evaluate policy against the
// client's RESOLVED context, not the empty string. Policy here grants only
// "prod"; an omitted context (client resolves to "prod") must be ALLOWED.
func TestGateUsesResolvedContextWhenArgOmitted(t *testing.T) {
	t.Parallel()

	const prodOnly = `
policies:
  - contexts: "^prod$"
    allow:
      resources: ["*"]
      verbs: [get]
`
	res := policy.Resource{Version: "v1", Kind: "ConfigMap", Plural: "configmaps"}
	fc := &fakeClient{t: t, context: "prod", res: res, scope: kube.ScopeNamespaced, allowGet: true}

	cs := connect(t, mustEngine(t, prodOnly), &fakeFactory{client: fc})

	// No "context" arg — defaults to the client's resolved context ("prod").
	out := call(t, cs, "k8s_get", map[string]any{"resource": "configmaps", "name": "app", "namespace": "demo"})
	if out.IsError {
		t.Fatalf("expected allow (context resolves to prod), got error: %s", resultText(t, out))
	}

	if fc.getN != 1 {
		t.Fatalf("expected Get to run once, got %d", fc.getN)
	}
}

// k8s_list must render the list's items. UnstructuredList keeps items in .Items
// (not .Object), so a regression that marshals .Object drops them.
func TestListRendersItems(t *testing.T) {
	t.Parallel()

	res := policy.Resource{Version: "v1", Kind: "ConfigMap", Plural: "configmaps"}
	fc := &fakeClient{
		t: t, res: res, scope: kube.ScopeNamespaced, allowList: true,
		listItems: []any{
			map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": "app-config"}},
			map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": "feature-flags"}},
		},
	}

	cs := connect(t, mustEngine(t, allowReadProd), &fakeFactory{client: fc})

	out := call(t, cs, "k8s_list", map[string]any{"resource": "configmaps", "namespace": "demo"})
	if out.IsError {
		t.Fatalf("k8s_list error: %s", resultText(t, out))
	}

	txt := resultText(t, out)
	for _, want := range []string{`"items"`, "app-config", "feature-flags"} {
		if !strings.Contains(txt, want) {
			t.Errorf("list output missing %q; got:\n%s", want, txt)
		}
	}
}

var errFactoryShouldNotBeCalled = errors.New("factory should not be called")
