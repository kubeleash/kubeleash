// SPDX-License-Identifier: Apache-2.0
package mcp

import "testing"

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
