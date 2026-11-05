package runtime

// Ready reports whether the runtime is ready as /readyz reports it: every source accepted, every provider ready and
// no source stale. A program that runs the runtime with Run and cannot ask the management port, because it keeps that
// port behind a gate, asks here.
func (r *Runtime) Ready() bool { return r.isReady() }
