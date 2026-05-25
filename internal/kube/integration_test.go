// SPDX-License-Identifier: Apache-2.0

package kube_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"github.com/kubeleash/kubeleash/internal/kube"
	"github.com/kubeleash/kubeleash/internal/policy"
)

const testContextName = "kubeleash-test"

// Package-level envtest control plane, started once in TestMain and shared by
// every test in the package. Per-test isolation comes from the unique object
// names the tests already use; tests needing namespace isolation create their
// own namespaces.
var (
	// sharedEnv is the running control plane, or nil when KUBEBUILDER_ASSETS is
	// unset (in which case every test must skip).
	sharedEnv *envtest.Environment
	// sharedCfg is the rest.Config for sharedEnv.
	sharedCfg *rest.Config
	// sharedKubeconfig is the path to a kubeconfig pointing at sharedEnv.
	sharedKubeconfig string
	// envtestAvailable reports whether the control plane started; when false,
	// requireEnvtest skips the calling test rather than nil-panicking.
	envtestAvailable bool
)

// TestMain starts a single envtest control plane for the whole package. If
// KUBEBUILDER_ASSETS is unset it does NOT start the env; individual tests then
// skip via requireEnvtest. This cuts the suite from one control-plane boot per
// test down to exactly one.
func TestMain(m *testing.M) {
	code, err := runWithEnvtest(m)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kube envtest setup: %v\n", err)
		os.Exit(1)
	}

	os.Exit(code)
}

// runWithEnvtest owns the control-plane lifecycle so env.Stop runs via defer
// even though TestMain itself must call os.Exit.
func runWithEnvtest(m *testing.M) (int, error) {
	// envtest reads KUBEBUILDER_ASSETS itself; we only need to detect presence so
	// the suite can skip cleanly off-CI without the assets installed.
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		return m.Run(), nil
	}

	env := &envtest.Environment{
		CRDs: []*apiextv1.CustomResourceDefinition{
			namespacedCRD(),
			clusterScopedCRD(),
		},
	}

	cfg, err := env.Start()
	if err != nil {
		return 0, fmt.Errorf("envtest.Start (assets=%q): %w", os.Getenv("KUBEBUILDER_ASSETS"), err)
	}

	if cfg == nil {
		return 0, fmt.Errorf("envtest.Start returned a nil rest.Config")
	}

	defer func() {
		if stopErr := env.Stop(); stopErr != nil {
			fmt.Fprintf(os.Stderr, "envtest.Stop: %v\n", stopErr)
		}
	}()

	kubeconfigPath, err := writeSharedKubeconfig(cfg)
	if err != nil {
		return 0, err
	}

	// Wait for the CRDs to become Established so discovery sees them.
	if err := waitForCRDsEstablished(cfg); err != nil {
		return 0, err
	}

	sharedEnv = env
	sharedCfg = cfg
	sharedKubeconfig = kubeconfigPath
	envtestAvailable = true

	return m.Run(), nil
}

// requireEnvtest skips the calling test when no control plane is running. Every
// test that needs the cluster must call this first to avoid nil-panicking when
// KUBEBUILDER_ASSETS is unset.
func requireEnvtest(t *testing.T) {
	t.Helper()

	if !envtestAvailable {
		t.Skip("KUBEBUILDER_ASSETS not set; run 'setup-envtest use' or set it " +
			"(CI sets it explicitly); skipping envtest suite")
	}
}

// writeSharedKubeconfig writes a kubeconfig file pointing at cfg with a single
// named context so the Factory can load it like a real on-disk kubeconfig. It
// is used once from TestMain; the file lives in os.TempDir for the package run.
func writeSharedKubeconfig(cfg *rest.Config) (string, error) {
	apiCfg := clientcmdapi.NewConfig()
	apiCfg.Clusters[testContextName] = &clientcmdapi.Cluster{
		Server:                   cfg.Host,
		CertificateAuthorityData: cfg.CAData,
	}
	apiCfg.AuthInfos[testContextName] = &clientcmdapi.AuthInfo{
		ClientCertificateData: cfg.CertData,
		ClientKeyData:         cfg.KeyData,
	}
	apiCfg.Contexts[testContextName] = &clientcmdapi.Context{
		Cluster:  testContextName,
		AuthInfo: testContextName,
	}
	apiCfg.CurrentContext = testContextName

	dir, err := os.MkdirTemp("", "kubeleash-kube-test")
	if err != nil {
		return "", fmt.Errorf("mkdir temp for kubeconfig: %w", err)
	}

	path := filepath.Join(dir, "kubeconfig")
	if err := clientcmd.WriteToFile(*apiCfg, path); err != nil {
		return "", fmt.Errorf("write kubeconfig: %w", err)
	}

	return path, nil
}

