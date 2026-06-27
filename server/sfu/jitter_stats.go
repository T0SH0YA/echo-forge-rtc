package main

import "sync/atomic"

// Counters da Etapa 15 (jitter buffer + NACK upstream).
var (
	jbPush    atomic.Uint64
	jbEmit    atomic.Uint64
	jbLate    atomic.Uint64
	jbDup     atomic.Uint64
	jbSkip    atomic.Uint64
	jbNackOut atomic.Uint64
)
