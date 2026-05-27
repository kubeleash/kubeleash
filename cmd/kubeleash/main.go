// SPDX-License-Identifier: Apache-2.0
//
// Command kubeleash is a Kubernetes MCP server that enforces RBAC-style,
// context-scoped access control for AI agents. See docs/ for the design.
//
// It parses flags, loads a required policy (default-deny: it refuses to start
// without one), builds the kube client factory and audit logger, and serves the
// MCP tool surface over stdio until the client disconnects or it receives
// SIGINT/SIGTERM. stdout is reserved exclusively for the MCP transport; all
// diagnostics go to stderr.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gopkg.in/yaml.v3"

	"github.com/kubeleash/kubeleash/internal/audit"
	"github.com/kubeleash/kubeleash/internal/kube"
	intmcp "github.com/kubeleash/kubeleash/internal/mcp"
	"github.com/kubeleash/kubeleash/internal/policy"
)

// Build metadata. GoReleaser injects these via -ldflags
// "-X main.version=... -X main.commit=... -X main.date=...". The names MUST stay
// exactly main.version / main.commit / main.date.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// policyEnvVar is the environment fallback for the policy path when --policy is
// not given.
const policyEnvVar = "K8S_MCP_POLICY"

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "kubeleash: %v\n", err)
		os.Exit(1)
	}
}

// run is the testable entrypoint seam. It parses args, handles the
// exit-early flags (--version, --print-effective-policy), and otherwise builds
// the layers and serves over stdio until ctx is cancelled or the client
// disconnects. stdout is written to ONLY for the explicit --version /
// --print-effective-policy output; everything else goes to stderr.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("kubeleash", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var (
		policyPath      = fs.String("policy", "", "path to the policy file (required; or set "+policyEnvVar+")")
		kubeconfig      = fs.String("kubeconfig", "", "explicit kubeconfig path (empty = standard client-go loading rules)")
		dryRun          = fs.Bool("dry-run", false, "log and report allowed mutations as would-do without touching the cluster")
		printEffective  = fs.Bool("print-effective-policy", false, "load and validate the policy, print the effective (normalized) rules, and exit")
		showVersion     = fs.Bool("version", false, "print version information and exit")
		logLevel        = fs.String("log-level", "info", "log level: debug, info, warn, or error")
		logsDefaultTail = fs.Int64("logs-default-tail-lines", 100, "k8s_logs: lines returned when the caller omits tailLines")
		logsMaxTail     = fs.Int64("logs-max-tail-lines", 2000, "k8s_logs: upper bound on a caller's tailLines")
		logsMaxBytes    = fs.Int64("logs-max-bytes", 256*1024, "k8s_logs: hard byte cap on output")
		execTimeout     = fs.Duration("exec-timeout", 30*time.Second, "k8s_exec: max wall-clock per command")
		execMaxBytes    = fs.Int64("exec-max-bytes", 256*1024, "k8s_exec: hard byte cap on each of stdout/stderr")
	)

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}

	if *showVersion {
		if _, err := fmt.Fprintf(stdout, "kubeleash %s (commit %s, built %s)\n", version, commit, date); err != nil {
			return fmt.Errorf("write version: %w", err)
		}

		return nil
	}

	level, err := parseLevel(*logLevel)
	if err != nil {
		return err
	}

	if *logsDefaultTail < 1 || *logsMaxTail < 1 || *logsMaxBytes < 1 {
		return fmt.Errorf("log limits must be >= 1 (got default=%d max=%d bytes=%d)", *logsDefaultTail, *logsMaxTail, *logsMaxBytes)
	}
	if *logsDefaultTail > *logsMaxTail {
		return fmt.Errorf("--logs-default-tail-lines (%d) cannot exceed --logs-max-tail-lines (%d)", *logsDefaultTail, *logsMaxTail)
	}

	if *execTimeout <= 0 {
		return fmt.Errorf("--exec-timeout must be positive (got %s)", *execTimeout)
	}
	if *execMaxBytes < 1 {
		return fmt.Errorf("--exec-max-bytes must be >= 1 (got %d)", *execMaxBytes)
	}

	resolvedPolicy, err := resolvePolicyPath(*policyPath, os.Getenv(policyEnvVar))
	if err != nil {
		return err
	}

	engine, err := policy.LoadFile(resolvedPolicy)
	if err != nil {
		return fmt.Errorf("load policy: %w", err)
	}

	if *printEffective {
		return printEffectivePolicy(stdout, engine)
	}

	kubeconfigPath, err := expandPath(*kubeconfig)
	if err != nil {
		return fmt.Errorf("expand kubeconfig path: %w", err)
	}

	factory, err := kube.NewFactory(kube.Options{KubeconfigPath: kubeconfigPath})
	if err != nil {
		return fmt.Errorf("build kube factory: %w", err)
	}

	logger := audit.New(stderr, level)

	srv := intmcp.New(
		engine, factory,
		intmcp.WithAudit(logger),
		intmcp.WithDryRun(*dryRun),
		intmcp.WithVersion(version),
		intmcp.WithLogLimits(intmcp.LogLimits{
			DefaultTailLines: *logsDefaultTail,
			MaxTailLines:     *logsMaxTail,
			MaxBytes:         *logsMaxBytes,
		}),
		intmcp.WithExecLimits(intmcp.ExecLimits{
			Timeout:  *execTimeout,
			MaxBytes: *execMaxBytes,
		}),
	)

	return serve(ctx, srv, stderr)
}

