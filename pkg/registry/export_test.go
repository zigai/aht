package registry

import "time"

// ReconcilingWatchOptions returns opts with a reconcile interval, so tests do not wait for the default.
func ReconcilingWatchOptions(opts WatchOptions, interval time.Duration) WatchOptions {
	opts.reconcileInterval = interval

	return opts
}
