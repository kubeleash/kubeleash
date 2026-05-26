// SPDX-License-Identifier: Apache-2.0

package kube

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Options configures a [Factory].
type Options struct {
	// KubeconfigPath is an explicit kubeconfig path. When empty, the standard
	// client-go loading rules are used (KUBECONFIG env, then ~/.kube/config).
	KubeconfigPath string
}

// Factory loads a kubeconfig once and hands out a per-context [Client]. It is
// safe for concurrent use. Per-context clients are built lazily and cached, so
// an agent can reach prod and dev clusters from a single Factory in one
// session.
type Factory struct {
	loader clientcmd.ClientConfigLoader

	mu      sync.Mutex
	clients map[string]Client
}

// NewFactory builds a Factory from opts. It does not contact any cluster.
func NewFactory(opts Options) (*Factory, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	if opts.KubeconfigPath != "" {
		rules.ExplicitPath = opts.KubeconfigPath
	}

	return &Factory{
		loader:  rules,
		clients: make(map[string]Client),
	}, nil
}

// Client returns a [Client] scoped to the named context. An empty contextName
// uses the kubeconfig's current-context. The kubeconfig is re-read on every call
// and the cache is keyed on a fingerprint of the resolved connection identity, so
// an out-of-band kubeconfig change (new port, rotated credentials) yields a fresh
// client instead of a stale cached one.
func (f *Factory) Client(contextName string) (Client, error) {
	cfg, resolvedName, err := f.restConfig(contextName)
	if err != nil {
		return nil, err
	}

	key := resolvedName + "\x00" + fingerprint(cfg)

	f.mu.Lock()
	defer f.mu.Unlock()

	if c, ok := f.clients[key]; ok {
		return c, nil
	}

	c, err := newClient(resolvedName, cfg)
	if err != nil {
		return nil, err
	}

	f.clients[key] = c

	return c, nil
}

// fingerprint hashes the connection-identifying fields of a rest.Config so that
// any change to where/how we connect produces a distinct cache key.
func fingerprint(cfg *rest.Config) string {
	h := sha256.New()
	tc := cfg.TLSClientConfig

	fmt.Fprintf(h, "%s\x00%t\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00",
		cfg.Host, tc.Insecure, tc.ServerName,
		tc.CAFile, tc.CertFile, tc.KeyFile,
		cfg.BearerToken, cfg.BearerTokenFile,
	)
	fmt.Fprintf(h, "%s\x00", cfg.Username)

	for _, b := range [][]byte{tc.CAData, tc.CertData, tc.KeyData} {
		h.Write(b)
		h.Write([]byte{0})
	}

	if cfg.AuthProvider != nil {
		fmt.Fprintf(h, "ap:%s\x00", cfg.AuthProvider.Name)
	}
	if cfg.ExecProvider != nil {
		fmt.Fprintf(h, "ep:%s\x00", cfg.ExecProvider.Command)
	}

	return hex.EncodeToString(h.Sum(nil))
}

// ResolveContext returns the concrete context name for contextName, defaulting
// to the kubeconfig current-context when contextName is "". It reads the
// kubeconfig but builds no client and contacts no cluster, so callers that only
// need the name (e.g. policy introspection) stay cluster-free.
func (f *Factory) ResolveContext(contextName string) (string, error) {
	overrides := &clientcmd.ConfigOverrides{}
	if contextName != "" {
		overrides.CurrentContext = contextName
	}

	rawCfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(f.loader, overrides).RawConfig()
	if err != nil {
		return "", fmt.Errorf("kube: load kubeconfig: %w", err)
	}

	resolved := contextName
	if resolved == "" {
		resolved = rawCfg.CurrentContext
	}
	if resolved == "" {
		return "", fmt.Errorf("kube: no context specified and kubeconfig has no current-context")
	}
	if _, ok := rawCfg.Contexts[resolved]; !ok {
		return "", fmt.Errorf("kube: context %q not found in kubeconfig", resolved)
	}

	return resolved, nil
}

// restConfig resolves contextName against the loaded kubeconfig and returns a
// *rest.Config plus the concrete context name that was used.
func (f *Factory) restConfig(contextName string) (*rest.Config, string, error) {
	overrides := &clientcmd.ConfigOverrides{}
	if contextName != "" {
		overrides.CurrentContext = contextName
	}

	clientConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(f.loader, overrides)

	rawCfg, err := clientConfig.RawConfig()
	if err != nil {
		return nil, "", fmt.Errorf("kube: load kubeconfig: %w", err)
	}

	resolvedName := contextName
	if resolvedName == "" {
		resolvedName = rawCfg.CurrentContext
	}

	if resolvedName == "" {
		return nil, "", fmt.Errorf("kube: no context specified and kubeconfig has no current-context")
	}

	if _, ok := rawCfg.Contexts[resolvedName]; !ok {
		return nil, "", fmt.Errorf("kube: context %q not found in kubeconfig", resolvedName)
	}

	restCfg, err := clientConfig.ClientConfig()
	if err != nil {
		return nil, "", fmt.Errorf("kube: build rest config for context %q: %w", resolvedName, err)
	}

	return restCfg, resolvedName, nil
}
