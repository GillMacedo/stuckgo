package dump

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Severities of a finding.
const (
	High   = "high"
	Medium = "medium"
	Low    = "low"
)

// Unknown is the state of a debug=1 goroutine whose stack doesn't show what it waits on.
const Unknown = "unknown"

const groupLimit = 15

// Finding is one lead from a dump.
type Finding struct {
	ID       string `json:"id"`
	Severity string `json:"severity"`
	Where    string `json:"where"`
	Count    int    `json:"count"`
	Message  string `json:"message"`
	Try      string `json:"try,omitempty"`
}

// Group is a set of goroutines with the same state and stack.
type Group struct {
	Count          int      `json:"count"`
	State          string   `json:"state"`
	MaxWaitMinutes int      `json:"max_wait_minutes,omitempty"`
	MinWaitMinutes int      `json:"min_wait_minutes,omitempty"`
	Where          string   `json:"where"`
	CreatedBy      string   `json:"created_by,omitempty"`
	CreatedAt      string   `json:"created_at,omitempty"`
	Expected       bool     `json:"expected,omitempty"`
	IDs            []int    `json:"ids,omitempty"`
	Stack          []string `json:"stack"`

	key    string
	frames []Frame
	leaked bool
}

// StateCount is how many goroutines are in a state.
type StateCount struct {
	State string `json:"state"`
	Count int    `json:"count"`
}

// Review is what a dump shows.
type Review struct {
	Format     string       `json:"format"`
	Total      int          `json:"total"`
	Expected   int          `json:"expected_idle"`
	Fatal      string       `json:"fatal,omitempty"`
	Signal     string       `json:"signal,omitempty"`
	States     []StateCount `json:"states"`
	Findings   []Finding    `json:"findings"`
	Groups     []Group      `json:"groups"`
	GroupCount int          `json:"group_count"`
	Notes      []string     `json:"notes,omitempty"`
}

// stdlibRoots are the top-level names of the Go standard library, so a module
// with a dotless name such as "example/app" still counts as the user's code.
var stdlibRoots = map[string]bool{}

func init() {
	for _, name := range strings.Fields(`archive bufio builtin bytes cmp compress container context crypto database debug embed encoding errors expvar flag fmt go hash html image index internal io iter log maps math mime net os path plugin reflect regexp runtime slices sort strconv strings structs sync syscall testing text time unicode unique unsafe vendor weak`) {
		stdlibRoots[name] = true
	}
}

// packageOf returns a function's import path: "net/http" for "net/http.(*Client).do".
func packageOf(function string) string {
	slash := strings.LastIndex(function, "/")
	dot := strings.Index(function[slash+1:], ".")
	if dot < 0 {
		return function
	}
	return function[:slash+1+dot]
}

func isStdlib(function string) bool {
	root, _, _ := strings.Cut(packageOf(function), "/")
	return stdlibRoots[root]
}

func userFrame(frames []Frame) (Frame, bool) {
	for _, frame := range frames {
		// errgroup and friends are plumbing, so the caller is the one to read.
		if !isStdlib(frame.Func) && !strings.HasPrefix(frame.Func, "golang.org/x/sync/") {
			return frame, true
		}
	}
	return Frame{}, false
}

func shortPath(file string) string {
	parts := strings.Split(file, "/")
	if len(parts) > 2 {
		parts = parts[len(parts)-2:]
	}
	return strings.Join(parts, "/")
}

func describe(frame Frame) string {
	if frame.File == "" {
		return frame.Func
	}
	return fmt.Sprintf("%s (%s:%d)", frame.Func, shortPath(frame.File), frame.Line)
}

// where names the frame a person would look at first.
func where(frames []Frame) string {
	if frame, ok := userFrame(frames); ok {
		return describe(frame)
	}
	for _, frame := range frames {
		name := packageOf(frame.Func)
		if name != "runtime" && !strings.HasPrefix(name, "internal/") {
			return describe(frame)
		}
	}
	if len(frames) > 0 {
		return describe(frames[0])
	}
	return "stack unavailable"
}

