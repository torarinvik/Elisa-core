package semantic

import "sync/atomic"

// transientSMTObserved is process-wide and sticky: an outer command cache must
// account for every analysis, including failed and private analyses.
var transientSMTObserved atomic.Bool

func markTransientSMT() { transientSMTObserved.Store(true) }

// CacheInvocationCertifiable reports whether this process observed an SMT
// outcome that could change if the request were run again.
func CacheInvocationCertifiable() bool { return !transientSMTObserved.Load() }
