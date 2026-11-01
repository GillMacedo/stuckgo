package dump

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadExample(t *testing.T, name string) *Dump {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "examples", "dumps", name))
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := Parse(string(data))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return parsed
}

func find(review Review, id string) *Finding {
	for index := range review.Findings {
		if review.Findings[index].ID == id {
			return &review.Findings[index]
		}
	}
	return nil
}

// Every dump here came from a real program in examples/programs.
func TestRulesOnRealDumps(t *testing.T) {
	cases := []struct {
		file, id, severity, where string
		atLeast                   int
		message                   string
	}{
		{"deadlock.txt", "SG101", High, "main.deadlock", 2, "every goroutine was blocked"},
		{"panic-all.txt", "SG102", High, "main.crash", 1, "nil map"},
		{"map.txt", "SG102", High, "main.concurrent.func1", 1, "concurrent map writes"},
		{"leakprofile.txt", "SG103", High, "main.stuck", 5, "leak profile"},
		{"leakprofile-debug1.txt", "SG103", High, "main.stuck", 5, "leak profile"},
		{"nilchan-late.txt", "SG104", High, "main.publish", 1, "nil channel"},
		{"sqlpool-late.txt", "SG105", High, "main.loadCustomer", 28, "free database connection"},
		{"sqlpool-debug1.txt", "SG105", High, "main.loadCustomer", 28, "free database connection"},
		{"httpclient-late.txt", "SG106", High, "main.callPartner", 12, "outgoing HTTP call"},
		{"httpclient-debug1.txt", "SG106", Medium, "main.callPartner", 12, "outgoing HTTP call"},
		{"mutex-late.txt", "SG107", High, "main.(*store).Get", 40, "main.(*store).Refresh"},
		{"mutex-debug1.txt", "SG107", High, "main.(*store).Get", 40, "main.(*store).Refresh"},
		{"waitgroup-late.txt", "SG108", Low, "main.waitgroup", 1, "main.process"},
		{"leak-late.txt", "SG109", High, "main.lookupPrice.func1", 100, "stuck sending"},
		{"netread-late.txt", "SG111", Medium, "main.readReply", 1, "network I/O"},
		{"sleepers-late.txt", "SG114", Low, "main.poll", 150, "sleeping"},
		{"leak-debug1.txt", "SG117", Medium, "main.lookupPrice.func1", 100, "parked"},
		{"go1.22/mutex-late.txt", "SG107", High, "main.(*store).Get", 40, "main.(*store).Refresh"},
		{"go1.22/waitgroup-late.txt", "SG108", Low, "main.waitgroup", 1, "main.process"},
		{"go1.22/sqlpool-late.txt", "SG105", High, "main.loadCustomer", 28, "free database connection"},
		{"go1.22/leak-late.txt", "SG109", High, "main.lookupPrice.func1", 100, "stuck sending"},
	}
	for _, c := range cases {
		t.Run(c.file+"/"+c.id, func(t *testing.T) {
			review := Analyze(loadExample(t, c.file))
			finding := find(review, c.id)
			if finding == nil {
				t.Fatalf("no %s in %+v", c.id, review.Findings)
			}
			if finding.Severity != c.severity {
				t.Errorf("severity %s, want %s", finding.Severity, c.severity)
			}
			if !strings.HasPrefix(finding.Where, c.where) {
				t.Errorf("where %q, want %q", finding.Where, c.where)
			}
			if finding.Count < c.atLeast {
				t.Errorf("count %d, want at least %d", finding.Count, c.atLeast)
			}
			if !strings.Contains(finding.Message, c.message) {
				t.Errorf("message %q lacks %q", finding.Message, c.message)
			}
		})
	}
}

// Healthy programs must stay quiet, or nobody will trust the rest.
func TestHealthyDumpsHaveNoFindings(t *testing.T) {
	for _, name := range []string{"healthy-early.txt", "http2-late.txt", "workers-late.txt", "sigquit.txt", "go1.22/healthy-early.txt", "go1.22/http2-late.txt"} {
		review := Analyze(loadExample(t, name))
		if len(review.Findings) != 0 {
			t.Errorf("%s: %+v", name, review.Findings)
		}
	}
}