// inferState works out a debug=1 goroutine's state from its stack, which is all that format has.
func inferState(frames []Frame) string {
	for _, frame := range frames {
		switch frame.Func {
		case "runtime.chansend", "runtime.chansend1":
			return "chan send"
		case "runtime.chanrecv", "runtime.chanrecv1", "runtime.chanrecv2":
			return "chan receive"
		case "runtime.selectgo":
			return "select"
		case "runtime.block":
			return "select (no cases)"
		case "time.Sleep":
			return "sleep"
		case "internal/poll.runtime_pollWait":
			return "IO wait"
		case "sync.runtime_notifyListWait":
			return "sync.Cond.Wait"
		case "sync.runtime_SemacquireRWMutexR":
			return "sync.RWMutex.RLock"
		case "sync.runtime_SemacquireRWMutex":
			return "sync.RWMutex.Lock"
		case "sync.runtime_SemacquireWaitGroup":
			return "sync.WaitGroup.Wait"
		case "sync.runtime_SemacquireMutex", "internal/sync.runtime_SemacquireMutex":
			return "sync.Mutex.Lock"
		case "sync.runtime_Semacquire":
			return "semacquire"
		case "runtime.gcBgMarkWorker":
			return "GC worker (idle)"
		case "runtime.goexit", "runtime.main":
			return Unknown
		case "runtime/pprof.writeRuntimeProfile", "runtime.Stack", "runtime/pprof.runtime_goroutineProfileWithLabels":
			return "running"
		case "syscall.Syscall", "syscall.Syscall6", "syscall.RawSyscall", "syscall.syscall", "syscall.syscall6", "internal/runtime/syscall.Syscall6":
			return "syscall"
		}
	}
	return Unknown
}

var idleStates = regexp.MustCompile(`^(GC |force gc|finalizer wait|cleanup wait|GOMAXPROCS updater|wait for GC|trace |dumping heap|garbage collection|stopping the world|flushing proc|page trace|preempted)`)

// Stacks that are normally parked in a healthy program. A person can still see them in the groups.
var expectedAnywhere = []string{
	"net.(*TCPListener).Accept", "net.(*UnixListener).Accept", "net.(*netFD).accept",
	"os/signal.signal_recv", "os/signal.loop", "os/signal.NotifyContext",
	"net/http.(*persistConn).readLoop", "net/http.(*persistConn).writeLoop",
	"net/http.(*connReader).backgroundRead",
	// Go 1.27 moved HTTP/2 to net/http/internal/http2. Before that it was bundled into net/http.
	"net/http.(*http2serverConn).serve", "net/http.(*http2serverConn).readFrames", "net/http.(*http2ClientConn).readLoop", "net/http.(*http2clientConnReadLoop).run",
	"net/http/internal/http2.(*serverConn).serve", "net/http/internal/http2.(*serverConn).readFrames", "net/http/internal/http2.(*ClientConn).readLoop", "net/http/internal/http2.(*clientConnReadLoop).run",
	"golang.org/x/net/http2.(*serverConn).serve", "golang.org/x/net/http2.(*serverConn).readFrames", "golang.org/x/net/http2.(*ClientConn).readLoop", "golang.org/x/net/http2.(*clientConnReadLoop).run",
	"database/sql.(*DB).connectionOpener", "database/sql.(*DB).connectionCleaner", "database/sql.(*DB).connectionResetter",
	"runtime/pprof.writeGoroutineStacks", "runtime/pprof.writeRuntimeProfile", "runtime.ensureSigM", "runtime/trace.Start", "runtime.bgsweep", "runtime.bgscavenge", "runtime.forcegchelper", "runtime.runfinq", "runtime.runCleanups", "runtime.gcBgMarkWorker", "runtime.updateMaxProcsGoroutine",
	"google.golang.org/grpc/internal/transport.(*loopyWriter).run", "google.golang.org/grpc/internal/transport.(*http2Client).reader", "google.golang.org/grpc/internal/transport.(*http2Client).keepalive",
	"google.golang.org/grpc/internal/transport.(*http2Server).keepalive", "google.golang.org/grpc/internal/transport.(*http2Server).HandleStreams", "google.golang.org/grpc/internal/grpcsync.(*CallbackSerializer).run",
}

