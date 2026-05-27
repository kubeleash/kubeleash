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
	k8sfake "k8s.io/client-go/kubernetes/fake"
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

func TestPodLogOptionsMapping(t *testing.T) {
	tail := int64(50)
	since := int64(120)
	limit := int64(4096)
	got := podLogOptions(LogsOptions{
		Container: "app", TailLines: &tail, Previous: true,
		SinceSeconds: &since, Timestamps: true, LimitBytes: &limit,
	})
	if got.Container != "app" || !got.Previous || !got.Timestamps {
		t.Errorf("flags not mapped: %+v", got)
	}
	if got.TailLines == nil || *got.TailLines != 50 ||
		got.SinceSeconds == nil || *got.SinceSeconds != 120 ||
		got.LimitBytes == nil || *got.LimitBytes != 4096 {
		t.Errorf("pointers not mapped: %+v", got)
	}
}

func TestLogsReturnsText(t *testing.T) {
	cs := k8sfake.NewSimpleClientset()
	c := &client{contextName: "t", clientset: cs}
	out, err := c.Logs(context.Background(), "default", "web", LogsOptions{})
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	if out == "" {
		t.Errorf("Logs returned empty; fake clientset returns canned output")
	}
}