// waitForCRDsEstablished blocks until both test CRDs report Established so
// discovery sees them. It is called once from TestMain.
func waitForCRDsEstablished(cfg *rest.Config) error {
	cs, err := apiextclient.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("apiextensions client: %w", err)
	}

	names := []string{"widgets.example.com", "clusterwidgets.example.com"}
	deadline := time.Now().Add(30 * time.Second)

	for _, name := range names {
		for {
			crd, getErr := cs.ApiextensionsV1().CustomResourceDefinitions().
				Get(context.Background(), name, metav1.GetOptions{})
			if getErr == nil && crdEstablished(crd) {
				break
			}

			if time.Now().After(deadline) {
				return fmt.Errorf("CRD %q not Established in time (lastErr=%w)", name, getErr)
			}

			time.Sleep(100 * time.Millisecond)
		}
	}

	return nil
}

func crdEstablished(crd *apiextv1.CustomResourceDefinition) bool {
	for _, c := range crd.Status.Conditions {
		if c.Type == apiextv1.Established && c.Status == apiextv1.ConditionTrue {
			return true
		}
	}

	return false
}

// namespacedCRD defines a trivial namespaced CRD: widgets.example.com.
func namespacedCRD() *apiextv1.CustomResourceDefinition {
	return trivialCRD("widgets.example.com", "widgets", "Widget", apiextv1.NamespaceScoped)
}

// clusterScopedCRD defines a trivial cluster-scoped CRD:
// clusterwidgets.example.com.
func clusterScopedCRD() *apiextv1.CustomResourceDefinition {
	return trivialCRD("clusterwidgets.example.com", "clusterwidgets", "ClusterWidget", apiextv1.ClusterScoped)
}

func trivialCRD(name, plural, kind string, scope apiextv1.ResourceScope) *apiextv1.CustomResourceDefinition {
	preserve := true

	return &apiextv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: apiextv1.CustomResourceDefinitionSpec{
			Group: "example.com",
			Names: apiextv1.CustomResourceDefinitionNames{
				Plural: plural,
				Kind:   kind,
			},
			Scope: scope,
			Versions: []apiextv1.CustomResourceDefinitionVersion{
				{
					Name:    "v1alpha1",
					Served:  true,
					Storage: true,
					Schema: &apiextv1.CustomResourceValidation{
						OpenAPIV3Schema: &apiextv1.JSONSchemaProps{
							Type:                   "object",
							XPreserveUnknownFields: &preserve,
						},
					},
				},
			},
		},
	}
}

// newClient builds a kube.Client for the test context from the shared
// kubeconfig, exercising the real Factory path.
func newClient(t *testing.T) kube.Client {
	t.Helper()

	f, err := kube.NewFactory(kube.Options{KubeconfigPath: sharedKubeconfig})
	if err != nil {
		t.Fatalf("NewFactory: %v", err)
	}

	c, err := f.Client(testContextName)
	if err != nil {
		t.Fatalf("Factory.Client(%q): %v", testContextName, err)
	}

	return c
}

// ---------------------------------------------------------------------------
// Resolve + scope discovery
// ---------------------------------------------------------------------------