func isExpected(g Goroutine, state string) bool {
	if idleStates.MatchString(state) {
		return true
	}
	if g.Leaked {
		return false
	}
	for _, frame := range g.Frames {
		for _, known := range expectedAnywhere {
			if frame.Func == known {
				return true
			}
		}
	}
	// A server connection waiting for its next request, with no handler on the stack.
	if _, user := userFrame(g.Frames); !user && state == "IO wait" {
		for _, frame := range g.Frames {
			if frame.Func == "net/http.(*conn).serve" {
				return true
			}
		}
	}
	return false
}

func stackKey(frames []Frame) string {
	var key strings.Builder
	started := false
	for _, frame := range frames {
		if !started && packageOf(frame.Func) == "runtime" {
			continue
		}
		if frame.Func == "runtime.goexit" {
			continue
		}
		started = true
		fmt.Fprintf(&key, "%s@%s:%d\n", frame.Func, frame.File, frame.Line)
	}
	return key.String()
}

func buildGroups(parsed *Dump) []Group {
	groups := map[string]*Group{}
	var order []string
	for _, g := range parsed.Goroutines {
		state := g.State
		if parsed.Format == FormatProfile {
			state = inferState(g.Frames)
		}
		key := state + "\n" + stackKey(g.Frames)
		group, ok := groups[key]
		if !ok {
			group = &Group{State: state, Where: where(g.Frames), CreatedBy: g.CreatedBy, CreatedAt: g.CreatedAt, Expected: isExpected(g, state), key: stackKey(g.Frames), frames: g.Frames, MinWaitMinutes: g.WaitMinutes}
			for _, frame := range g.Frames {
				if len(group.Stack) == 8 {
					break
				}
				group.Stack = append(group.Stack, describe(frame))
			}
			groups[key] = group
			order = append(order, key)
		}
		group.Count += g.Count
		group.leaked = group.leaked || g.Leaked
		if g.WaitMinutes > group.MaxWaitMinutes {
			group.MaxWaitMinutes = g.WaitMinutes
		}
		if g.WaitMinutes < group.MinWaitMinutes {
			group.MinWaitMinutes = g.WaitMinutes
		}
		if g.ID != 0 && len(group.IDs) < 5 {
			group.IDs = append(group.IDs, g.ID)
		}
	}
	result := make([]Group, 0, len(order))
	for _, key := range order {
		result = append(result, *groups[key])
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Expected != result[j].Expected {
			return !result[i].Expected
		}
		return result[i].Count > result[j].Count
	})
	return result
}

// Analyze reviews a parsed dump.
func Analyze(parsed *Dump) Review {
	groups := buildGroups(parsed)
	review := Review{Format: parsed.Format, Fatal: parsed.Fatal, Signal: parsed.Signal, GroupCount: len(groups), Findings: []Finding{}, Groups: []Group{}}
	states := map[string]int{}
	for _, group := range groups {
		review.Total += group.Count
		states[group.State] += group.Count
		if group.Expected {
			review.Expected += group.Count
		}
	}
	for state, count := range states {
		review.States = append(review.States, StateCount{state, count})
	}
	sort.Slice(review.States, func(i, j int) bool {
		if review.States[i].Count != review.States[j].Count {
			return review.States[i].Count > review.States[j].Count
		}
		return review.States[i].State < review.States[j].State
	})
	review.Findings = findings(parsed, groups, review.Total)
	review.Groups = groups
	if len(groups) > groupLimit {
		review.Groups = groups[:groupLimit]
	}
	review.Notes = notes(parsed, review)
	return review
}

func notes(parsed *Dump, review Review) []string {
	var result []string
	if parsed.Format == FormatProfile {
		result = append(result, "This debug=1 dump has no wait times or goroutine IDs, and hides most of what a goroutine waits on. Use /debug/pprof/goroutine?debug=2 for all three.")
	} else if review.Total == 1 {
		result = append(result, "This dump shows one goroutine. A crash only prints the failing one by default. Set GOTRACEBACK=all, or use /debug/pprof/goroutine?debug=2, to see the rest.")
	}
	if parsed.Format == FormatTraceback {
		result = append(result, "Go only prints a wait of a minute or more, and starts the clock at a garbage collection, so a missing wait time doesn't mean a short wait.")
	}
	if parsed.CutOff {
		result = append(result, "The dump looks cut off, so some goroutines may be missing.")
	}
	for _, g := range parsed.Goroutines {
		if g.Elided {
			result = append(result, "Some stacks have frames left out by Go, so the code that started them may be hidden.")
			break
		}
	}
	return result
}

