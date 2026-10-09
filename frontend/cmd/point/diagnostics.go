package main

// Opt-in diagnostics for settling the "is memory actually growing" question
// directly, rather than by inference from Task Manager — which reports the
// whole process tree (Windows Terminal's own scrollback buffer and GPU
// surfaces included), not Point's Go process alone, and can move for
// reasons that have nothing to do with this program.
//
// Neither of these runs unless explicitly asked for by an environment
// variable — a normal `go run .\frontend\cmd\point` behaves exactly as
// before.

import (
	"log/slog"
	"net"
	"net/http"
	_ "net/http/pprof" // registers /debug/pprof/* on the default mux; served loopback-only below
	"os"
	"runtime"
	"strconv"
	"time"
)

// startMemLog logs Go's own runtime.MemStats at a steady interval when
// POINT_MEMLOG is set — "1" (or anything that doesn't parse as a positive
// integer) for the 5-second default, or a number of seconds directly
// (POINT_MEMLOG=2 for a tighter interval during a short manual test).
//
// Each tick forces a GC before reading, matching exactly what
// TestRepeatedGReRepinsAndDoesNotLeak in frontend\internal\chat does before
// its own before/after ReadMemStats — the same measurement, not a
// Task-Manager-shaped proxy for it, so a real organic scroll session is
// directly comparable to that test's result instead of argued from a
// different number.
func startMemLog(log *slog.Logger) (stop func()) {
	raw, on := os.LookupEnv("POINT_MEMLOG")
	if !on {
		return func() {}
	}
	interval := 5 * time.Second
	if secs, err := strconv.Atoi(raw); err == nil && secs > 0 {
		interval = time.Duration(secs) * time.Second
	}

	log.Info("memlog enabled", "interval", interval.String(),
		"note", "forces a GC before every reading — see diagnostics.go")

	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		var m runtime.MemStats
		for {
			select {
			case <-ticker.C:
				runtime.GC()
				runtime.ReadMemStats(&m)
				log.Info("memlog",
					"heap_alloc", m.HeapAlloc,
					"heap_objects", m.HeapObjects,
					"heap_sys", m.HeapSys,
					"mallocs", m.Mallocs,
					"frees", m.Frees,
					"num_gc", m.NumGC,
					"goroutines", runtime.NumGoroutine(),
				)
			case <-done:
				return
			}
		}
	}()
	return func() { close(done) }
}

// The port is 6061, not pprof's conventional 6060, and it has been since
// F3 (design\completed\frontend\F03-chat-viewport.md records it). F12's task
// doc asks for 6060 in its own Done-when #7 command, which is simply a wrong
// number in that doc — a verification round spent a step on "nothing is
// listening on 6060" because of it. The code is the truth here, per
// CLAUDE.md, so the number stays and the docs get corrected.
const pprofAddr = "127.0.0.1:6061"

// startPprof serves net/http/pprof on loopback only, when POINT_PPROF is
// set — the "settle it definitively" option: if startMemLog's counter shows
// real growth, `go tool pprof` against /debug/pprof/heap points at exactly
// which allocation site is holding the memory, rather than which package is
// suspected of it.
//
// 127.0.0.1 only, never 0.0.0.0 — this is a loopback dev diagnostic, not a
// network service, and binding it that way is what keeps it from being one
// by accident. Note that it is 127.0.0.1 and NOT `localhost`: on a machine
// where `localhost` resolves to ::1 first, a browser or curl aimed at
// `localhost` can be refused while the server is up and perfectly healthy.
// Both the log line below and every doc that names this URL use the literal
// address for that reason.
//
// # The listen is synchronous
//
// It used to spawn ListenAndServe in a goroutine and then log "pprof
// enabled" unconditionally, which meant the one line a user would check
// claimed success before the bind had been attempted at all: a port already
// in use logged "enabled" and then, separately and later, a Warn that is
// easy to miss above a screenful of startup. net.Listen happens here, in
// line, so the log states what actually happened and names the address it
// really bound.
func startPprof(log *slog.Logger) (stop func()) {
	if _, on := os.LookupEnv("POINT_PPROF"); !on {
		return func() {}
	}
	ln, err := net.Listen("tcp", pprofAddr)
	if err != nil {
		// Loud, and not fatal: a diagnostic that cannot start must say so
		// in the one place someone will look, but must never stop the app
		// it was only meant to measure.
		log.Error("pprof NOT started — nothing will be listening",
			"addr", pprofAddr, "err", err)
		return func() {}
	}
	srv := &http.Server{}
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Warn("pprof server stopped", "err", err)
		}
	}()
	log.Info("pprof listening", "addr", ln.Addr().String(),
		"url", "http://"+ln.Addr().String()+"/debug/pprof/",
		"heap_profile", "go tool pprof -top http://"+ln.Addr().String()+"/debug/pprof/heap")
	return func() { _ = srv.Close() }
}