// Real services, left running: they must not raise anything above low.
func TestRealServicesStayQuiet(t *testing.T) {
	for _, name := range []string{"real/prometheus-3.7.3.txt", "real/prometheus-3.7.3-debug1.txt", "real/etcd-3.6.5.txt", "real/etcd-3.6.5-debug1.txt"} {
		review := Analyze(loadExample(t, name))
		if review.Total < 20 || review.HasFindingAt(High, Medium) {
			t.Errorf("%s: %d goroutines, %+v", name, review.Total, review.Findings)
		}
	}
}

func TestCompareOnAHealthyServiceIsQuiet(t *testing.T) {
	result := Compare(loadExample(t, "real/etcd-3.6.5-early.txt"), loadExample(t, "real/etcd-3.6.5.txt"))
	if len(result.Growing) != 0 || len(result.Parked) != 0 || result.ParkedElsewhere == 0 {
		t.Errorf("%+v", result)
	}
}

func TestHealthyServerIsMarkedIdle(t *testing.T) {
	review := Analyze(loadExample(t, "healthy-early.txt"))
	if review.Expected < 5 {
		t.Errorf("expected idle %d of %d", review.Expected, review.Total)
	}
	for _, group := range review.Groups {
		if !group.Expected && !strings.HasPrefix(group.Where, "main.") {
			t.Errorf("not marked idle: %+v", group)
		}
	}
}

func TestOneGoroutineDumpSaysSo(t *testing.T) {
	review := Analyze(loadExample(t, "panic-single.txt"))
	if review.Total != 1 || !strings.Contains(strings.Join(review.Notes, " "), "GOTRACEBACK=all") {
		t.Errorf("%+v", review)
	}
}

func TestSignalIsNotACrash(t *testing.T) {
	parsed := loadExample(t, "sigquit.txt")
	if parsed.Signal == "" || parsed.Fatal != "" {
		t.Errorf("signal %q fatal %q", parsed.Signal, parsed.Fatal)
	}
	for _, g := range parsed.Goroutines {
		if g.ID == 0 {
			t.Error("kept the scheduler's goroutine 0")
		}
	}
}

func TestDebug1StatesAreOnlyInferredWhenVisible(t *testing.T) {
	review := Analyze(loadExample(t, "leak-debug1.txt"))
	if review.Format != FormatProfile || review.Total < 100 {
		t.Fatalf("%+v", review)
	}
	if review.Groups[0].State != Unknown {
		t.Errorf("a debug=1 channel wait can't be told from a select: %s", review.Groups[0].State)
	}
	if !strings.Contains(strings.Join(review.Notes, " "), "debug=2") {
		t.Error("should suggest debug=2")
	}
}

func TestCompareShowsGrowthAndGoroutinesThatNeverMoved(t *testing.T) {
	result := Compare(loadExample(t, "leak-early.txt"), loadExample(t, "leak-late.txt"))
	if result.After <= result.Before || len(result.Growing) != 1 || !strings.HasPrefix(result.Growing[0].Where, "main.lookupPrice.func1") {
		t.Fatalf("%+v", result)
	}
	if len(result.Parked) != 1 || !result.Parked[0].Growing || result.Parked[0].Count < 100 || result.Parked[0].MaxWaitMinutes == 0 {
		t.Fatalf("parked %+v", result.Parked)
	}
	if len(result.Remaining) != 1 {
		t.Errorf("remaining %+v", result.Remaining)
	}
}

func TestCompareAcrossFormatsSaysWhatItCantShow(t *testing.T) {
	result := Compare(loadExample(t, "leak-debug1.txt"), loadExample(t, "leak-late.txt"))
	if len(result.Parked) != 0 || !strings.Contains(strings.Join(result.Notes, " "), "different formats") {
		t.Errorf("%+v", result)
	}
}

func TestCompareFixedAndNew(t *testing.T) {
	result := Compare(loadExample(t, "mutex-late.txt"), loadExample(t, "leak-late.txt"))
	if len(result.Fixed) == 0 || len(result.New) == 0 || len(result.Shrinking) == 0 {
		t.Errorf("%+v", result)
	}
}