func contains(frames []Frame, names ...string) bool {
	for _, frame := range frames {
		for _, name := range names {
			if frame.Func == name {
				return true
			}
		}
	}
	return false
}

func waited(group Group) string {
	if group.MaxWaitMinutes == 0 {
		return ""
	}
	return fmt.Sprintf(", waiting up to %d minutes", group.MaxWaitMinutes)
}

func plural(count int, singular string) string {
	if count == 1 {
		return "1 " + singular
	}
	return fmt.Sprintf("%d %ss", count, singular)
}

// findings applies the rules. A debug=1 dump has no wait times, so counts carry the rules alone.
func findings(parsed *Dump, groups []Group, total int) []Finding {
	result := []Finding{}
	add := func(id, severity, place string, count int, message, try string) {
		result = append(result, Finding{ID: id, Severity: severity, Where: place, Count: count, Message: message, Try: try})
	}
	if strings.Contains(parsed.Fatal, "all goroutines are asleep") {
		add("SG101", High, firstPlace(parsed), total, "Go stopped the program because every goroutine was blocked, so nothing could ever wake them.", "Read the groups below for the two sides that wait on each other. Check for a send with no receiver, or a channel that's never closed.")
	} else if parsed.Fatal != "" {
		add("SG102", High, firstPlace(parsed), 1, "The program crashed: "+parsed.Fatal, "Read the first goroutine's stack, which is usually the one that failed.")
	}
	for _, group := range groups {
		if group.leaked {
			add("SG103", High, group.Where, group.Count, fmt.Sprintf("Go's leak profile reports %s as leaked, blocked on something no running goroutine can reach.", plural(group.Count, "goroutine")), "Read this stack and find who should have unblocked it.")
			continue
		}
		if group.Expected {
			continue
		}
		wait, count := group.MaxWaitMinutes, group.Count
		switch {
		case strings.HasSuffix(group.State, "(nil chan)"):
			add("SG104", High, group.Where, count, fmt.Sprintf("%s blocked forever on a nil channel%s. A send or receive on nil never returns.", plural(count, "goroutine"), waited(group)), "Find where this channel should be made with make(chan ...), such as a struct field that's never set.")
		case contains(group.frames, "database/sql.(*DB).conn"):
			severity := Medium
			if count >= 2 {
				severity = High
			}
			add("SG105", severity, group.Where, count, fmt.Sprintf("%s waiting for a free database connection%s. The pool is full.", plural(count, "goroutine"), waited(group)), "Look for queries that hold connections: slow queries, Rows not closed, or a transaction left open. Compare with SetMaxOpenConns, and pass a context with a deadline.")
		case contains(group.frames, "net/http.(*Client).do", "net/http.(*Transport).roundTrip", "net/http.(*persistConn).roundTrip"):
			severity := ""
			switch {
			case wait >= 1 && count >= 10, count >= 100:
				severity = High
			case wait >= 1, count >= 10:
				severity = Medium
			}
			if severity != "" {
				add("SG106", severity, group.Where, count, fmt.Sprintf("%s waiting on an outgoing HTTP call%s.", plural(count, "goroutine"), waited(group)), "Check for a request with no deadline. http.DefaultClient has no timeout, so set Client.Timeout or use a context with a deadline.")
			}
		case strings.HasPrefix(group.State, "sync.Mutex.Lock"), strings.HasPrefix(group.State, "sync.RWMutex."):
			if count >= 2 || wait >= 1 {
				severity := Medium
				if count >= 10 || wait >= 1 {
					severity = High
				}
				add("SG107", severity, group.Where, count, lockMessage(group, groups), "Look at what the holder does while it holds the lock. Don't hold a lock across network calls, channel operations or slow work.")
			}
		case strings.HasPrefix(group.State, "sync.WaitGroup.Wait"), group.State == "semacquire" && contains(group.frames, "sync.(*WaitGroup).Wait"):
			// Long-running services park here for their whole life, so a single waiter proves little.
			if wait >= 1 || count >= 10 {
				severity := Low
				if count >= 10 && wait >= 1 {
					severity = Medium
				}
				add("SG108", severity, group.Where, count, waitGroupMessage(group, groups), "If this work should have finished, find which goroutine hasn't called Done, and check for a return path that skips it. This is normal if what it started runs for the life of the program.")
			}
		case strings.HasPrefix(group.State, "chan send"):
			severity := ""
			switch {
			case count >= 100, count >= 10 && wait >= 1:
				severity = High
			case count >= 10, wait >= 5:
				severity = Medium
			case wait >= 1:
				severity = Low
			}
			if severity != "" {
				add("SG109", severity, group.Where, count, fmt.Sprintf("%s stuck sending on a channel%s. Nothing is receiving. This is the usual goroutine leak: the caller gave up, and the sender can't finish.", plural(count, "goroutine"), waited(group)), "If the reader can leave early, make the channel buffered with room for every sender, or use select with ctx.Done(). Take a second dump to see if this count grows.")
			}
		case strings.HasPrefix(group.State, "chan receive"), group.State == "select":
			severity := ""
			switch {
			case count >= 1000:
				severity = High
			case count >= 100:
				severity = Medium
			case count >= 10 && wait >= 1:
				severity = Low
			}
			if severity != "" {
				add("SG110", severity, group.Where, count, fmt.Sprintf("%s waiting in %s%s. This may be a normal idle pool, or a leak if the count keeps growing.", plural(count, "goroutine"), group.State, waited(group)), "Take a second dump and compare. Check that something closes this channel or cancels this context on shutdown.")
			}
		case group.State == "IO wait":
			severity := ""
			switch {
			case wait >= 1 && count >= 10:
				severity = High
			case wait >= 1, count >= 100:
				severity = Medium
			}
			if severity != "" {
				add("SG111", severity, group.Where, count, fmt.Sprintf("%s waiting on network I/O%s.", plural(count, "goroutine"), waited(group)), "Check for a missing read or write deadline. This can be normal for long-lived connections, such as a subscription.")
			}
		case group.State == "syscall":
			if wait >= 1 {
				severity := Low
				if count >= 10 {
					severity = Medium
				}
				add("SG112", severity, group.Where, count, fmt.Sprintf("%s in a system call%s.", plural(count, "goroutine"), waited(group)), "Check for blocking file, device or cgo calls. Each one holds an OS thread.")
			}
		case group.State == "sync.Cond.Wait":
			if count >= 10 && wait >= 1 || wait >= 5 {
				add("SG113", Low, group.Where, count, fmt.Sprintf("%s in sync.Cond.Wait%s.", plural(count, "goroutine"), waited(group)), "Check that something calls Signal or Broadcast after every change. This may be a normal idle queue.")
			}
		case group.State == Unknown:
			severity := ""
			switch {
			case count >= 1000:
				severity = High
			case count >= 100:
				severity = Medium
			case count >= 10:
				severity = Low
			}
			if severity != "" {
				add("SG117", severity, group.Where, count, fmt.Sprintf("%s parked at this line. A debug=1 dump doesn't show what they wait on, so this could be a leak or a normal idle pool.", plural(count, "goroutine")), "Read this line of the code. Take a debug=2 dump for the state, wait times and goroutine IDs, and a second dump to see if this count grows.")
			}
		case group.State == "sleep":
			if count >= 100 {
				add("SG114", Low, group.Where, count, fmt.Sprintf("%s sleeping at one place%s, so this may be one poller per item.", plural(count, "goroutine"), waited(group)), "Check whether one shared ticker or a worker pool would do, and that these loops can stop.")
			}
		}
	}
	result = append(result, crowdFindings(groups, total)...)
	rank := map[string]int{High: 0, Medium: 1, Low: 2}
	sort.SliceStable(result, func(i, j int) bool {
		if rank[result[i].Severity] != rank[result[j].Severity] {
			return rank[result[i].Severity] < rank[result[j].Severity]
		}
		return result[i].Count > result[j].Count
	})
	return result
}