// serve runs the MCP server over stdio until the client disconnects or a
// SIGINT/SIGTERM cancels the derived context. A clean shutdown (context
// cancellation) is not reported as an error.
func serve(ctx context.Context, srv *intmcp.Server, stderr io.Writer) error {
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Diagnostic banner to stderr; a failed write here is non-actionable.
	_, _ = fmt.Fprintln(stderr, "kubeleash: serving MCP over stdio (stdout is the transport)")

	if err := srv.MCP().Run(ctx, &mcp.StdioTransport{}); err != nil {
		if errors.Is(err, context.Canceled) {
			return nil
		}

		return fmt.Errorf("serve mcp: %w", err)
	}

	return nil
}

// printEffectivePolicy writes the engine's normalized rules to w as YAML. These
// are the rules exactly as the engine evaluates them (e.g. an omitted resources
// list rendered as ["*"]), not a verbatim echo of the source file.
func printEffectivePolicy(w io.Writer, engine *policy.Engine) error {
	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)

	if err := enc.Encode(engine.EffectivePolicy()); err != nil {
		return fmt.Errorf("encode effective policy: %w", err)
	}

	if err := enc.Close(); err != nil {
		return fmt.Errorf("encode effective policy: %w", err)
	}

	return nil
}

// resolvePolicyPath returns the policy path, preferring the flag value over the
// env fallback, with leading "~" expanded to the user's home directory.
// kubeleash is default-deny and MUST refuse to start without an explicit
// policy, so an empty result is an error (never fail-open).
func resolvePolicyPath(flagVal, envVal string) (string, error) {
	switch {
	case flagVal != "":
		return expandPath(flagVal)
	case envVal != "":
		return expandPath(envVal)
	default:
		return "", fmt.Errorf(
			"no policy specified: pass --policy <path> or set %s; kubeleash is default-deny and will not start without an explicit policy",
			policyEnvVar,
		)
	}
}

// expandPath expands a leading "~" or "~/" to the user's home directory.
// Absolute paths, relative paths, "$VAR" forms, and "~user/" forms are returned
// unchanged. An empty input returns empty, preserving client-go's default
// kubeconfig loading. kubeleash needs this because MCP servers are spawned via
// execve (no shell), so a tilde in a configured path is never expanded for us.
func expandPath(p string) (string, error) {
	if p == "" || p[0] != '~' {
		return p, nil
	}
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p, nil // ~user/... — unsupported, leave as-is
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("expand %q: %w", p, err)
	}
	if p == "~" {
		return home, nil
	}

	return filepath.Join(home, p[2:]), nil // p[2:] trims the leading "~/"
}

// parseLevel maps a level name to a slog.Level. Unknown values are an error so
// a typo fails fast rather than silently defaulting.
func parseLevel(name string) (slog.Level, error) {
	switch name {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("invalid log level %q: want one of debug, info, warn, error", name)
	}
}
