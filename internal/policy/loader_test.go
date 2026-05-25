// SPDX-License-Identifier: Apache-2.0

package policy_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kubeleash/kubeleash/internal/policy"
)

func TestLoad_ValidationErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		yaml        string
		wantErrSubs []string // substrings expected in error message
	}{
		{
			name: "empty_contexts_field",
			yaml: `
policies:
  - contexts: ""
    allow:
      resources: ["pods"]
      verbs: [get]
`,
			wantErrSubs: []string{"contexts", "rule 0"},
		},
		{
			name: "bad_regex_in_contexts",
			yaml: `
policies:
  - contexts: "["
    allow:
      resources: ["pods"]
      verbs: [get]
`,
			wantErrSubs: []string{"rule 0", "contexts"},
		},
		{
			name: "unknown_verb_in_allow",
			yaml: `
policies:
  - contexts: ".*"
    allow:
      resources: ["pods"]
      verbs: [get, frobnicate]
`,
			wantErrSubs: []string{"rule 0", "frobnicate"},
		},
		{
			name: "unknown_verb_in_deny",
			yaml: `
policies:
  - contexts: ".*"
    deny:
      resources: ["pods"]
      verbs: [nuke]
`,
			wantErrSubs: []string{"rule 0", "nuke"},
		},
		{
			name: "rule_with_no_allow_or_deny",
			yaml: `
policies:
  - contexts: ".*"
`,
			wantErrSubs: []string{"rule 0", "allow", "deny"},
		},
		{
			name: "allow_ruleset_with_empty_verbs",
			yaml: `
policies:
  - contexts: ".*"
    allow:
      resources: ["pods"]
      verbs: []
`,
			wantErrSubs: []string{"rule 0", "verbs"},
		},
		{
			name: "deny_ruleset_with_empty_verbs",
			yaml: `
policies:
  - contexts: ".*"
    deny:
      resources: ["pods"]
      verbs: []
`,
			wantErrSubs: []string{"rule 0", "verbs"},
		},
		{
			name: "second_rule_has_bad_verb",
			yaml: `
policies:
  - contexts: ".*"
    allow:
      resources: ["pods"]
      verbs: [get]
  - contexts: ".*prod.*"
    deny:
      verbs: [badverb]
`,
			wantErrSubs: []string{"rule 1", "badverb"},
		},
		{
			name: "invalid_yaml_syntax",
			yaml: `
policies:
  - contexts: [unclosed
`,
			wantErrSubs: []string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			eng, err := policy.Load(strings.NewReader(tc.yaml))
			if err == nil {
				t.Fatal("Load() expected error but got nil")
			}

			if eng != nil {
				t.Error("Load() returned non-nil engine on error — must return nil engine on validation failure")
			}

			for _, sub := range tc.wantErrSubs {
				if !strings.Contains(err.Error(), sub) {
					t.Errorf("error %q missing expected substring %q", err.Error(), sub)
				}
			}
		})
	}
}

func TestLoad_ValidPolicies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		yaml string
	}{
		{
			name: "minimal_allow",
			yaml: `
policies:
  - contexts: ".*"
    allow:
      resources: ["pods"]
      verbs: [get]
`,
		},
		{
			name: "minimal_deny",
			yaml: `
policies:
  - contexts: ".*"
    deny:
      verbs: [exec]
`,
		},
		{
			name: "reserved_verbs_watch_and_patch_are_valid",
			yaml: `
policies:
  - contexts: ".*"
    allow:
      resources: ["*"]
      verbs: [get, list, watch, patch]
`,
		},
		{
			name: "all_known_verbs",
			yaml: `
policies:
  - contexts: ".*"
    allow:
      verbs: [get, list, create, update, delete, logs, exec, scale, watch, patch]
`,
		},
		{
			name: "design_fixture",
			yaml: designFixtureYAML,
		},
		{
			name: "empty_resources_defaults_to_wildcard",
			yaml: `
policies:
  - contexts: ".*"
    deny:
      verbs: [exec]
`,
		},
		{
			name: "multiple_rules",
			yaml: `
policies:
  - contexts: ".*"
    allow:
      resources: ["pods"]
      verbs: [get, list]
  - contexts: ".*prod.*"
    namespaces: ["kube-system"]
    deny:
      verbs: [exec, delete]
`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			eng, err := policy.Load(strings.NewReader(tc.yaml))
			if err != nil {
				t.Fatalf("Load() unexpected error: %v", err)
			}

			if eng == nil {
				t.Fatal("Load() returned nil engine on valid policy")
			}
		})
	}
}

func TestLoadFile(t *testing.T) {
	t.Parallel()

	t.Run("valid_file", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		path := filepath.Join(dir, "policy.yaml")

		if err := os.WriteFile(path, []byte(designFixtureYAML), 0o600); err != nil {
			t.Fatal(err)
		}

		eng, err := policy.LoadFile(path)
		if err != nil {
			t.Fatalf("LoadFile() unexpected error: %v", err)
		}

		if eng == nil {
			t.Fatal("LoadFile() returned nil engine")
		}
	})

	t.Run("missing_file", func(t *testing.T) {
		t.Parallel()

		eng, err := policy.LoadFile("/no/such/file/policy.yaml")
		if err == nil {
			t.Fatal("LoadFile() expected error for missing file")
		}

		if eng != nil {
			t.Error("LoadFile() returned non-nil engine on error")
		}
	})

	t.Run("invalid_policy_file", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		path := filepath.Join(dir, "policy.yaml")
		bad := `policies:
  - contexts: ""
    allow:
      verbs: [get]`

		if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
			t.Fatal(err)
		}

		eng, err := policy.LoadFile(path)
		if err == nil {
			t.Fatal("LoadFile() expected error for invalid policy")
		}

		if eng != nil {
			t.Error("LoadFile() returned non-nil engine on error")
		}
	})
}
