# 0001: Read dump text only

**Decision:** take a goroutine dump as text and never connect to the program.

**Why:** a dump can come from production, a ticket or a crash log without giving an agent access to the service. It also means no credentials and nothing that can affect a stuck program.

**Alternatives:** fetching `/debug/pprof` directly would be one step shorter, but needs network access to a service, and pprof endpoints should stay private.
