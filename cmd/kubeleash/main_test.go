// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validPolicy = `
policies:
  - contexts: "dev"
    allow:
      verbs: [get, list]
`

// writeTempPolicy writes a minimal valid policy file and returns its path.
func writeTempPolicy(t *testing.T, body string) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "policy.yaml")

	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write temp policy: %v", err)
	}

	return path
}

func TestResolvePolicyPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		flagVal string
		envVal  string
		want    string
		wantErr bool
	}{
		{name: "flag beats env", flagVal: "/from/flag", envVal: "/from/env", want: "/from/flag"},
		{name: "env fallback when no flag", flagVal: "", envVal: "/from/env", want: "/from/env"},
		{name: "flag only", flagVal: "/from/flag", envVal: "", want: "/from/flag"},
		{name: "neither set is error", flagVal: "", envVal: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := resolvePolicyPath(tt.flagVal, tt.envVal)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseLevel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in      string
		want    slog.Level
		wantErr bool
	}{
		{in: "debug", want: slog.LevelDebug},
		{in: "info", want: slog.LevelInfo},
		{in: "warn", want: slog.LevelWarn},
		{in: "error", want: slog.LevelError},
		{in: "bogus", wantErr: true},
		{in: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()

			got, err := parseLevel(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got nil", tt.in)
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if got != tt.want {
				t.Errorf("parseLevel(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestRunVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer

	err := run(context.Background(), []string{"--version"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run(--version) returned error: %v", err)
	}

	out := stdout.String()
	if !strings.HasPrefix(out, "kubeleash ") {
		t.Errorf("version output = %q, want prefix %q", out, "kubeleash ")
	}

	if !strings.Contains(out, version) || !strings.Contains(out, commit) {
		t.Errorf("version output missing build vars: %q", out)
	}

	if stderr.Len() != 0 {
		t.Errorf("expected no stderr on --version, got %q", stderr.String())
	}
}

func TestRunPrintEffectivePolicy(t *testing.T) {
	path := writeTempPolicy(t, validPolicy)

	var stdout, stderr bytes.Buffer

	err := run(context.Background(), []string{"--policy", path, "--print-effective-policy"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run(--print-effective-policy) returned error: %v", err)
	}

	out := stdout.String()
	// Normalized output should default the omitted resources to "*" and list
	// the sorted verbs.
	for _, want := range []string{"contexts: dev", "resources:", "'*'", "get", "list"} {
		if !strings.Contains(out, want) {
			t.Errorf("effective policy output missing %q\ngot:\n%s", want, out)
		}
	}
}

func TestRunMissingPolicyIsError(t *testing.T) {
	t.Setenv(policyEnvVar, "")

	var stdout, stderr bytes.Buffer

	err := run(context.Background(), []string{}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error when no policy is specified, got nil")
	}

	if !strings.Contains(err.Error(), "no policy specified") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRunInvalidPolicyFileIsError(t *testing.T) {
	t.Setenv(policyEnvVar, "")

	var stdout, stderr bytes.Buffer

	err := run(context.Background(), []string{"--policy", "/nonexistent/policy.yaml"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error loading nonexistent policy, got nil")
	}

	if !strings.Contains(err.Error(), "load policy") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRunInvalidLogLevelIsError(t *testing.T) {
	path := writeTempPolicy(t, validPolicy)

	var stdout, stderr bytes.Buffer

	err := run(context.Background(), []string{"--policy", path, "--log-level", "bogus"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for invalid log level, got nil")
	}

	if !strings.Contains(err.Error(), "invalid log level") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestExpandPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cases := []struct {
		name, in, want string
	}{
		{"empty", "", ""},
		{"tilde slash", "~/.kube/config", filepath.Join(home, ".kube/config")},
		{"tilde slash empty", "~/", home},
		{"bare tilde", "~", home},
		{"absolute", "/abs/path", "/abs/path"},
		{"relative", "rel/x", "rel/x"},
		{"dollar home literal", "$HOME/.kube/config", "$HOME/.kube/config"},
		{"tilde user unsupported", "~bob/x", "~bob/x"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := expandPath(c.in)
			if err != nil {
				t.Fatalf("expandPath(%q): %v", c.in, err)
			}
			if got != c.want {
				t.Errorf("expandPath(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestResolvePolicyPathExpandsTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	want := filepath.Join(home, "p.yaml")

	t.Run("from flag", func(t *testing.T) {
		got, err := resolvePolicyPath("~/p.yaml", "")
		if err != nil {
			t.Fatalf("resolvePolicyPath: %v", err)
		}
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("from env", func(t *testing.T) {
		got, err := resolvePolicyPath("", "~/p.yaml")
		if err != nil {
			t.Fatalf("resolvePolicyPath: %v", err)
		}
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}

func TestRunInvalidLogLimits(t *testing.T) {
	t.Setenv(policyEnvVar, "")
	dir := t.TempDir()
	pol := filepath.Join(dir, "p.yaml")
	if err := os.WriteFile(pol, []byte("policies: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer

	// default tail above max -> error
	err := run(context.Background(),
		[]string{"--policy", pol, "--logs-default-tail-lines", "10", "--logs-max-tail-lines", "5"},
		&out, &errOut)
	if err == nil {
		t.Fatal("want error when default tail > max tail")
	}

	// non-positive -> error
	err = run(context.Background(),
		[]string{"--policy", pol, "--logs-max-bytes", "0"}, &out, &errOut)
	if err == nil {
		t.Fatal("want error when a log limit < 1")
	}
}

func TestRunPolicyFromEnv(t *testing.T) {
	path := writeTempPolicy(t, validPolicy)
	t.Setenv(policyEnvVar, path)

	var stdout, stderr bytes.Buffer

	// Use --print-effective-policy so we exercise env resolution + load without
	// starting the stdio server.
	err := run(context.Background(), []string{"--print-effective-policy"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run with env policy returned error: %v", err)
	}

	if !strings.Contains(stdout.String(), "contexts: dev") {
		t.Errorf("expected effective policy from env, got: %s", stdout.String())
	}
}