func TestDumpsWithTimestampsAndIndentsReadTheSame(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "examples", "dumps", "mutex-late.txt"))
	if err != nil {
		t.Fatal(err)
	}
	var stamped, indented strings.Builder
	for _, line := range strings.Split(string(data), "\n") {
		stamped.WriteString("2026-11-01T12:00:00.123456789Z " + line + "\n")
		indented.WriteString("    " + line + "\r\n")
	}
	want := Analyze(loadExample(t, "mutex-late.txt"))
	for name, text := range map[string]string{"stamped": stamped.String(), "indented": indented.String()} {
		parsed, err := Parse(text)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got := Analyze(parsed)
		if got.Total != want.Total || len(got.Findings) != len(want.Findings) || got.Findings[0].Message != want.Findings[0].Message {
			t.Errorf("%s: %+v", name, got.Findings)
		}
	}
}

func TestHeaderVariants(t *testing.T) {
	text := `goroutine 7 gp=0xc000 m=nil [chan send (leaked), 12 minutes, locked to thread]:
main.a(0x1)
	/app/a.go:1 +0x1
goroutine 8 gp=0xc000 m=3 mp=0xc001 [sync.Mutex.Lock (scan), 3 minutes] {request: "abc", user: x}:
main.b[...](...)
	/app/b.go:2
created by main.c in goroutine 1
	/app/c.go:3 +0x1
goroutine 9 [running]:
	goroutine running on other thread; stack unavailable
`
	parsed, err := Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Goroutines) != 3 {
		t.Fatalf("%+v", parsed.Goroutines)
	}
	first, second := parsed.Goroutines[0], parsed.Goroutines[1]
	if first.State != "chan send" || !first.Leaked || !first.Locked || first.WaitMinutes != 12 || first.Frames[0].Func != "main.a" {
		t.Errorf("%+v", first)
	}
	if second.State != "sync.Mutex.Lock" || second.WaitMinutes != 3 || second.CreatedBy != "main.c" || second.CreatedAt != "/app/c.go:3" || second.Frames[0].Func != "main.b[...]" {
		t.Errorf("%+v", second)
	}
	if where(parsed.Goroutines[2].Frames) != "stack unavailable" {
		t.Errorf("%+v", parsed.Goroutines[2])
	}
}

func TestNotADump(t *testing.T) {
	for _, text := range []string{"", "hello\nworld\n", "goroutines are great"} {
		if _, err := Parse(text); err == nil {
			t.Errorf("%q parsed", text)
		}
	}
}

func TestLogLinesAroundADumpAreIgnored(t *testing.T) {
	text := "level=info msg=\"starting (v2)\"\n\ngoroutine 1 [select]:\nmain.a()\n\t/app/a.go:1 +0x1\n\nlevel=info msg=\"later (done)\"\n"
	parsed, err := Parse(text)
	if err != nil || len(parsed.Goroutines) != 1 || len(parsed.Goroutines[0].Frames) != 1 {
		t.Fatalf("%v %+v", err, parsed)
	}
}

func TestCutOffDumpIsNoted(t *testing.T) {
	parsed, err := Parse("goroutine 1 [select]:\nmain.a()\n\t/app/a.go:1 +0x1\ngoroutine 2 [select]:\nmain.b()")
	if err != nil {
		t.Fatal(err)
	}
	if !parsed.CutOff || !strings.Contains(strings.Join(Analyze(parsed).Notes, " "), "cut off") {
		t.Errorf("%+v", parsed)
	}
}

func TestElidedFramesAreNoted(t *testing.T) {
	parsed, err := Parse("goroutine 1 [select]:\nmain.a()\n\t/app/a.go:1 +0x1\n...50 frames elided...\nmain.b()\n\t/app/a.go:2 +0x1\n")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(Analyze(parsed).Notes, " "), "left out") {
		t.Errorf("%+v", parsed)
	}
}

