# Architecture

    dump text --> Parse --> one list of goroutines
    buildGroups --> same state and stack --> groups, with normally idle stacks marked
    findings --> one rule per group, ranked by severity and count
    Compare --> two dumps --> stacks growing, and goroutine IDs that never moved

- **Formats:** `debug=2`, panics, fatal errors and SIGQUIT share a format. `debug=1` is different: it has counts, no IDs or wait times, and hides runtime frames, so a channel wait can't be told from a select. Those groups have the state `unknown`, and SG117 covers them.
- **Your code or Go's:** a frame counts as the user's unless its package's first path element is in the standard library's list, so a module called `example/app` still counts.
- **Normally idle:** a list of stacks, such as listeners and HTTP read loops, and a program's garbage collector workers. They still count in totals and show in JSON.
- **Severity** mixes count and wait time. See the thresholds in `review.go`. They are judgment calls.

Key choice: read dump text only, with no connection to the program. See `decisions/`.
