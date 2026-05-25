// SPDX-License-Identifier: Apache-2.0

// Package policy implements kubeleash's context-scoped allow/deny policy
// engine. It is intentionally pure — it has no Kubernetes client imports and
// performs zero cluster I/O. The kube layer resolves GVK and scope and passes
// them in via [Request].
//
// See docs/design.md "Policy model" for the authoritative specification.
package policy

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ---------------------------------------------------------------------------
// Verb — known set
// ---------------------------------------------------------------------------

// Verb is a policy verb from the known set.
type Verb string

// Known verbs. watch and patch are reserved (accepted but no v1 tool emits
// them) and are forward-compatible placeholders.
const (
	VerbGet    Verb = "get"
	VerbList   Verb = "list"
	VerbCreate Verb = "create"
	VerbUpdate Verb = "update"
	VerbDelete Verb = "delete"
	VerbLogs   Verb = "logs"
	VerbExec   Verb = "exec"
	VerbScale  Verb = "scale"
	VerbWatch  Verb = "watch"
	VerbPatch  Verb = "patch"
)

// knownVerbs is the complete set of valid verbs (including reserved ones).
var knownVerbs = map[Verb]struct{}{
	VerbGet:    {},
	VerbList:   {},
	VerbCreate: {},
	VerbUpdate: {},
	VerbDelete: {},
	VerbLogs:   {},
	VerbExec:   {},
	VerbScale:  {},
	VerbWatch:  {},
	VerbPatch:  {},
}

// ---------------------------------------------------------------------------
// Config types (YAML-tagged)
// ---------------------------------------------------------------------------

// Config is the top-level parsed policy document.
type Config struct {
	Policies []Rule `yaml:"policies"`
}

// Rule is one entry in the policies list.
type Rule struct {
	Contexts   string   `yaml:"contexts"`
	Namespaces []string `yaml:"namespaces"`
	Allow      *RuleSet `yaml:"allow"`
	Deny       *RuleSet `yaml:"deny"`
}

// RuleSet holds the resource + verb constraints for an allow or deny clause.
type RuleSet struct {
	Resources []string `yaml:"resources"`
	Verbs     []string `yaml:"verbs"`
}

// ---------------------------------------------------------------------------
// Resource — fully qualified resource descriptor
// ---------------------------------------------------------------------------

// Resource identifies a Kubernetes resource fully. The kube layer fills this
// in; policy evaluation is purely based on these fields.
type Resource struct {
	Group   string
	Version string
	Kind    string
	Plural  string
}

// ---------------------------------------------------------------------------
// Request — everything policy.Evaluate needs
// ---------------------------------------------------------------------------

// Request carries the context-resolved values for a single evaluation.
type Request struct {
	Context       string
	Resource      Resource
	Namespace     string // "" if unset or cluster-scoped
	ClusterScoped bool
	Verb          Verb
}

// ---------------------------------------------------------------------------
// Outcome + Decision
// ---------------------------------------------------------------------------

// Outcome is the result of a policy evaluation.
type Outcome int

const (
	// NotGranted is the zero value: absent any explicit decision, nothing is
	// granted. This is intentional — a zero-value Decision must be denied so
	// that an uninitialised or accidentally-dropped Decision fails safe
	// (default-deny, never fail-open).
	NotGranted Outcome = iota
	// Allowed means an allow rule covered the request and no deny rule fired.
	Allowed
	// ExplicitDeny means a deny rule fired; the agent should not retry.
	ExplicitDeny
)

// Decision is the full result of [Engine.Evaluate].
type Decision struct {
	Outcome Outcome
	// Reason is a human-readable explanation. For ExplicitDeny it names the
	// firing context regex and verb. For NotGranted it names the resource,
	// verb, and context.
	Reason string
}

// Allowed returns true only when Outcome is [Allowed].
func (d Decision) Allowed() bool {
	return d.Outcome == Allowed
}

// ---------------------------------------------------------------------------
// compiled internal representation
// ---------------------------------------------------------------------------

// compiledRule is a validated, compiled version of [Rule].
type compiledRule struct {
	contextsRaw string
	contextsRe  *regexp.Regexp
	namespaces  []string // nil/empty = match all
	allow       *compiledRuleSet
	deny        *compiledRuleSet
}

// compiledRuleSet is a validated [RuleSet] with verbs stored in a set.
type compiledRuleSet struct {
	resources []string // after defaulting empty → ["*"]
	verbs     map[Verb]struct{}
}

// ---------------------------------------------------------------------------
// Engine
// ---------------------------------------------------------------------------

// Engine holds the compiled, validated rules. Use [Load] or [LoadFile] to
// create one.
type Engine struct {
	rules []compiledRule
}

// ---------------------------------------------------------------------------
// Load / LoadFile
// ---------------------------------------------------------------------------

