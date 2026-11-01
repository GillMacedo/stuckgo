// Command crash fails on purpose, so examples/ has real deadlock, panic and SIGQUIT output.
package main

import (
	"os"
	"runtime/pprof"
	"time"
)

func process(c chan int) { <-c }

func deadlock() {
	first, second := make(chan int), make(chan int)
	go func() { second <- <-first }()
	first <- <-second
}

func crash() {
	c := make(chan int)
	go process(c)
	go process(c)
	time.Sleep(100 * time.Millisecond)
	var counts map[string]int
	counts["boom"]++
}

func concurrent() {
	counts := map[int]int{}
	for i := 0; i < 8; i++ {
		go func() {
			for {
				counts[i]++
			}
		}()
	}
	time.Sleep(10 * time.Second)
}

// leakProfile prints Go's own leak profile, which Go 1.27 has with no setup.
func leakProfile() {
	for i := 0; i < 5; i++ {
		go stuck()
	}
	time.Sleep(time.Second)
	debug := 2
	if len(os.Args) > 2 {
		debug = 1
	}
	pprof.Lookup("goroutineleak").WriteTo(os.Stdout, debug)
}

func stuck() {
	result := make(chan int)
	result <- 1
}

func main() {
	switch os.Args[1] {
	case "leakprofile":
		leakProfile()
	case "deadlock":
		deadlock()
	case "panic":
		crash()
	case "map":
		concurrent()
	case "wait":
		c := make(chan int)
		for i := 0; i < 3; i++ {
			go process(c)
		}
		time.Sleep(time.Hour)
	}
}
