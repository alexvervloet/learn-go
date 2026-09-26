package redblack

import "runtime"

// A thin alias so the size test reads without runtime noise in the middle of it.
type memStats = runtime.MemStats

func readMemStats(m *memStats) {
	runtime.GC() // TotalAlloc is cumulative, but a GC first keeps the reading tidy
	runtime.ReadMemStats(m)
}
