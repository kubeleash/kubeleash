// SPDX-License-Identifier: Apache-2.0

package kube

import (
	"testing"

	"k8s.io/client-go/rest"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// fingerprint must change when any connection-identity field changes, otherwise
// the Factory would hand back a client pinned to stale credentials/endpoint.
func TestFingerprintDistinguishesIdentityFields(t *testing.T) {
	base := func() *rest.Config {
		return &rest.Config{
			Host:     "https://example.test:6443",
			Username: "alice",
			Password: "pw1",
			TLSClientConfig: rest.TLSClientConfig{
				CAData:   []byte("ca"),
				CertData: []byte("cert"),
				KeyData:  []byte("key"),
			},
			Impersonate: rest.ImpersonationConfig{
				UserName: "imp1",
				Groups:   []string{"g1"},
				Extra:    map[string][]string{"scopes": {"a"}},
			},
			AuthProvider: &clientcmdapi.AuthProviderConfig{
				Name:   "oidc",
				Config: map[string]string{"k": "v1"},
			},
			ExecProvider: &clientcmdapi.ExecConfig{
				Command: "aws",
				Args:    []string{"--region", "us-east-1"},
				Env:     []clientcmdapi.ExecEnvVar{{Name: "PROFILE", Value: "p1"}},
			},
		}
	}

	ref := fingerprint(base())

	// Identical config -> identical fingerprint.
	if got := fingerprint(base()); got != ref {
		t.Fatalf("identical config produced different fingerprints: %s vs %s", ref, got)
	}

	mutations := map[string]func(*rest.Config){
		"host":             func(c *rest.Config) { c.Host = "https://other.test:6443" },
		"password":         func(c *rest.Config) { c.Password = "pw2" },
		"caData":           func(c *rest.Config) { c.CAData = []byte("ca2") },
		"impersonate":      func(c *rest.Config) { c.Impersonate.UserName = "imp2" },
		"authProvider":     func(c *rest.Config) { c.AuthProvider.Config = map[string]string{"k": "v2"} },
		"execArgs":         func(c *rest.Config) { c.ExecProvider.Args = []string{"--region", "eu-west-1"} },
		"execEnv":          func(c *rest.Config) { c.ExecProvider.Env = []clientcmdapi.ExecEnvVar{{Name: "PROFILE", Value: "p2"}} },
		"execCommand":      func(c *rest.Config) { c.ExecProvider.Command = "gke-gcloud-auth-plugin" },
		"impersonateUID":   func(c *rest.Config) { c.Impersonate.UID = "uid2" },
		"groups":           func(c *rest.Config) { c.Impersonate.Groups = []string{"g1", "g2"} },
		"authProviderName": func(c *rest.Config) { c.AuthProvider.Name = "azure" },
		"impersonateExtra": func(c *rest.Config) { c.Impersonate.Extra = map[string][]string{"scopes": {"b"}} },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			cfg := base()
			mutate(cfg)
			if got := fingerprint(cfg); got == ref {
				t.Errorf("changing %s did not change the fingerprint (stale-client risk)", name)
			}
		})
	}
}

// Moving a value between ExecProvider.Args and ExecProvider.Env must change the
// fingerprint: the two lists must be framed, not concatenated. Regression test
// for an aws-eks-style kubeconfig collision.
func TestFingerprintFramesExecArgsAndEnvSeparately(t *testing.T) {
	withArgs := &rest.Config{ExecProvider: &clientcmdapi.ExecConfig{
		Command: "aws",
		Args:    []string{"X", "Y", "Z"},
	}}
	withEnv := &rest.Config{ExecProvider: &clientcmdapi.ExecConfig{
		Command: "aws",
		Args:    []string{"X"},
		Env:     []clientcmdapi.ExecEnvVar{{Name: "Y", Value: "Z"}},
	}}
	if fingerprint(withArgs) == fingerprint(withEnv) {
		t.Fatal("Args and Env are not framed separately: distinct configs collide")
	}
}
