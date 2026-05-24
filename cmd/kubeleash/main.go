// SPDX-License-Identifier: Apache-2.0
//
// Command kubeleash is a Kubernetes MCP server that enforces RBAC-style,
// context-scoped access control for AI agents. See docs/ for the design.
//
// This is a skeleton entrypoint: the MCP server, policy engine, and kube
// client layers are implemented per the design spec and wired in here.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "kubeleash: not yet implemented")
	os.Exit(1)
}
