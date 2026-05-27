// SPDX-License-Identifier: Apache-2.0

package kube_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/kubeleash/kubeleash/internal/kube"
)

func kubeconfigWithPort(port int) string {
	return fmt.Sprintf(`apiVersion: v1
kind: Config
current-context: ctx-a
clusters:
  - name: c1
    cluster:
      server: https://127.0.0.1:%d
contexts:
  - name: ctx-a
    context: {cluster: c1, user: u1}
users:
  - name: u1
    user: {}
`, port)
}

// A kubeconfig edited under a running Factory (e.g. a kind cluster recreated on a
// new port) must yield a fresh client; an unchanged kubeconfig must reuse the
// cached one. Building clients performs no cluster I/O, so unreachable ports are
// fine here.
func TestClientCacheTracksKubeconfigChanges(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "kubeconfig")
	write := func(contents string) {
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatalf("write kubeconfig: %v", err)
		}
	}

	write(kubeconfigWithPort(6443))
	f, err := kube.NewFactory(kube.Options{KubeconfigPath: path})
	if err != nil {
		t.Fatalf("NewFactory: %v", err)
	}

	c1, err := f.Client("ctx-a")
	if err != nil {
		t.Fatalf("Client first: %v", err)
	}
	c2, err := f.Client("ctx-a")
	if err != nil {
		t.Fatalf("Client second: %v", err)
	}
	if c1 != c2 {
		t.Fatalf("unchanged kubeconfig: want cached client reuse, got distinct instances")
	}

	write(kubeconfigWithPort(7443)) // simulate cluster recreate on a new port
	c3, err := f.Client("ctx-a")
	if err != nil {
		t.Fatalf("Client after change: %v", err)
	}
	if c3 == c1 {
		t.Fatalf("changed kubeconfig: want fresh client, got stale cached instance")
	}
}
