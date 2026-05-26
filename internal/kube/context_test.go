// SPDX-License-Identifier: Apache-2.0

package kube_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kubeleash/kubeleash/internal/kube"
)

const twoContextKubeconfig = `apiVersion: v1
kind: Config
current-context: ctx-a
clusters:
  - name: c1
    cluster:
      server: https://example.test:6443
contexts:
  - name: ctx-a
    context: {cluster: c1, user: u1}
  - name: ctx-b
    context: {cluster: c1, user: u1}
users:
  - name: u1
    user: {}
`

// ResolveContext and Client().Context() must resolve "" to the kubeconfig
// current-context and validate named contexts — without contacting a cluster.
func TestResolveContextAndClientContext(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "kubeconfig")
	if err := os.WriteFile(path, []byte(twoContextKubeconfig), 0o600); err != nil {
		t.Fatalf("write kubeconfig: %v", err)
	}

	f, err := kube.NewFactory(kube.Options{KubeconfigPath: path})
	if err != nil {
		t.Fatalf("NewFactory: %v", err)
	}

	t.Run("ResolveContext", func(t *testing.T) {
		cases := []struct {
			in, want string
			wantErr  bool
		}{
			{in: "", want: "ctx-a"},      // defaults to current-context
			{in: "ctx-b", want: "ctx-b"}, // explicit
			{in: "nope", wantErr: true},  // not in kubeconfig
		}
		for _, c := range cases {
			got, err := f.ResolveContext(c.in)
			if c.wantErr {
				if err == nil {
					t.Errorf("ResolveContext(%q): want error, got %q", c.in, got)
				}
				continue
			}
			if err != nil {
				t.Errorf("ResolveContext(%q): %v", c.in, err)
			}
			if got != c.want {
				t.Errorf("ResolveContext(%q) = %q, want %q", c.in, got, c.want)
			}
		}
	})

	t.Run("Client reports resolved context", func(t *testing.T) {
		// Building a client performs no cluster I/O, so an unreachable server is
		// fine here — we only assert the resolved context name.
		c, err := f.Client("") // -> current-context ctx-a
		if err != nil {
			t.Fatalf("Client(\"\"): %v", err)
		}
		if c.Context() != "ctx-a" {
			t.Errorf("Client(\"\").Context() = %q, want ctx-a", c.Context())
		}

		c2, err := f.Client("ctx-b")
		if err != nil {
			t.Fatalf("Client(ctx-b): %v", err)
		}
		if c2.Context() != "ctx-b" {
			t.Errorf("Client(ctx-b).Context() = %q, want ctx-b", c2.Context())
		}
	})
}
