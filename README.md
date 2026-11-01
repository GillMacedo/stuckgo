# stuckgo: why is this Go program stuck?

[![npm](https://img.shields.io/npm/v/stuckgo)](https://www.npmjs.com/package/stuckgo) [![CI](https://github.com/GillMacedo/stuckgo/actions/workflows/ci.yml/badge.svg)](https://github.com/GillMacedo/stuckgo/actions/workflows/ci.yml) [![MIT license](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

**An MCP server and CLI that reads a Go goroutine dump and tells you what the program is waiting on. It finds goroutine leaks, lock pile-ups, full database pools and HTTP calls with no timeout. It works from the dump text alone, so it never connects to your service.**

A hung service can have thousands of goroutines, and most of them are normal: listeners, read loops, idle workers. `stuckgo` groups identical stacks, sets aside what's normally idle, and ranks what's left. When 40 goroutines wait on a mutex, it also names what might be holding it.

## What you can ask your agent

- "Our Go service is hanging. Here's the goroutine dump."
- "Do we have a goroutine leak? Here are two dumps ten minutes apart."
- "Why is this service out of database connections?"
- "What's holding this mutex?"

## Install

As a Claude Code plugin:

    claude plugin marketplace add GillMacedo/stuckgo
    claude plugin install stuckgo@stuckgo-marketplace

As an MCP server, for any client:

    {"mcpServers": {"stuckgo": {"command": "npx", "args": ["-y", "stuckgo@0.1.0", "serve"]}}}

As a CLI, with Node 18 or newer:

    npx stuckgo review dump.txt

Or with Go 1.27 or newer: `go install github.com/GillMacedo/stuckgo@latest`.

## Usage

Get a dump from a service that imports `net/http/pprof`. This doesn't stop the program:

    curl 'http://localhost:6060/debug/pprof/goroutine?debug=2' > dump.txt

`kill -QUIT <pid>` also prints a dump, but **it ends the process**. Crash output works too, and `GOTRACEBACK=all` shows every goroutine rather than just the failing one.

Here's a real dump from a made-up service in `examples/`, run from a clone of this repo:

    $ go run . review examples/dumps/mutex-late.txt
    43 goroutines, 1 normally idle

    Groups:
          40  sync.Mutex.Lock, up to 2 min  main.(*store).Get (app/main.go:109)
           1  chan receive, up to 2 min  main.mutex (app/main.go:130)
           1  select, up to 2 min  main.(*store).Refresh (app/main.go:118)

    HIGH   SG107 main.(*store).Get (app/main.go:109)
      40 goroutines waiting for a mutex, waiting up to 2 minutes. Possible holder, not proven: main.(*store).Refresh (app/main.go:118), in select.
      Try: Look at what the holder does while it holds the lock. Don't hold a lock across network calls, channel operations or slow work.

To check for a leak, compare two dumps taken some time apart. A stack that grows, with the same goroutine IDs staying in place, is much stronger evidence than one dump:

    $ go run . compare examples/dumps/leak-early.txt examples/dumps/leak-late.txt
    Goroutines: 344 -> 637 (+293)

    Growing:
        +293  342 -> 635  chan send  main.lookupPrice.func1 (app/main.go:79)

    Same goroutine IDs in the same place in both dumps:
         342  chan send, up to 2 min  main.lookupPrice.func1 (app/main.go:79)

With Go 1.27 or newer, Go can also check for itself: `/debug/pprof/goroutineleak?debug=2`. `stuckgo` reads that output too.

Add `--json` for JSON, or `--fail-on high` to exit 1 in CI. Use `-` to read from stdin.

## What it finds

| Rule | Finds |
|---|---|
| SG101 | Go's own "all goroutines are asleep" deadlock |
| SG102 | A panic or fatal error, and where it happened |
| SG103 | Goroutines that Go's leak profile reports as leaked |
| SG104 | A send or receive on a nil channel, which never returns |
| SG105 | Goroutines waiting for a free `database/sql` connection |
| SG106 | Outgoing HTTP calls that have been waiting a long time |
| SG107 | Goroutines piling up on a mutex, with possible holders |
| SG108 | A `WaitGroup` that hasn't finished, and what it started |
| SG109 | Goroutines stuck sending on a channel, the usual leak |
| SG110 | Many goroutines waiting on a channel receive or select |
| SG111 | Long waits on network I/O |
| SG112 | Long system calls |
| SG113 | Long waits in `sync.Cond.Wait` |
| SG114 | Many goroutines sleeping at one line |
| SG115 | One function that started most of the goroutines, across several stacks |
| SG116 | Many goroutines running or waiting for a CPU |
| SG117 | Many goroutines parked at one line in a `debug=1` dump, which doesn't say why |

Severity comes from how many goroutines are involved and how long they've waited. These thresholds are judgment calls.

## MCP tools

| Tool | Does |
|---|---|
| `go_goroutine_review` | Reviews one dump, from text or a file |
| `go_goroutine_compare` | Compares two dumps of one program |

## Limits

- **Findings are leads.** A dump is one moment. Check each finding against the code.
- **It can't prove who holds a lock.** Go's dumps don't say. A "possible holder" is another goroutine in the same type's methods.
- **One dump can't prove a leak,** unless it's Go's own leak profile. An idle worker pool looks the same as a leak, so compare two dumps.
- **`debug=1` shows less.** It has counts, with no wait times or goroutine IDs, and hides most of what a goroutine waits on. Use `debug=2` when you can.
- **Wait times are rough.** Go only prints a wait of a minute or more, and starts the clock at a garbage collection, so a missing time doesn't mean a short wait.
- **It can't see CPU use.** For a program that's slow with nothing parked, take a CPU profile.
- **The list of normally idle stacks is short:** the Go runtime, `net/http`, `database/sql`, gRPC and `golang.org/x/net/http2`. Other libraries' background loops show up as groups, but rarely as findings.
- Dumps can hold file paths and values from your program. `stuckgo` stays on your machine, but your MCP client sends tool results to its model.

## FAQ

**How do I find a goroutine leak in Go?** Take two dumps some time apart and run `stuckgo compare`. A growing stack whose goroutine IDs stay in place is the sign to look for.

**How do I get a goroutine dump from a running Go program?** Import `net/http/pprof`, then fetch `/debug/pprof/goroutine?debug=2`. Keep that endpoint private.

**What does `[chan send, 5 minutes]` mean in a goroutine dump?** That goroutine has been blocked for about 5 minutes, sending on a channel nothing is receiving from. SG109 covers it.

**Why is my Go service out of database connections?** Goroutines waiting in `database/sql.(*DB).conn` mean the pool is full. SG105 flags them. Look at what's holding the connections, such as slow queries or `Rows` that aren't closed.

**How is this different from panicparse or goroutine-inspect?** [panicparse](https://github.com/maruel/panicparse) and [goroutine-inspect](https://github.com/linuxerwang/goroutine-inspect) help a person read and explore a dump. `stuckgo` gives an agent ranked findings, can compare two dumps, and sets aside what's normally idle.

## Tested on

- **Real programs in `examples/programs/`:** every rule from SG101 to SG109, plus SG111, SG114 and SG117, fired on dumps from programs that really got stuck, in `examples/dumps/`. Most also ran on Go 1.22.
- **Real services:** idle Prometheus 3.7.3 and etcd 3.6.5, in `examples/dumps/real/`, raised nothing above low. Prometheus gets two low `WaitGroup` findings, which are normal there.
- **Written examples only:** SG110, SG112, SG113, SG115 and SG116.
- **MCP tool calls** went through the MCP Inspector.

It hasn't been used on an incident in production yet, or in a fresh Claude session.

## Contributing

Contributions are welcome, from people and from people working with AI agents. See [CONTRIBUTING.md](CONTRIBUTING.md). Good first issues are [listed here](https://github.com/GillMacedo/stuckgo/issues?q=is%3Aissue+is%3Aopen+label%3A%22good+first+issue%22).

### How pull requests work

1. **Open a PR.** Fork the repo, work on a branch, and keep it to one change. Write a title that says what changes for users, because it becomes the changelog line: `feat:` goes under Added and `fix:` under Fixed.
2. **Automatic check.** Code changes need tests, and `make test` must pass. Changes over 30 lines need a linked open issue (`Closes #<number>`), and a PR can have at most 800 changed lines. If something is missing, a comment tells you what to fix.
3. **Review.** Claude reviews each PR automatically. Code PRs also get a security review and are tested in a sandbox. The review then merges the PR or asks for changes. PRs that change CI, agent settings, dependencies or build files go to the maintainer, as does anything the security review doesn't clear.
4. **Release.** A merge that changes what users install becomes a new version within about 20 minutes, with a GitHub Release and, if the project has one, its npm package. Docs and CI changes wait for the next release.

## AI disclosure

This project was largely generated with Claude Code and reviewed by its maintainer.

## About me

I'm Giulliano Macedo, and I build developer tools at GoTech Solutions. Follow along or get in touch:

- GitHub: [GillMacedo](https://github.com/GillMacedo)
- LinkedIn: [giulliano](https://www.linkedin.com/in/giulliano/)
- X: [@giumacedo](https://x.com/giumacedo)

If `stuckgo` found what your program was stuck on, a star helps others find it.

---

Built by [Giulliano Macedo](https://github.com/GillMacedo), GoTech Solutions · [LinkedIn](https://www.linkedin.com/in/giulliano/) · [X](https://x.com/giumacedo)
