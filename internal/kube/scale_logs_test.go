// SPDX-License-Identifier: Apache-2.0
package kube

import (
	"context"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"

	"github.com/kubeleash/kubeleash/internal/policy"
)

func TestScalePatchesScaleSubresource(t *testing.T) {
	gvr := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	scheme := runtime.NewScheme()
	dc := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme,
		map[schema.GroupVersionResource]string{gvr: "DeploymentList"})

	// Pre-populate the tracker with the target object so the fake's ObjectReaction
	// can look it up when processing the scale subresource patch.
	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"})
	existing.SetName("web")
	existing.SetNamespace("default")
	if err := dc.Tracker().Add(existing); err != nil {
		t.Fatalf("add object to tracker: %v", err)
	}

	c := &client{contextName: "t", dyn: dc}
	res := policy.Resource{Group: "apps", Version: "v1", Kind: "Deployment", Plural: "deployments"}

	if err := c.Scale(context.Background(), res, "default", "web", 3); err != nil {
		t.Fatalf("Scale: %v", err)
	}

	var patched bool
	for _, a := range dc.Actions() {
		pa, ok := a.(clienttesting.PatchAction)
		if !ok || pa.GetSubresource() != "scale" {
			continue
		}
		patched = true
		if !strings.Contains(string(pa.GetPatch()), `"replicas":3`) {
			t.Errorf("patch body = %s, want replicas:3", pa.GetPatch())
		}
	}
	if !patched {
		t.Fatalf("no Patch on the scale subresource was issued; actions=%v", dc.Actions())
	}
}
