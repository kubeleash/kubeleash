// SPDX-License-Identifier: Apache-2.0

package kube

import (
	"testing"

	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"k8s.io/client-go/rest"
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
			Impersonate: rest.ImpersonationConfig{UserName: "imp1", Groups: []string{"g1"}},
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
		"host":         func(c *rest.Config) { c.Host = "https://other.test:6443" },
		"password":     func(c *rest.Config) { c.Password = "pw2" },
		"caData":       func(c *rest.Config) { c.TLSClientConfig.CAData = []byte("ca2") },
		"impersonate":  func(c *rest.Config) { c.Impersonate.UserName = "imp2" },
		"authProvider": func(c *rest.Config) { c.AuthProvider.Config = map[string]string{"k": "v2"} },
		"execArgs":     func(c *rest.Config) { c.ExecProvider.Args = []string{"--region", "eu-west-1"} },
		"execEnv":      func(c *rest.Config) { c.ExecProvider.Env = []clientcmdapi.ExecEnvVar{{Name: "PROFILE", Value: "p2"}} },
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
