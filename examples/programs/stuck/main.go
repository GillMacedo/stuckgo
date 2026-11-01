// Command stuck gets stuck on purpose, so the dumps in examples/ come from real
// programs. Every scenario is made up and uses only the standard library.
//
//	stuck <scenario> [dump-prefix]
//
// It writes <prefix>-early.txt, <prefix>-late.txt and <prefix>-debug1.txt from
// its own goroutine profile, 70 and 130 seconds in, so waits are over a minute.
package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"runtime/pprof"
	"sync"
	"time"
)

var never = make(chan struct{})

func main() {
	scenario, prefix := os.Args[1], "dump"
	if len(os.Args) > 2 {
		prefix = os.Args[2]
	}
	scenarios := map[string]func(){
		"leak": leak, "workers": workers, "mutex": mutex, "waitgroup": waitgroup, "nilchan": nilchan,
		"sqlpool": sqlpool, "httpclient": httpclient, "netread": netread, "healthy": healthy,
		"sleepers": sleepers, "http2": http2,
	}
	run, ok := scenarios[scenario]
	if !ok {
		fmt.Fprintln(os.Stderr, "unknown scenario")
		os.Exit(2)
	}
	go run()
	// The runtime only starts a wait's clock at a garbage collection.
	time.Sleep(5 * time.Second)
	runtime.GC()
	time.Sleep(65 * time.Second)
	write(prefix+"-early.txt", 2)
	time.Sleep(60 * time.Second)
	write(prefix+"-late.txt", 2)
	write(prefix+"-debug1.txt", 1)
}

func write(path string, debug int) {
	file, err := os.Create(path)
	if err != nil {
		panic(err)
	}
	defer file.Close()
	if err := pprof.Lookup("goroutine").WriteTo(file, debug); err != nil {
		panic(err)
	}
}

// leak is the classic one: a timed-out caller stops reading, and the sender blocks forever.
func leak() {
	for {
		lookupPrice(context.Background(), "sku")
		time.Sleep(200 * time.Millisecond)
	}
}

func lookupPrice(ctx context.Context, sku string) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Millisecond)
	defer cancel()
	result := make(chan int)
	go func() {
		time.Sleep(5 * time.Millisecond)
		result <- len(sku)
	}()
	select {
	case price := <-result:
		return price, nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

// workers is healthy: a pool waiting for jobs looks stuck in a dump, and isn't.
func workers() {
	jobs := make(chan int)
	for i := 0; i < 8; i++ {
		go worker(jobs)
	}
	<-never
}

func worker(jobs <-chan int) {
	for range jobs {
	}
}

type store struct {
	mu   sync.Mutex
	data map[string]int
}

func (s *store) Get(key string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data[key]
}

func (s *store) Refresh(conn net.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Holds the lock across a network read with no deadline.
	io.ReadAll(conn)
}

func mutex() {
	client, server := net.Pipe()
	defer server.Close()
	s := &store{data: map[string]int{}}
	go s.Refresh(client)
	time.Sleep(100 * time.Millisecond)
	for i := 0; i < 40; i++ {
		go s.Get("key")
	}
	<-never
}

func waitgroup() {
	var group sync.WaitGroup
	for i := 0; i < 3; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			process()
		}()
	}
	group.Wait()
}

func process() { <-never }

func nilchan() {
	var results chan int
	go publish(results)
	<-never
}

func publish(results chan int) { results <- 1 }

// A driver whose queries never return, so the pool fills up.
type stuckDriver struct{}
type stuckConn struct{}
type stuckStmt struct{}

func (stuckDriver) Open(string) (driver.Conn, error)         { return stuckConn{}, nil }
func (stuckConn) Prepare(string) (driver.Stmt, error)        { return stuckStmt{}, nil }
func (stuckConn) Close() error                               { return nil }
func (stuckConn) Begin() (driver.Tx, error)                  { return nil, driver.ErrSkip }
func (stuckStmt) Close() error                               { return nil }
func (stuckStmt) NumInput() int                              { return 0 }
func (stuckStmt) Exec([]driver.Value) (driver.Result, error) { return nil, driver.ErrSkip }
func (stuckStmt) Query([]driver.Value) (driver.Rows, error) {
	<-never
	return nil, nil
}

func sqlpool() {
	sql.Register("stuck", stuckDriver{})
	db, err := sql.Open("stuck", "")
	if err != nil {
		panic(err)
	}
	db.SetMaxOpenConns(2)
	for i := 0; i < 30; i++ {
		go loadCustomer(db)
	}
	<-never
}

func loadCustomer(db *sql.DB) {
	rows, err := db.Query("SELECT 1")
	if err == nil {
		rows.Close()
	}
}

// httpclient calls a server that never answers, with no timeout on the client.
func httpclient() {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-never }))
	for i := 0; i < 12; i++ {
		go callPartner(server.URL)
	}
	<-never
}

func callPartner(url string) {
	response, err := http.Get(url)
	if err == nil {
		response.Body.Close()
	}
}

func netread() {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			defer conn.Close()
		}
	}()
	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		panic(err)
	}
	go readReply(conn)
	<-never
}

func readReply(conn net.Conn) {
	buffer := make([]byte, 16)
	conn.Read(buffer)
}

// healthy is a normal idle server: a listener, keep-alive connections and client read loops.
func healthy() {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "ok") }))
	client := &http.Client{Timeout: 5 * time.Second}
	for i := 0; i < 4; i++ {
		response, err := client.Get(server.URL)
		if err != nil {
			panic(err)
		}
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
	}
	var concurrent sync.WaitGroup
	for i := 0; i < 6; i++ {
		concurrent.Add(1)
		go func() {
			defer concurrent.Done()
			response, err := client.Get(server.URL)
			if err == nil {
				io.Copy(io.Discard, response.Body)
				response.Body.Close()
			}
		}()
	}
	concurrent.Wait()
	<-never
}

func sleepers() {
	for i := 0; i < 150; i++ {
		go poll()
	}
	<-never
}

func poll() {
	for {
		time.Sleep(time.Hour)
	}
}

// http2 is healthy too, with idle HTTP/2 connections on both sides.
func http2() {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "ok") }))
	server.EnableHTTP2 = true
	server.StartTLS()
	client := server.Client()
	client.Timeout = 5 * time.Second
	for i := 0; i < 4; i++ {
		response, err := client.Get(server.URL)
		if err != nil {
			panic(err)
		}
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
	}
	<-never
}
