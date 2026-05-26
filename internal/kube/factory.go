// SPDX-License-Identifier: Apache-2.0

package kube

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
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

	// Stale entries are intentionally never evicted; a session sees only a handful of distinct configs.
	f.clients[key] = c

	return c, nil
}

// fingerprint hashes the connection-identifying fields of a rest.Config so that
// any change to where or how we connect (endpoint, credentials, exec-plugin
// arguments, impersonation) produces a distinct cache key. Every string is
// length-prefixed and every list/optional block is count- or presence-prefixed,
// so the encoding is injective: distinct configs cannot collide.
func fingerprint(cfg *rest.Config) string {
	h := sha256.New()

	writeBytes := func(b []byte) {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(b)))
		h.Write(n[:])
		h.Write(b)
	}
	writeStr := func(s string) { writeBytes([]byte(s)) }
	writeCount := func(n int) {
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], uint64(uint(n))) //nolint:gosec // n is always a non-negative length/count
		h.Write(b[:])
	}

	tc := cfg.TLSClientConfig
	writeStr(cfg.Host)
	writeStr(strconv.FormatBool(tc.Insecure))
	writeStr(tc.ServerName)
	writeStr(tc.CAFile)
	writeStr(tc.CertFile)
	writeStr(tc.KeyFile)
	writeBytes(tc.CAData)
	writeBytes(tc.CertData)
	writeBytes(tc.KeyData)

	writeStr(cfg.Username)
	writeStr(cfg.Password)
	writeStr(cfg.BearerToken)
	writeStr(cfg.BearerTokenFile)

	writeStr(cfg.Impersonate.UserName)
	writeStr(cfg.Impersonate.UID)
	writeCount(len(cfg.Impersonate.Groups))
	for _, g := range cfg.Impersonate.Groups {
		writeStr(g)
	}
	extra := cfg.Impersonate.Extra
	writeCount(len(extra))
	for _, k := range sortedKeys(extra) {
		writeStr(k)
		writeCount(len(extra[k]))
		for _, v := range extra[k] {
			writeStr(v)
		}
	}

	if ap := cfg.AuthProvider; ap != nil {
		writeCount(1)
		writeStr(ap.Name)
		writeCount(len(ap.Config))
		for _, k := range sortedKeys(ap.Config) {
			writeStr(k)
			writeStr(ap.Config[k])
		}
	} else {
		writeCount(0)
	}

	if ep := cfg.ExecProvider; ep != nil {
		writeCount(1)
		writeStr(ep.Command)
		writeStr(ep.APIVersion)
		writeCount(len(ep.Args))
		for _, a := range ep.Args {
			writeStr(a)
		}
		writeCount(len(ep.Env))
		for _, e := range ep.Env {
			writeStr(e.Name)
			writeStr(e.Value)
		}
	} else {
		writeCount(0)
	}

	return hex.EncodeToString(h.Sum(nil))
}

// sortedKeys returns the keys of m in deterministic order so a map's hash
// contribution does not depend on Go's randomized map iteration.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
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
