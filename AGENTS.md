# AGENTS.md

## Project Overview

A CLI tool that organizes photos and videos by capture date with content-hash naming. It shells out to exiftool for metadata extraction and uses Go for all hashing, path building, and file operations.

## How the program works and it's intent

Read the README.md for details.

## Build & Test

There is no Makefile. Just use standard go commands.

- Run: `go run .`
- Build: `go build .`
- Test: `go test -v -p 1 ./...`
- Vet: `go vet ./...`
- Format: use gofumpt.

## Code Standards

- [Google Go Style Guide](https://google.github.io/styleguide/go/) (itself a superset of Effective Go)
- General spirit of idiomatic Go: keeping it simple and concise.

## Testing Requirements

- All features should be covered by tests.

## Commit Conventions

- Use [Conventional Commits](https://www.conventionalcommits.org/) format: `type: summary`
- Types: `feat`, `fix`, `chore`, `docs`, `refactor`, `test`
- In the commit body, summarize what was changed and why
- End the commit body with the agent and model that produced it, e.g.: `Implemented by opencode (ollama-cloud/glm-5.2).`