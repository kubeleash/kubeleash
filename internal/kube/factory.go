// SPDX-License-Identifier: Apache-2.0

package kube

import (
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
// uses the kubeconfig's current-context. Built clients are cached per resolved
// context key.
func (f *Factory) Client(contextName string) (Client, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if c, ok := f.clients[contextName]; ok {
		return c, nil
	}

	cfg, resolvedName, err := f.restConfig(contextName)
	if err != nil {
		return nil, err
	}

	c, err := newClient(resolvedName, cfg)
	if err != nil {
		return nil, err
	}

	// Cache under both the requested key (possibly "") and the resolved name so
	// repeat lookups by either form hit the cache.
	f.clients[contextName] = c
	f.clients[resolvedName] = c

	return c, nil
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