func crowdFindings(groups []Group, total int) []Finding {
	var result []Finding
	byCreator := map[string]int{}
	stacks := map[string]int{}
	running := 0
	for _, group := range groups {
		if group.State == "runnable" || group.State == "running" {
			running += group.Count
		}
		if group.Expected || group.CreatedBy == "" {
			continue
		}
		byCreator[group.CreatedBy] += group.Count
		stacks[group.CreatedBy]++
	}
	creators := make([]string, 0, len(byCreator))
	for creator := range byCreator {
		creators = append(creators, creator)
	}
	sort.Strings(creators)
	for _, creator := range creators {
		count := byCreator[creator]
		if count >= 100 && count*2 >= total && stacks[creator] >= 2 {
			result = append(result, Finding{ID: "SG115", Severity: Medium, Where: creator, Count: count, Message: fmt.Sprintf("%s of %d, across %d different stacks, were started by this one function.", plural(count, "goroutine"), total, stacks[creator]), Try: "Check for a limit on how many it starts, such as a worker pool or semaphore."})
		}
	}
	if running >= 100 {
		result = append(result, Finding{ID: "SG116", Severity: Low, Where: "whole program", Count: running, Message: fmt.Sprintf("%s running or waiting for a CPU, so the program may be short of CPU.", plural(running, "goroutine")), Try: "Check CPU limits and GOMAXPROCS, and take a CPU profile, because a dump can't show where time goes."})
	}
	return result
}

