# stuckgo

MCP server and CLI that finds what a stuck or leaking Go program is waiting on, from a goroutine dump.

## Architecture
`internal/dump/parse.go` reads every dump format into one list. `review.go` groups stacks and applies the rules. `compare.go` compares two dumps. `internal/server` exposes the MCP tools, and `main.go` the CLI. See `docs/architecture.md`.

## Commands
- `make test`: gofmt, vet and tests
- `make lint`: build, manifest validation and MCP Inspector checks
- `make demo`: review a real example

## Conventions
- Never connect to a program or use the network.
- Each rule has a stable `SGnnn` ID. Don't reuse IDs.
- Every rule has a real dump in `examples/dumps/`, from a program in `examples/programs/`. Say in the test if one isn't real.
- A stack marked as normally idle must really be idle in a healthy program, and needs a test.

## Do and don't
- Do call findings leads, and holders "possible". A dump doesn't say who holds a lock.
- Don't say a goroutine leaks from one dump unless Go's leak profile says so.

## Definition of done
`make test` and `make lint` pass, and `CHANGELOG.md`, `ROADMAP.md` and docs are updated when relevant.
