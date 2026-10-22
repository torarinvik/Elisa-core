package semantic

// Worker profiles start at zero. Counters and process time add, while the
// slowest individual query remains a maximum across all solver sessions.
func mergeParallelSMTStats(dst *SMTStats, worker SMTStats) {
	dst.Enabled = dst.Enabled || worker.Enabled
	dst.Attempts += worker.Attempts
	dst.Proven += worker.Proven
	dst.Declined += worker.Declined
	dst.SolverProven += worker.SolverProven
	dst.PersistentCacheHits += worker.PersistentCacheHits
	dst.CacheHits += worker.CacheHits
	dst.SpawnTime += worker.SpawnTime
	dst.SolverTime += worker.SolverTime
	dst.Slowest = max(dst.Slowest, worker.Slowest)
}