// Load parses and validates a policy from r. It returns an error (and a nil
// *Engine) if the YAML is malformed or the policy fails validation. A bad
// policy MUST NOT produce a usable engine (fail-fast, never fail-open).
func Load(r io.Reader) (*Engine, error) {
	var cfg Config

	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)

	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("policy: parse error: %w", err)
	}

	rules, err := compile(cfg)
	if err != nil {
		return nil, err
	}

	return &Engine{rules: rules}, nil
}

// LoadFile is a convenience wrapper that opens path and calls [Load].
// The path is supplied by the operator at startup and is not user-controlled.
//
//nolint:gosec // G304: path is an operator-supplied config path, not user input.
func LoadFile(path string) (*Engine, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("policy: open %q: %w", path, err)
	}

	defer func() {
		// Error closing a read-only file is non-actionable; log suppression is
		// intentional — we already have the data we need.
		_ = f.Close()
	}()

	return Load(f)
}

// ---------------------------------------------------------------------------
// Compilation + validation
// ---------------------------------------------------------------------------

func compile(cfg Config) ([]compiledRule, error) {
	out := make([]compiledRule, 0, len(cfg.Policies))

	for i, r := range cfg.Policies {
		cr, err := compileRule(i, r)
		if err != nil {
			return nil, err
		}

		out = append(out, cr)
	}

	return out, nil
}

func compileRule(i int, r Rule) (compiledRule, error) {
	// contexts must be non-empty and compilable.
	if r.Contexts == "" {
		return compiledRule{}, fmt.Errorf("policy: rule %d: contexts must not be empty", i)
	}

	re, err := regexp.Compile(r.Contexts)
	if err != nil {
		return compiledRule{}, fmt.Errorf("policy: rule %d: contexts %q is not a valid regexp: %w", i, r.Contexts, err)
	}

	// At least one of allow or deny must be present.
	if r.Allow == nil && r.Deny == nil {
		return compiledRule{}, fmt.Errorf("policy: rule %d: must have at least one of allow or deny", i)
	}

	var allow, deny *compiledRuleSet

	if r.Allow != nil {
		allow, err = compileRuleSet(i, "allow", r.Allow)
		if err != nil {
			return compiledRule{}, err
		}
	}

	if r.Deny != nil {
		deny, err = compileRuleSet(i, "deny", r.Deny)
		if err != nil {
			return compiledRule{}, err
		}
	}

	return compiledRule{
		contextsRaw: r.Contexts,
		contextsRe:  re,
		namespaces:  r.Namespaces,
		allow:       allow,
		deny:        deny,
	}, nil
}

func compileRuleSet(ruleIdx int, kind string, rs *RuleSet) (*compiledRuleSet, error) {
	if len(rs.Verbs) == 0 {
		return nil, fmt.Errorf("policy: rule %d: %s.verbs must not be empty", ruleIdx, kind)
	}

	verbSet := make(map[Verb]struct{}, len(rs.Verbs))

	for _, v := range rs.Verbs {
		vv := Verb(v)
		if _, ok := knownVerbs[vv]; !ok {
			return nil, fmt.Errorf("policy: rule %d: unknown verb %q in %s", ruleIdx, v, kind)
		}

		verbSet[vv] = struct{}{}
	}

	resources := rs.Resources
	if len(resources) == 0 {
		resources = []string{"*"}
	}

	return &compiledRuleSet{
		resources: resources,
		verbs:     verbSet,
	}, nil
}

// ---------------------------------------------------------------------------
// Evaluate
// ---------------------------------------------------------------------------

// Evaluate applies the deny-wins, default-deny algorithm specified in
// docs/design.md against req and returns a [Decision].
func (e *Engine) Evaluate(req Request) Decision {
	// Step 1: collect matching rules.
	var matched []compiledRule

	for _, r := range e.rules {
		if ruleMatches(r, req) {
			matched = append(matched, r)
		}
	}

	// Step 2: explicit deny wins.
	for _, r := range matched {
		if r.deny != nil && ruleSetCovers(r.deny, req) {
			return Decision{
				Outcome: ExplicitDeny,
				Reason: fmt.Sprintf(
					"denied by deny rule for context %q, verb %q",
					r.contextsRaw,
					req.Verb,
				),
			}
		}
	}

	// Step 3: check allow.
	for _, r := range matched {
		if r.allow != nil && ruleSetCovers(r.allow, req) {
			return Decision{
				Outcome: Allowed,
				Reason:  fmt.Sprintf("allowed by rule for context %q", r.contextsRaw),
			}
		}
	}

	// Step 4: default deny (not granted).
	return Decision{
		Outcome: NotGranted,
		Reason: fmt.Sprintf(
			"not granted: no allow rule matches resource %q verb %q in context %q",
			req.Resource.Plural,
			req.Verb,
			req.Context,
		),
	}
}

// ---------------------------------------------------------------------------
// EffectivePolicy — normalized rules for introspection / --print-effective-policy
// ---------------------------------------------------------------------------

