// SPDX-License-Identifier: Apache-2.0

package kube_test

import (
	"testing"

	"github.com/kubeleash/kubeleash/internal/kube"
)

// TestScopeClusterScopedFailSafe asserts the fail-safe invariant: only an
// explicit ScopeClusterScoped reports cluster-scoped, and the zero value
// (ScopeUnknown) never reports cluster-scoped.
func TestScopeClusterScopedFailSafe(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		s    kube.Scope
		want bool
	}{
		{"zero value is not cluster-scoped", kube.Scope(0), false},
		{"unknown is not cluster-scoped", kube.ScopeUnknown, false},
		{"namespaced is not cluster-scoped", kube.ScopeNamespaced, false},
		{"cluster-scoped is cluster-scoped", kube.ScopeClusterScoped, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.s.ClusterScoped(); got != tt.want {
				t.Errorf("Scope(%d).ClusterScoped() = %v, want %v", tt.s, got, tt.want)
			}
		})
	}
}

// TestScopeZeroValueIsUnknown guards that the zero value is ScopeUnknown, so an
// uninitialised Scope is obviously invalid rather than a real axis.
func TestScopeZeroValueIsUnknown(t *testing.T) {
	t.Parallel()

	var s kube.Scope
	if s != kube.ScopeUnknown {
		t.Fatalf("zero-value Scope = %v, want ScopeUnknown", s)
	}
}
