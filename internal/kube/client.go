// SPDX-License-Identifier: Apache-2.0

package kube

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"

	"github.com/kubeleash/kubeleash/internal/policy"
)

// fieldManager identifies kubeleash as the owner of server-side-applied fields.
const fieldManager = "kubeleash"

// client is the concrete [Client] for one kube context. It holds a dynamic
// client and a RESTMapper backed by cached discovery; construction itself does
// not contact the cluster (discovery is lazy via the deferred mapper).
type client struct {
	contextName string
	dyn         dynamic.Interface
	mapper      *restmapper.DeferredDiscoveryRESTMapper
}

// Context returns the resolved kube context name this client is scoped to.
func (c *client) Context() string { return c.contextName }

// newClient builds a concrete client for cfg. It does not perform cluster I/O.
func newClient(contextName string, cfg *rest.Config) (*client, error) {
	disco, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("kube: build discovery client for context %q: %w", contextName, err)
	}

	dyn, err := newDynamicClient(cfg)
	if err != nil {
		return nil, err
	}

	mapper := restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(disco))

	return &client{
		contextName: contextName,
		dyn:         dyn,
		mapper:      mapper,
	}, nil
}

// newDynamicClient builds a dynamic client for cfg.
func newDynamicClient(cfg *rest.Config) (dynamic.Interface, error) {
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("kube: build dynamic client: %w", err)
	}

	return dyn, nil
}

// Resolve implements [Client].
func (c *client) Resolve(_ context.Context, resourceRef string) (policy.Resource, Scope, error) {
	ref := strings.TrimSpace(resourceRef)
	if ref == "" {
		return policy.Resource{}, ScopeUnknown, fmt.Errorf("kube: empty resource reference")
	}

	if ref == "*" {
		return policy.Resource{}, ScopeUnknown,
			fmt.Errorf("kube: wildcard %q is a policy concept and cannot be resolved to a concrete resource", ref)
	}

	mapping, err := c.restMapping(ref)
	if err != nil {
		return policy.Resource{}, ScopeUnknown, err
	}

	scope, err := scopeFromMapping(mapping)
	if err != nil {
		return policy.Resource{}, ScopeUnknown, err
	}

	res := policy.Resource{
		Group:   mapping.GroupVersionKind.Group,
		Version: mapping.GroupVersionKind.Version,
		Kind:    mapping.GroupVersionKind.Kind,
		Plural:  mapping.Resource.Resource,
	}

	return res, scope, nil
}

// restMapping resolves a reference into a RESTMapping. The reference is either a
// "group/version/kind" string (exactly three slash-separated parts; the core
// group is the empty first part) or a plural resource name.
func (c *client) restMapping(ref string) (*meta.RESTMapping, error) {
	if strings.Contains(ref, "/") {
		parts := strings.Split(ref, "/")
		if len(parts) != 3 {
			return nil, fmt.Errorf(
				"kube: resource reference %q is not a valid group/version/kind (want exactly 3 parts)", ref)
		}

		gvk := schema.GroupVersionKind{Group: parts[0], Version: parts[1], Kind: parts[2]}

		mapping, err := c.mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
		if err != nil {
			return nil, fmt.Errorf("kube: resolve %q: %w", ref, err)
		}

		return mapping, nil
	}

	// Plural resource name: resolve to a GVR, then to a full mapping.
	gvr, err := c.mapper.ResourceFor(schema.GroupVersionResource{Resource: ref})
	if err != nil {
		return nil, fmt.Errorf("kube: resolve plural %q: %w", ref, err)
	}

	gvk, err := c.mapper.KindFor(gvr)
	if err != nil {
		return nil, fmt.Errorf("kube: resolve kind for %q: %w", ref, err)
	}

	mapping, err := c.mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return nil, fmt.Errorf("kube: mapping for %q: %w", ref, err)
	}

	return mapping, nil
}

// scopeFromMapping derives the fail-safe Scope from a RESTMapping. An
// unrecognised scope name is an error rather than a guess.
func scopeFromMapping(mapping *meta.RESTMapping) (Scope, error) {
	switch mapping.Scope.Name() {
	case meta.RESTScopeNameNamespace:
		return ScopeNamespaced, nil
	case meta.RESTScopeNameRoot:
		return ScopeClusterScoped, nil
	default:
		return ScopeUnknown, fmt.Errorf(
			"kube: unknown REST scope %q for %s", mapping.Scope.Name(), mapping.GroupVersionKind)
	}
}

// resourceInterface returns the dynamic resource interface for res, narrowed to
// namespace when the resource is namespaced and namespace is non-empty.
func (c *client) resourceInterface(res policy.Resource, namespace string) dynamic.ResourceInterface {
	gvr := schema.GroupVersionResource{
		Group:    res.Group,
		Version:  res.Version,
		Resource: res.Plural,
	}

	if namespace != "" {
		return c.dyn.Resource(gvr).Namespace(namespace)
	}

	return c.dyn.Resource(gvr)
}

// Get implements [Client].
func (c *client) Get(ctx context.Context, res policy.Resource, namespace, name string) (*unstructured.Unstructured, error) {
	obj, err := c.resourceInterface(res, namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("kube: get %s %q: %w", res.Plural, name, err)
	}

	return obj, nil
}

// List implements [Client].
func (c *client) List(ctx context.Context, res policy.Resource, namespace string) (*unstructured.UnstructuredList, error) {
	list, err := c.resourceInterface(res, namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("kube: list %s: %w", res.Plural, err)
	}

	return list, nil
}

// Apply implements [Client] via server-side apply.
func (c *client) Apply(ctx context.Context, res policy.Resource, namespace string, obj *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	if obj == nil {
		return nil, fmt.Errorf("kube: apply %s: nil object", res.Plural)
	}

	name := obj.GetName()
	if name == "" {
		return nil, fmt.Errorf("kube: apply %s: object has no name", res.Plural)
	}

	data, err := obj.MarshalJSON()
	if err != nil {
		return nil, fmt.Errorf("kube: apply %s %q: marshal: %w", res.Plural, name, err)
	}

	applied, err := c.resourceInterface(res, namespace).Patch(
		ctx, name, types.ApplyPatchType, data,
		metav1.PatchOptions{FieldManager: fieldManager},
	)
	if err != nil {
		return nil, fmt.Errorf("kube: apply %s %q: %w", res.Plural, name, err)
	}

	return applied, nil
}

// Delete implements [Client].
func (c *client) Delete(ctx context.Context, res policy.Resource, namespace, name string) error {
	if err := c.resourceInterface(res, namespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
		return fmt.Errorf("kube: delete %s %q: %w", res.Plural, name, err)
	}

	return nil
}

// Scale implements [Client] by merge-patching the scale subresource.
func (c *client) Scale(ctx context.Context, res policy.Resource, namespace, name string, replicas int32) error {
	patch := []byte(fmt.Sprintf(`{"spec":{"replicas":%d}}`, replicas))

	_, err := c.resourceInterface(res, namespace).Patch(
		ctx, name, types.MergePatchType, patch,
		metav1.PatchOptions{FieldManager: fieldManager}, "scale",
	)
	if err != nil {
		return fmt.Errorf("kube: scale %s %q: %w", res.Plural, name, err)
	}

	return nil
}
