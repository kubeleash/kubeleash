# Contributing to kubeleash

Thanks for your interest in kubeleash — a Kubernetes MCP server that keeps AI
agents on a leash. kubeleash is **pre-release**; the design is settling and the
API may change. Issues, discussion, and PRs are all welcome.

## Ground rules

- Be excellent to each other — see [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).
- Security issues: **do not** open a public issue. See [SECURITY.md](SECURITY.md).
- Discuss non-trivial changes in an issue or Discussion before opening a large PR.

## Development setup

Requirements: **Go 1.26+**.

```bash
git clone git@github.com:kubeleash/kubeleash.git
cd kubeleash
go build ./...
go test ./...
```

## Quality gates (run before pushing)

```bash
gofumpt -l .                                                   # formatting
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest run ./...   # lint
go test -race ./...                                            # tests
go run golang.org/x/vuln/cmd/govulncheck@latest ./...          # vulnerabilities
```

CI runs the same checks; PRs must be green.

## Commits & PRs

- We use [Conventional Commits](https://www.conventionalcommits.org/)
  (`feat:`, `fix:`, `docs:`, `chore:`, `ci:`, `refactor:`, `test:`). The PR
  **title** is linted against this and is used to generate the changelog.
- Keep PRs focused. Add tests for behaviour changes.
- `main` is protected: PR + passing CI + at least one review.

## Project layout

See [docs/design.md](docs/design.md) for the architecture and package layout.
