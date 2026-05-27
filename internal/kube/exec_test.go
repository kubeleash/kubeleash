// SPDX-License-Identifier: Apache-2.0
package kube

import (
	"strings"
	"testing"
)

func TestPodExecOptionsMapping(t *testing.T) {
	got := podExecOptions(ExecOptions{Container: "app", Command: []string{"sh", "-c", "echo hi"}})
	if got.Container != "app" {
		t.Errorf("container = %q, want app", got.Container)
	}
	if len(got.Command) != 3 || got.Command[0] != "sh" {
		t.Errorf("command = %v, want [sh -c echo hi]", got.Command)
	}
	if !got.Stdout || !got.Stderr {
		t.Errorf("Stdout/Stderr must be true: %+v", got)
	}
	if got.Stdin || got.TTY {
		t.Errorf("Stdin/TTY must be false (one-shot, non-interactive): %+v", got)
	}
}

func TestLimitedWriterCaps(t *testing.T) {
	w := &limitedWriter{max: 5}
	if _, err := w.Write([]byte("abc")); err != nil {
		t.Fatalf("write abc: %v", err)
	}
	if w.truncated {
		t.Errorf("should not be truncated yet")
	}
	if _, err := w.Write([]byte("defgh")); err != nil {
		t.Fatalf("write defgh: %v", err)
	}
	if w.buf.String() != "abcde" {
		t.Errorf("buffered = %q, want abcde", w.buf.String())
	}
	if !w.truncated {
		t.Errorf("should be truncated after exceeding max")
	}
	n, err := w.Write([]byte("xyz"))
	if err != nil || n != 3 {
		t.Errorf("discard write n=%d err=%v, want n=3 err=nil", n, err)
	}
	if !strings.HasPrefix(w.buf.String(), "abcde") || w.buf.Len() != 5 {
		t.Errorf("buffer grew past cap: %q", w.buf.String())
	}
}
