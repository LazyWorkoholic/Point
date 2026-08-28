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

// startPprof serves net/http/pprof on loopback only, when POINT_PPROF is
// set — the "settle it definitively" option: if startMemLog's counter shows
// real growth, `go tool pprof` against /debug/pprof/heap points at exactly
// which allocation site is holding the memory, rather than which package is
// suspected of it.
//
// 127.0.0.1 only, never 0.0.0.0 — this is a loopback dev diagnostic, not a
// network service, and binding it that way is what keeps it from being one
// by accident.
func startPprof(log *slog.Logger) (stop func()) {
	if _, on := os.LookupEnv("POINT_PPROF"); !on {
		return func() {}
	}
	const addr = "127.0.0.1:6061"
	srv := &http.Server{Addr: addr}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Warn("pprof server stopped", "err", err)
		}
	}()
	log.Info("pprof enabled", "url", "http://"+addr+"/debug/pprof/",
		"heap_profile", "go tool pprof http://"+addr+"/debug/pprof/heap")
	return func() { _ = srv.Close() }
}
