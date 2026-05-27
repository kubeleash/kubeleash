// SPDX-License-Identifier: Apache-2.0
package mcp

import (
	"testing"
	"time"
)

func TestNormalizeExecLimits(t *testing.T) {
	// zero -> code defaults
	got := normalizeExecLimits(ExecLimits{})
	if got.Timeout != 30*time.Second || got.MaxBytes != 256*1024 {
		t.Fatalf("defaults wrong: %+v", got)
	}
	// non-positive -> defaults
	got = normalizeExecLimits(ExecLimits{Timeout: -1, MaxBytes: -9})
	if got.Timeout != 30*time.Second || got.MaxBytes != 256*1024 {
		t.Fatalf("non-positive not defaulted: %+v", got)
	}
	// positive values are kept
	got = normalizeExecLimits(ExecLimits{Timeout: 5 * time.Second, MaxBytes: 1024})
	if got.Timeout != 5*time.Second || got.MaxBytes != 1024 {
		t.Fatalf("positive values not kept: %+v", got)
	}
}

func TestNormalizeLogLimits(t *testing.T) {
	// zero -> code defaults
	got := normalizeLogLimits(LogLimits{})
	if got.DefaultTailLines != 100 || got.MaxTailLines != 2000 || got.MaxBytes != 256*1024 {
		t.Fatalf("defaults wrong: %+v", got)
	}
	// negative -> defaults
	got = normalizeLogLimits(LogLimits{DefaultTailLines: -5, MaxTailLines: -1, MaxBytes: -9})
	if got.DefaultTailLines != 100 || got.MaxTailLines != 2000 || got.MaxBytes != 256*1024 {
		t.Fatalf("negative not defaulted: %+v", got)
	}
	// default > max -> default clamped down to max
	got = normalizeLogLimits(LogLimits{DefaultTailLines: 5000, MaxTailLines: 50, MaxBytes: 10})
	if got.DefaultTailLines != 50 || got.MaxTailLines != 50 || got.MaxBytes != 10 {
		t.Fatalf("default>max not clamped: %+v", got)
	}
}