func TestResolveAndScope(t *testing.T) {
	requireEnvtest(t)
	c := newClient(t)
	ctx := context.Background()

	tests := []struct {
		name      string
		ref       string
		want      policy.Resource
		wantScope kube.Scope
	}{
		{
			name:      "built-in namespaced plural: pods",
			ref:       "pods",
			want:      policy.Resource{Group: "", Version: "v1", Kind: "Pod", Plural: "pods"},
			wantScope: kube.ScopeNamespaced,
		},
		{
			name:      "built-in namespaced GVK: apps/v1/Deployment",
			ref:       "apps/v1/Deployment",
			want:      policy.Resource{Group: "apps", Version: "v1", Kind: "Deployment", Plural: "deployments"},
			wantScope: kube.ScopeNamespaced,
		},
		{
			name:      "built-in namespaced plural: deployments",
			ref:       "deployments",
			want:      policy.Resource{Group: "apps", Version: "v1", Kind: "Deployment", Plural: "deployments"},
			wantScope: kube.ScopeNamespaced,
		},
		{
			name:      "built-in cluster-scoped plural: nodes",
			ref:       "nodes",
			want:      policy.Resource{Group: "", Version: "v1", Kind: "Node", Plural: "nodes"},
			wantScope: kube.ScopeClusterScoped,
		},
		{
			name:      "built-in cluster-scoped plural: namespaces",
			ref:       "namespaces",
			want:      policy.Resource{Group: "", Version: "v1", Kind: "Namespace", Plural: "namespaces"},
			wantScope: kube.ScopeClusterScoped,
		},
		{
			name:      "built-in cluster-scoped plural: clusterroles",
			ref:       "clusterroles",
			want:      policy.Resource{Group: "rbac.authorization.k8s.io", Version: "v1", Kind: "ClusterRole", Plural: "clusterroles"},
			wantScope: kube.ScopeClusterScoped,
		},
		{
			name:      "CRD namespaced plural: widgets",
			ref:       "widgets",
			want:      policy.Resource{Group: "example.com", Version: "v1alpha1", Kind: "Widget", Plural: "widgets"},
			wantScope: kube.ScopeNamespaced,
		},
		{
			name:      "CRD namespaced GVK: example.com/v1alpha1/Widget",
			ref:       "example.com/v1alpha1/Widget",
			want:      policy.Resource{Group: "example.com", Version: "v1alpha1", Kind: "Widget", Plural: "widgets"},
			wantScope: kube.ScopeNamespaced,
		},
		{
			name:      "CRD cluster-scoped plural: clusterwidgets",
			ref:       "clusterwidgets",
			want:      policy.Resource{Group: "example.com", Version: "v1alpha1", Kind: "ClusterWidget", Plural: "clusterwidgets"},
			wantScope: kube.ScopeClusterScoped,
		},
		{
			name:      "CRD cluster-scoped GVK: example.com/v1alpha1/ClusterWidget",
			ref:       "example.com/v1alpha1/ClusterWidget",
			want:      policy.Resource{Group: "example.com", Version: "v1alpha1", Kind: "ClusterWidget", Plural: "clusterwidgets"},
			wantScope: kube.ScopeClusterScoped,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, scope, err := c.Resolve(ctx, tt.ref)
			if err != nil {
				t.Fatalf("Resolve(%q) error: %v", tt.ref, err)
			}

			if got != tt.want {
				t.Errorf("Resolve(%q) resource = %+v, want %+v", tt.ref, got, tt.want)
			}

			if scope != tt.wantScope {
				t.Errorf("Resolve(%q) scope = %v, want %v", tt.ref, scope, tt.wantScope)
			}
		})
	}
}

