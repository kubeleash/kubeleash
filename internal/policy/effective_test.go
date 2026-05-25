// SPDX-License-Identifier: Apache-2.0

package policy_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/kubeleash/kubeleash/internal/policy"
)

// TestEffectivePolicyNormalizesResourcesAndSortsVerbs checks that
// EffectivePolicy returns the normalized config: an omitted resources list is
// defaulted to ["*"], verbs are stably sorted, and a nil clause stays nil.
func TestEffectivePolicyNormalizesResourcesAndSortsVerbs(t *testing.T) {
	const src = `
policies:
  - contexts: ".*prod.*"
    namespaces: ["kube-system"]
    allow:
      verbs: [list, get]
    deny:
      verbs: [exec]
`

	eng, err := policy.Load(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	got := eng.EffectivePolicy()

	want := policy.Config{
		Policies: []policy.Rule{
			{
				Contexts:   ".*prod.*",
				Namespaces: []string{"kube-system"},
				Allow: &policy.RuleSet{
					Resources: []string{"*"}, // defaulted from empty
					Verbs:     []string{"get", "list"},
				},
				Deny: &policy.RuleSet{
					Resources: []string{"*"},
					Verbs:     []string{"exec"},
				},
			},
		},
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("EffectivePolicy() = %+v\nwant %+v", got, want)
	}
}

// TestEffectivePolicyNilDenyStaysNil verifies a rule with only an allow clause
// produces a nil Deny in the effective config (no spurious empty clause).
func TestEffectivePolicyNilDenyStaysNil(t *testing.T) {
	const src = `
policies:
  - contexts: "dev"
    allow:
      resources: ["pods"]
      verbs: [get]
`

	eng, err := policy.Load(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	got := eng.EffectivePolicy()
	if len(got.Policies) != 1 {
		t.Fatalf("got %d policies, want 1", len(got.Policies))
	}

	if got.Policies[0].Deny != nil {
		t.Errorf("Deny = %+v, want nil", got.Policies[0].Deny)
	}

	if got.Policies[0].Allow == nil {
		t.Fatal("Allow = nil, want non-nil")
	}
}