func TestStdlibOrUserCode(t *testing.T) {
	for function, want := range map[string]bool{
		"net/http.(*Client).do":              true,
		"sync.(*Mutex).Lock":                 true,
		"internal/sync.(*Mutex).lockSlow":    true,
		"main.main":                          false,
		"example/app/store.Get":              false,
		"github.com/acme/net/http.Get":       false,
		"golang.org/x/net/http2.(*Conn).run": false,
	} {
		if isStdlib(function) != want {
			t.Errorf("%s: want stdlib %v", function, want)
		}
	}
}

func TestReentrantLockIsPointedOut(t *testing.T) {
	text := "goroutine 5 [sync.Mutex.Lock, 2 minutes]:\nsync.(*Mutex).Lock(...)\n\t/go/src/sync/mutex.go:46\nmain.(*cache).load(0x1)\n\t/app/c.go:9 +0x1\nmain.(*cache).Get(0x1)\n\t/app/c.go:5 +0x1\n"
	parsed, err := Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	finding := find(Analyze(parsed), "SG107")
	if finding == nil || !strings.Contains(finding.Message, "may already hold") {
		t.Errorf("%+v", finding)
	}
}

// Rules below have no example program yet, so these are written by hand.
func TestWrittenRules(t *testing.T) {
	repeat := func(count int, block string) string {
		var text strings.Builder
		for index := 1; index <= count; index++ {
			text.WriteString("goroutine " + itoa(index) + " " + block)
		}
		return text.String()
	}
	cases := []struct {
		name, text, id, severity string
	}{
		{"idle pool", repeat(150, "[chan receive]:\nmain.worker()\n\t/app/w.go:1 +0x1\n\n"), "SG110", Medium},
		{"huge pool", repeat(1000, "[select]:\nmain.worker()\n\t/app/w.go:1 +0x1\n\n"), "SG110", High},
		{"syscall", repeat(1, "[syscall, 3 minutes]:\nmain.read()\n\t/app/w.go:1 +0x1\n\n"), "SG112", Low},
		{"cond", repeat(10, "[sync.Cond.Wait, 3 minutes]:\nsync.runtime_notifyListWait(0x1, 0x1)\n\t/go/src/runtime/sema.go:1 +0x1\nmain.next()\n\t/app/w.go:1 +0x1\n\n"), "SG113", Low},
		{"cpu", repeat(100, "[runnable]:\nmain.spin()\n\t/app/w.go:1 +0x1\n\n"), "SG116", Low},
	}
	for _, c := range cases {
		parsed, err := Parse(c.text)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		finding := find(Analyze(parsed), c.id)
		if finding == nil || finding.Severity != c.severity {
			t.Errorf("%s: %+v", c.name, finding)
		}
	}
}

func itoa(value int) string {
	digits := ""
	for ; value > 0; value /= 10 {
		digits = string(rune('0'+value%10)) + digits
	}
	return digits
}

func TestOneCreatorAcrossStacks(t *testing.T) {
	var text strings.Builder
	for index := 1; index <= 120; index++ {
		line := "1"
		if index%2 == 0 {
			line = "2"
		}
		text.WriteString("goroutine " + itoa(index) + " [sleep]:\nmain.task()\n\t/app/w.go:" + line + " +0x1\ncreated by main.spawn in goroutine 1\n\t/app/s.go:4 +0x1\n\n")
	}
	parsed, err := Parse(text.String())
	if err != nil {
		t.Fatal(err)
	}
	finding := find(Analyze(parsed), "SG115")
	if finding == nil || finding.Where != "main.spawn" || finding.Count != 120 {
		t.Errorf("%+v", finding)
	}
}

func TestGroupsAreLimited(t *testing.T) {
	var text strings.Builder
	for index := 1; index <= groupLimit+5; index++ {
		text.WriteString("goroutine " + itoa(index) + " [select]:\nmain.task()\n\t/app/w.go:" + itoa(index) + " +0x1\n\n")
	}
	parsed, err := Parse(text.String())
	if err != nil {
		t.Fatal(err)
	}
	review := Analyze(parsed)
	if len(review.Groups) != groupLimit || review.GroupCount != groupLimit+5 {
		t.Errorf("%d of %d", len(review.Groups), review.GroupCount)
	}
}