func TestResolveErrors(t *testing.T) {
	requireEnvtest(t)
	c := newClient(t)
	ctx := context.Background()

	tests := []struct {
		name string
		ref  string
	}{
		{"wildcard is rejected", "*"},
		{"empty ref is rejected", ""},
		{"unknown plural", "doesnotexistwidgets"},
		{"unknown GVK", "nope.example.com/v9/Nope"},
		{"malformed GVK (two parts)", "apps/Deployment"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, scope, err := c.Resolve(ctx, tt.ref)
			if err == nil {
				t.Fatalf("Resolve(%q) expected error, got nil (scope=%v)", tt.ref, scope)
			}

			if scope != kube.ScopeUnknown {
				t.Errorf("Resolve(%q) on error scope = %v, want ScopeUnknown", tt.ref, scope)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// CRUD round-trips
// ---------------------------------------------------------------------------

func TestCRUDBuiltin(t *testing.T) {
	requireEnvtest(t)
	c := newClient(t)
	ctx := context.Background()

	res, scope, err := c.Resolve(ctx, "configmaps")
	if err != nil {
		t.Fatalf("Resolve configmaps: %v", err)
	}

	if scope != kube.ScopeNamespaced {
		t.Fatalf("configmaps scope = %v, want namespaced", scope)
	}

	const ns = "default"

	name := "cm-roundtrip"

	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"name":      name,
			"namespace": ns,
		},
		"data": map[string]any{"hello": "world"},
	}}

	applied, err := c.Apply(ctx, res, ns, obj)
	if err != nil {
		t.Fatalf("Apply configmap: %v", err)
	}

	if applied.GetName() != name {
		t.Fatalf("applied name = %q, want %q", applied.GetName(), name)
	}

	got, err := c.Get(ctx, res, ns, name)
	if err != nil {
		t.Fatalf("Get configmap: %v", err)
	}

	if got.GetName() != name {
		t.Fatalf("got name = %q, want %q", got.GetName(), name)
	}

	list, err := c.List(ctx, res, ns)
	if err != nil {
		t.Fatalf("List configmaps: %v", err)
	}

	if !containsName(list, name) {
		t.Fatalf("List did not contain %q", name)
	}

	if err := c.Delete(ctx, res, ns, name); err != nil {
		t.Fatalf("Delete configmap: %v", err)
	}

	if _, err := c.Get(ctx, res, ns, name); err == nil {
		t.Fatalf("Get after Delete expected error, got nil")
	}
}

func TestCRUDCRD(t *testing.T) {
	requireEnvtest(t)
	c := newClient(t)
	ctx := context.Background()

	res, scope, err := c.Resolve(ctx, "widgets")
	if err != nil {
		t.Fatalf("Resolve widgets: %v", err)
	}

	if scope != kube.ScopeNamespaced {
		t.Fatalf("widgets scope = %v, want namespaced", scope)
	}

	const ns = "default"

	name := "widget-roundtrip"

	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "example.com/v1alpha1",
		"kind":       "Widget",
		"metadata": map[string]any{
			"name":      name,
			"namespace": ns,
		},
		"spec": map[string]any{"size": "large"},
	}}

	if _, err := c.Apply(ctx, res, ns, obj); err != nil {
		t.Fatalf("Apply widget: %v", err)
	}

	got, err := c.Get(ctx, res, ns, name)
	if err != nil {
		t.Fatalf("Get widget: %v", err)
	}

	if got.GetName() != name {
		t.Fatalf("got name = %q, want %q", got.GetName(), name)
	}

	if err := c.Delete(ctx, res, ns, name); err != nil {
		t.Fatalf("Delete widget: %v", err)
	}
}

// TestClusterScopedCRUD exercises a cluster-scoped CRD with namespace "" to
// prove the dynamic client routes cluster-scoped resources correctly.
func TestClusterScopedCRUD(t *testing.T) {
	requireEnvtest(t)
	c := newClient(t)
	ctx := context.Background()

	res, scope, err := c.Resolve(ctx, "clusterwidgets")
	if err != nil {
		t.Fatalf("Resolve clusterwidgets: %v", err)
	}

	if scope != kube.ScopeClusterScoped {
		t.Fatalf("clusterwidgets scope = %v, want cluster-scoped", scope)
	}

	name := "cw-roundtrip"

	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "example.com/v1alpha1",
		"kind":       "ClusterWidget",
		"metadata":   map[string]any{"name": name},
		"spec":       map[string]any{"size": "huge"},
	}}

	if _, err := c.Apply(ctx, res, "", obj); err != nil {
		t.Fatalf("Apply clusterwidget: %v", err)
	}

	got, err := c.Get(ctx, res, "", name)
	if err != nil {
		t.Fatalf("Get clusterwidget: %v", err)
	}

	if got.GetName() != name {
		t.Fatalf("got name = %q, want %q", got.GetName(), name)
	}

	if err := c.Delete(ctx, res, "", name); err != nil {
		t.Fatalf("Delete clusterwidget: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Factory: context resolution
// ---------------------------------------------------------------------------

func TestFactoryDefaultsToCurrentContext(t *testing.T) {
	requireEnvtest(t)

	f, err := kube.NewFactory(kube.Options{KubeconfigPath: sharedKubeconfig})
	if err != nil {
		t.Fatalf("NewFactory: %v", err)
	}

	// Empty context name should default to the kubeconfig current-context.
	c, err := f.Client("")
	if err != nil {
		t.Fatalf("Factory.Client(\"\"): %v", err)
	}

	if _, _, err := c.Resolve(context.Background(), "pods"); err != nil {
		t.Fatalf("Resolve via default context: %v", err)
	}
}

func TestFactoryUnknownContext(t *testing.T) {
	requireEnvtest(t)

	f, err := kube.NewFactory(kube.Options{KubeconfigPath: sharedKubeconfig})
	if err != nil {
		t.Fatalf("NewFactory: %v", err)
	}

	if _, err := f.Client("no-such-context"); err == nil {
		t.Fatal("Factory.Client(unknown) expected error, got nil")
	}
}

func containsName(list *unstructured.UnstructuredList, name string) bool {
	for i := range list.Items {
		if list.Items[i].GetName() == name {
			return true
		}
	}

	return false
}
