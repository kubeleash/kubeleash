// SPDX-License-Identifier: Apache-2.0

package policy_test

import (
	"strings"
	"testing"

	"github.com/kubeleash/kubeleash/internal/policy"
)

// design.md YAML fixture (verbatim from the Policy model section).
const designFixtureYAML = `
policies:
  - contexts: ".*prod.*"
    namespaces: ["kube-system"]
    allow:
      resources: ["*"]
      verbs: [get, list, watch]
    deny:
      verbs: [exec]
`

// helpers

func mustLoad(t *testing.T, yaml string) *policy.Engine {
	t.Helper()

	eng, err := policy.Load(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("Load failed unexpectedly: %v", err)
	}

	return eng
}

func pods() policy.Resource {
	return policy.Resource{Group: "", Version: "v1", Kind: "Pod", Plural: "pods"}
}

func nodes() policy.Resource {
	return policy.Resource{Group: "", Version: "v1", Kind: "Node", Plural: "nodes"}
}

func deployments() policy.Resource {
	return policy.Resource{Group: "apps", Version: "v1", Kind: "Deployment", Plural: "deployments"}
}

func crdWidget() policy.Resource {
	return policy.Resource{Group: "example.com", Version: "v1alpha1", Kind: "Widget", Plural: "widgets"}
}

// ---------------------------------------------------------------------------
// Fail-safe zero-value test
// ---------------------------------------------------------------------------

// TestDecisionZeroValueIsDenied asserts the fail-safe invariant: a zero-value
// Decision must never report Allowed. This guards against accidental
// fail-open behaviour (e.g. an uninitialised Decision short-circuiting the
// policy gate). The Outcome constants are ordered so that NotGranted == 0.
func TestDecisionZeroValueIsDenied(t *testing.T) {
	t.Parallel()

	var d policy.Decision
	if d.Allowed() {
		t.Fatal("zero-value Decision must NOT be allowed (fail-safe default-deny violated)")
	}

	if d.Outcome == policy.Allowed {
		t.Fatal("zero-value Decision Outcome must not equal Allowed")
	}
}

// ---------------------------------------------------------------------------
// Evaluate tests
// ---------------------------------------------------------------------------