// EffectivePolicy returns the resolved, normalized policy as a [Config]: the
// rules exactly as the engine evaluates them, after defaulting (e.g. an omitted
// resources list becomes ["*"]). It is a pure read over the compiled rules and
// performs no cluster I/O. Verbs are emitted in a stable, sorted order so the
// output is deterministic. This powers --print-effective-policy, letting an
// operator confirm what the engine actually enforces rather than re-reading the
// raw file.
func (e *Engine) EffectivePolicy() Config {
	cfg := Config{Policies: make([]Rule, 0, len(e.rules))}

	for _, r := range e.rules {
		rule := Rule{
			Contexts:   r.contextsRaw,
			Namespaces: r.namespaces,
			Allow:      effectiveRuleSet(r.allow),
			Deny:       effectiveRuleSet(r.deny),
		}
		cfg.Policies = append(cfg.Policies, rule)
	}

	return cfg
}

// effectiveRuleSet renders a compiled rule set back into an exported [RuleSet]
// with normalized resources and stably-sorted verbs. A nil input yields nil.
func effectiveRuleSet(rs *compiledRuleSet) *RuleSet {
	if rs == nil {
		return nil
	}

	verbs := make([]string, 0, len(rs.verbs))
	for v := range rs.verbs {
		verbs = append(verbs, string(v))
	}

	sort.Strings(verbs)

	return &RuleSet{
		Resources: rs.resources,
		Verbs:     verbs,
	}
}

// ---------------------------------------------------------------------------
// Capabilities — read-only policy introspection (no cluster I/O)
// ---------------------------------------------------------------------------

// Capabilities returns a human-readable summary of the actions the policy may
// allow in the given context. It is a pure read over the compiled rules — it
// performs no evaluation against a concrete resource and never touches a
// cluster. It is intended to power the k8s_capabilities tool so an agent can
// ask "what am I allowed to do here?" up front.
//
// Each returned line describes one allow clause whose context regex matches
// ctx, listing its namespace narrowing (if any), resources and verbs, and
// flags any deny clause in the same rule that subtracts verbs. Because the full
// allow/deny interaction is resource- and scope-dependent, the summary is
// advisory: the authoritative answer for a specific call is always Evaluate.
func (e *Engine) Capabilities(ctx string) []string {
	var out []string

	for _, r := range e.rules {
		if !r.contextsRe.MatchString(ctx) {
			continue
		}

		scope := "all namespaces + cluster-scoped"
		if len(r.namespaces) > 0 {
			scope = "namespaces " + strings.Join(r.namespaces, ",")
		}

		if r.allow != nil {
			line := fmt.Sprintf(
				"allow: resources [%s] verbs [%s] (%s)",
				strings.Join(r.allow.resources, ","),
				joinVerbs(r.allow.verbs),
				scope,
			)
			out = append(out, line)
		}

		if r.deny != nil {
			line := fmt.Sprintf(
				"deny: resources [%s] verbs [%s] (%s)",
				strings.Join(r.deny.resources, ","),
				joinVerbs(r.deny.verbs),
				scope,
			)
			out = append(out, line)
		}
	}

	return out
}

// joinVerbs renders a verb set as a stable, comma-separated string.
func joinVerbs(set map[Verb]struct{}) string {
	verbs := make([]string, 0, len(set))
	for v := range set {
		verbs = append(verbs, string(v))
	}

	sort.Strings(verbs)

	return strings.Join(verbs, ",")
}

// ruleMatches reports whether r applies to req per the namespace-axis rules:
//   - namespaces omitted/empty → matches all (namespaced in any ns, and cluster-scoped).
//   - namespaces non-empty → matches only if !req.ClusterScoped AND req.Namespace is listed.
func ruleMatches(r compiledRule, req Request) bool {
	if !r.contextsRe.MatchString(req.Context) {
		return false
	}

	if len(r.namespaces) == 0 {
		// Matches everything: any namespace, and cluster-scoped resources.
		return true
	}

	// Rule specifies namespaces → cluster-scoped resources never match.
	if req.ClusterScoped {
		return false
	}

	for _, ns := range r.namespaces {
		if ns == req.Namespace {
			return true
		}
	}

	return false
}

// ruleSetCovers reports whether rs covers (resource, verb) for req.
func ruleSetCovers(rs *compiledRuleSet, req Request) bool {
	if _, ok := rs.verbs[req.Verb]; !ok {
		return false
	}

	for _, entry := range rs.resources {
		if resourceEntryMatches(entry, req.Resource) {
			return true
		}
	}

	return false
}

// resourceEntryMatches reports whether the single policy resource entry string
// matches the given resource descriptor.
//
// Matching rules (from docs/design.md):
//   - "*" matches anything.
//   - If entry contains "/" it is compared to "group/version/kind"
//     (core group uses empty group, so "/v1/Pod").
//   - Otherwise compare to resource.Plural.
func resourceEntryMatches(entry string, res Resource) bool {
	if entry == "*" {
		return true
	}

	if strings.Contains(entry, "/") {
		// GVK form: group/version/kind
		gvk := res.Group + "/" + res.Version + "/" + res.Kind
		return entry == gvk
	}

	// Plural form.
	return entry == res.Plural
}
