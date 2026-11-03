//go:build race

package evaluation

// raceEnabled reports whether the tests run under the race detector, which makes sync.Pool drop buffers at random.
const raceEnabled = true