func TestEvaluate(t *testing.T) {
	t.Parallel()

	//nolint:govet // fieldalignment unimportant in tests
	tests := []struct {
		name    string
		yaml    string
		req     policy.Request
		want    policy.Outcome
		wantMsg []string // substrings that must appear in Reason
	}{
		// ----------------------------------------------------------------
		// deny-wins: allow AND deny on same (resource, verb) → ExplicitDeny
		// ----------------------------------------------------------------
		{
			name: "deny_wins_over_allow_same_rule",
			yaml: `
policies:
  - contexts: ".*"
    allow:
      resources: ["pods"]
      verbs: [get, exec]
    deny:
      resources: ["pods"]
      verbs: [exec]
`,
			req:     policy.Request{Context: "dev", Resource: pods(), Verb: policy.VerbExec},
			want:    policy.ExplicitDeny,
			wantMsg: []string{"exec"},
		},
		{
			name: "deny_wins_over_allow_different_rules",
			yaml: `
policies:
  - contexts: ".*"
    allow:
      resources: ["pods"]
      verbs: [get, exec]
  - contexts: ".*prod.*"
    deny:
      resources: ["*"]
      verbs: [exec]
`,
			req:     policy.Request{Context: "gke_prod_1", Resource: pods(), Verb: policy.VerbExec},
			want:    policy.ExplicitDeny,
			wantMsg: []string{"exec"},
		},

		// ----------------------------------------------------------------
		// default-deny: nothing matches → NotGranted
		// ----------------------------------------------------------------
		{
			name: "default_deny_no_matching_rule",
			yaml: `
policies:
  - contexts: ".*staging.*"
    allow:
      resources: ["pods"]
      verbs: [get, list]
`,
			req:  policy.Request{Context: "dev", Resource: pods(), Verb: policy.VerbGet},
			want: policy.NotGranted,
		},
		{
			name: "default_deny_verb_not_granted",
			yaml: `
policies:
  - contexts: ".*"
    allow:
      resources: ["pods"]
      verbs: [get, list]
`,
			req:  policy.Request{Context: "dev", Resource: pods(), Verb: policy.VerbDelete},
			want: policy.NotGranted,
		},
		{
			name: "default_deny_reason_contains_resource_and_verb",
			yaml: `
policies:
  - contexts: ".*staging.*"
    allow:
      resources: ["pods"]
      verbs: [get]
`,
			req:     policy.Request{Context: "dev", Resource: pods(), Verb: policy.VerbGet},
			want:    policy.NotGranted,
			wantMsg: []string{"pods", "get", "dev"},
		},

		// ----------------------------------------------------------------
		// ExplicitDeny and NotGranted are distinguishable via Outcome field
		// ----------------------------------------------------------------
		{
			name: "explicit_deny_outcome_is_distinct_from_not_granted",
			yaml: `
policies:
  - contexts: ".*"
    deny:
      verbs: [exec]
`,
			req:  policy.Request{Context: "dev", Resource: pods(), Verb: policy.VerbExec},
			want: policy.ExplicitDeny,
		},

		// ----------------------------------------------------------------
		// namespace narrowing
		// ----------------------------------------------------------------
		{
			name: "namespace_rule_matches_correct_ns",
			yaml: `
policies:
  - contexts: ".*"
    namespaces: ["kube-system"]
    allow:
      resources: ["pods"]
      verbs: [get]
`,
			req:  policy.Request{Context: "dev", Resource: pods(), Namespace: "kube-system", Verb: policy.VerbGet},
			want: policy.Allowed,
		},
		{
			name: "namespace_rule_no_match_different_ns",
			yaml: `
policies:
  - contexts: ".*"
    namespaces: ["kube-system"]
    allow:
      resources: ["pods"]
      verbs: [get]
`,
			req:  policy.Request{Context: "dev", Resource: pods(), Namespace: "default", Verb: policy.VerbGet},
			want: policy.NotGranted,
		},
		{
			name: "namespace_rule_never_matches_cluster_scoped",
			yaml: `
policies:
  - contexts: ".*"
    namespaces: ["kube-system"]
    allow:
      resources: ["nodes"]
      verbs: [get]
`,
			req:  policy.Request{Context: "dev", Resource: nodes(), ClusterScoped: true, Verb: policy.VerbGet},
			want: policy.NotGranted,
		},
		{
			name: "omitted_namespaces_governs_cluster_scoped",
			yaml: `
policies:
  - contexts: ".*"
    allow:
      resources: ["nodes"]
      verbs: [get, list]
`,
			req:  policy.Request{Context: "dev", Resource: nodes(), ClusterScoped: true, Verb: policy.VerbList},
			want: policy.Allowed,
		},
		{
			name: "omitted_namespaces_governs_any_ns",
			yaml: `
policies:
  - contexts: ".*"
    allow:
      resources: ["pods"]
      verbs: [get]
`,
			req:  policy.Request{Context: "dev", Resource: pods(), Namespace: "default", Verb: policy.VerbGet},
			want: policy.Allowed,
		},
		{
			name: "namespace_rule_cluster_scoped_even_if_ns_listed",
			yaml: `
policies:
  - contexts: ".*"
    namespaces: ["kube-system"]
    allow:
      resources: ["*"]
      verbs: [get]
`,
			// nodes are cluster-scoped; the rule specifies namespaces so it must NOT apply.
			req:  policy.Request{Context: "dev", Resource: nodes(), ClusterScoped: true, Verb: policy.VerbGet},
			want: policy.NotGranted,
		},

		// ----------------------------------------------------------------
		// context regex
		// ----------------------------------------------------------------
		{
			name: "context_regex_prod_matches_gke_prod_1",
			yaml: `
policies:
  - contexts: ".*prod.*"
    allow:
      resources: ["pods"]
      verbs: [get]
`,
			req:  policy.Request{Context: "gke_prod_1", Resource: pods(), Namespace: "default", Verb: policy.VerbGet},
			want: policy.Allowed,
		},
		{
			name: "context_regex_prod_does_not_match_staging",
			yaml: `
policies:
  - contexts: ".*prod.*"
    allow:
      resources: ["pods"]
      verbs: [get]
`,
			req:  policy.Request{Context: "staging", Resource: pods(), Namespace: "default", Verb: policy.VerbGet},
			want: policy.NotGranted,
		},
		{
			name: "context_regex_anchored_exact_match",
			yaml: `
policies:
  - contexts: "^dev$"
    allow:
      resources: ["pods"]
      verbs: [get]
`,
			req:  policy.Request{Context: "dev", Resource: pods(), Namespace: "default", Verb: policy.VerbGet},
			want: policy.Allowed,
		},
		{
			name: "context_regex_anchored_no_match_prefix",
			yaml: `
policies:
  - contexts: "^dev$"
    allow:
      resources: ["pods"]
      verbs: [get]
`,
			req:  policy.Request{Context: "dev-extra", Resource: pods(), Namespace: "default", Verb: policy.VerbGet},
			want: policy.NotGranted,
		},

		// ----------------------------------------------------------------
		// resource matching: "*", plural, GVK forms
		// ----------------------------------------------------------------
		{
			name: "resource_wildcard_matches_pods",
			yaml: `
policies:
  - contexts: ".*"
    allow:
      resources: ["*"]
      verbs: [get]
`,
			req:  policy.Request{Context: "dev", Resource: pods(), Namespace: "default", Verb: policy.VerbGet},
			want: policy.Allowed,
		},
		{
			name: "resource_wildcard_matches_deployments",
			yaml: `
policies:
  - contexts: ".*"
    allow:
      resources: ["*"]
      verbs: [get]
`,
			req:  policy.Request{Context: "dev", Resource: deployments(), Namespace: "default", Verb: policy.VerbGet},
			want: policy.Allowed,
		},
		{
			name: "resource_plural_matches_pods",
			yaml: `
policies:
  - contexts: ".*"
    allow:
      resources: ["pods"]
      verbs: [get]
`,
			req:  policy.Request{Context: "dev", Resource: pods(), Namespace: "default", Verb: policy.VerbGet},
			want: policy.Allowed,
		},
		{
			name: "resource_plural_does_not_match_deployments",
			yaml: `
policies:
  - contexts: ".*"
    allow:
      resources: ["pods"]
      verbs: [get]
`,
			req:  policy.Request{Context: "dev", Resource: deployments(), Namespace: "default", Verb: policy.VerbGet},
			want: policy.NotGranted,
		},
		{
			name: "resource_gvk_apps_v1_deployment",
			yaml: `
policies:
  - contexts: ".*"
    allow:
      resources: ["apps/v1/Deployment"]
      verbs: [get]
`,
			req:  policy.Request{Context: "dev", Resource: deployments(), Namespace: "default", Verb: policy.VerbGet},
			want: policy.Allowed,
		},
		{
			name: "resource_gvk_core_pod_empty_group",
			yaml: `
policies:
  - contexts: ".*"
    allow:
      resources: ["/v1/Pod"]
      verbs: [get]
`,
			req:  policy.Request{Context: "dev", Resource: pods(), Namespace: "default", Verb: policy.VerbGet},
			want: policy.Allowed,
		},
		{
			name: "resource_gvk_crd_widget",
			yaml: `
policies:
  - contexts: ".*"
    allow:
      resources: ["example.com/v1alpha1/Widget"]
      verbs: [get, list]
`,
			req:  policy.Request{Context: "dev", Resource: crdWidget(), Namespace: "default", Verb: policy.VerbList},
			want: policy.Allowed,
		},
		{
			name: "resource_gvk_does_not_match_wrong_kind",
			yaml: `
policies:
  - contexts: ".*"
    allow:
      resources: ["apps/v1/Deployment"]
      verbs: [get]
`,
			// pods != Deployment
			req:  policy.Request{Context: "dev", Resource: pods(), Namespace: "default", Verb: policy.VerbGet},
			want: policy.NotGranted,
		},

		// ----------------------------------------------------------------
		// resources defaulting: empty resources → ["*"]
		// ----------------------------------------------------------------
		{
			name: "resources_empty_defaults_to_wildcard_in_deny",
			yaml: `
policies:
  - contexts: ".*"
    deny:
      verbs: [exec]
`,
			req:  policy.Request{Context: "dev", Resource: pods(), Namespace: "default", Verb: policy.VerbExec},
			want: policy.ExplicitDeny,
		},
		{
			name: "resources_empty_defaults_to_wildcard_in_allow",
			yaml: `
policies:
  - contexts: ".*"
    allow:
      verbs: [get]
`,
			req:  policy.Request{Context: "dev", Resource: pods(), Namespace: "default", Verb: policy.VerbGet},
			want: policy.Allowed,
		},

		// ----------------------------------------------------------------
		// reserved verbs: watch and patch validate fine and can appear in policy
		// ----------------------------------------------------------------
		{
			name: "reserved_verb_watch_validates_ok_and_would_match",
			yaml: `
policies:
  - contexts: ".*"
    allow:
      resources: ["pods"]
      verbs: [get, list, watch]
`,
			// watch is reserved — no v1 tool emits it — but if a request
			// with that verb arrives it must be evaluated correctly.
			req:  policy.Request{Context: "dev", Resource: pods(), Namespace: "default", Verb: policy.VerbWatch},
			want: policy.Allowed,
		},
		{
			name: "reserved_verb_patch_validates_ok_and_would_match",
			yaml: `
policies:
  - contexts: ".*"
    allow:
      resources: ["pods"]
      verbs: [patch]
`,
			req:  policy.Request{Context: "dev", Resource: pods(), Namespace: "default", Verb: policy.VerbPatch},
			want: policy.Allowed,
		},

		// ----------------------------------------------------------------
		// design.md fixture (exact YAML from the Policy model section)
		// ----------------------------------------------------------------
		{
			name: "design_fixture_allow_get_kube_system_prod",
			yaml: designFixtureYAML,
			req:  policy.Request{Context: "gke_prod_1", Resource: pods(), Namespace: "kube-system", Verb: policy.VerbGet},
			want: policy.Allowed,
		},
		{
			name: "design_fixture_deny_exec_kube_system_prod",
			yaml: designFixtureYAML,
			// deny covers all resources (empty → ["*"]) with verb exec
			req:     policy.Request{Context: "gke_prod_1", Resource: pods(), Namespace: "kube-system", Verb: policy.VerbExec},
			want:    policy.ExplicitDeny,
			wantMsg: []string{"exec"},
		},
		{
			name: "design_fixture_no_match_staging_context",
			yaml: designFixtureYAML,
			req:  policy.Request{Context: "staging", Resource: pods(), Namespace: "kube-system", Verb: policy.VerbGet},
			want: policy.NotGranted,
		},
		{
			name: "design_fixture_no_match_different_ns",
			yaml: designFixtureYAML,
			req:  policy.Request{Context: "gke_prod_1", Resource: pods(), Namespace: "default", Verb: policy.VerbGet},
			want: policy.NotGranted,
		},
		{
			name: "design_fixture_cluster_scoped_node_in_prod_no_match",
			yaml: designFixtureYAML,
			// rule lists namespaces → cluster-scoped resources never match
			req:  policy.Request{Context: "gke_prod_1", Resource: nodes(), ClusterScoped: true, Verb: policy.VerbGet},
			want: policy.NotGranted,
		},

		// ----------------------------------------------------------------
		// explicit deny reason format
		// ----------------------------------------------------------------
		{
			name: "explicit_deny_reason_names_context_and_verb",
			yaml: `
policies:
  - contexts: ".*prod.*"
    deny:
      resources: ["*"]
      verbs: [exec]
`,
			req:     policy.Request{Context: "gke_prod_1", Resource: pods(), Namespace: "default", Verb: policy.VerbExec},
			want:    policy.ExplicitDeny,
			wantMsg: []string{".*prod.*", "exec"},
		},

		// ----------------------------------------------------------------
		// multiple verbs, only some allowed
		// ----------------------------------------------------------------
		{
			name: "allowed_verb_in_multi_verb_rule",
			yaml: `
policies:
  - contexts: ".*"
    allow:
      resources: ["pods"]
      verbs: [get, list, create]
`,
			req:  policy.Request{Context: "dev", Resource: pods(), Namespace: "default", Verb: policy.VerbCreate},
			want: policy.Allowed,
		},
		{
			name: "not_allowed_verb_not_in_rule",
			yaml: `
policies:
  - contexts: ".*"
    allow:
      resources: ["pods"]
      verbs: [get, list, create]
`,
			req:  policy.Request{Context: "dev", Resource: pods(), Namespace: "default", Verb: policy.VerbDelete},
			want: policy.NotGranted,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			eng := mustLoad(t, tc.yaml)
			dec := eng.Evaluate(tc.req)

			if dec.Outcome != tc.want {
				t.Errorf("Outcome = %v, want %v (Reason: %q)", dec.Outcome, tc.want, dec.Reason)
			}

			for _, sub := range tc.wantMsg {
				if !strings.Contains(dec.Reason, sub) {
					t.Errorf("Reason %q missing expected substring %q", dec.Reason, sub)
				}
			}

			// Decision.Allowed() must agree with Outcome
			gotAllowed := dec.Allowed()
			wantAllowed := tc.want == policy.Allowed

			if gotAllowed != wantAllowed {
				t.Errorf("Decision.Allowed() = %v, want %v", gotAllowed, wantAllowed)
			}
		})
	}
}