// firstPlace is where the first goroutine is. Go prints the one that failed first.
func firstPlace(parsed *Dump) string {
	return where(parsed.Goroutines[0].Frames)
}

// receiverOf returns "main.(*store)" for "main.(*store).Get", or "" for a plain function.
func receiverOf(function string) string {
	start := strings.Index(function, ".(*")
	if start < 0 {
		return ""
	}
	end := strings.Index(function[start:], ")")
	if end < 0 {
		return ""
	}
	return function[:start+end+1]
}

func lockMessage(group Group, groups []Group) string {
	kind := "a mutex"
	if strings.Contains(group.State, "RWMutex") {
		kind = "an RWMutex"
	}
	message := fmt.Sprintf("%s waiting for %s%s.", plural(group.Count, "goroutine"), kind, waited(group))
	frame, ok := userFrame(group.frames)
	if !ok {
		return message
	}
	receiver := receiverOf(frame.Func)
	if receiver == "" {
		return message
	}
	// A dump doesn't say who holds a lock, so this can only offer candidates.
	seen := 0
	for _, candidate := range group.frames {
		if receiverOf(candidate.Func) == receiver {
			seen++
		}
	}
	if seen >= 2 {
		message += fmt.Sprintf(" This stack enters %s more than once, so it may already hold this lock. Go's mutexes aren't reentrant.", receiver)
	}
	var holders []string
	for _, other := range groups {
		if strings.HasPrefix(other.State, "sync.Mutex.Lock") || strings.HasPrefix(other.State, "sync.RWMutex.") || other.Expected {
			continue
		}
		for _, candidate := range other.frames {
			if receiverOf(candidate.Func) == receiver {
				holder := describe(candidate)
				if other.State != Unknown {
					holder += ", in " + other.State
				}
				holders = append(holders, holder)
				break
			}
		}
		if len(holders) == 3 {
			break
		}
	}
	if len(holders) > 0 {
		message += " Possible holder, not proven: " + strings.Join(holders, "; ") + "."
	}
	return message
}

func waitGroupMessage(group Group, groups []Group) string {
	message := fmt.Sprintf("%s waiting on a WaitGroup%s.", plural(group.Count, "goroutine"), waited(group))
	frame, ok := userFrame(group.frames)
	if !ok {
		return message
	}
	var started []string
	for _, other := range groups {
		if other.CreatedBy == frame.Func || strings.HasPrefix(other.CreatedBy, frame.Func+".") {
			started = append(started, fmt.Sprintf("%d at %s, in %s", other.Count, other.Where, other.State))
		}
		if len(started) == 3 {
			break
		}
	}
	if len(started) > 0 {
		message += " It started: " + strings.Join(started, "; ") + "."
	}
	return message
}

// HasFindingAt reports whether a review has a finding at any of these severities.
func (r Review) HasFindingAt(levels ...string) bool {
	for _, finding := range r.Findings {
		for _, level := range levels {
			if finding.Severity == level {
				return true
			}
		}
	}
	return false
}
