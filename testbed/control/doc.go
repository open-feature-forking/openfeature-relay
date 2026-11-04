// Package control is the control API of a provider testbed: the four calls /start, /stop, /restart and /change that the
// provider test suites drive, as the flagd testbed's launchpad serves them, on a listener of its own.
//
// The package knows no flagd type. A Target supplies the behaviour: it starts and stops what is under test, and takes in
// a new combined flag document. The server supplies everything else, the same for every target: the calls, their
// texts and status codes, the delay of /restart and the cancellation of a pending restart by a later call, the
// timeouts, the in-memory state of the combined flag document with its toggled flag, and the rendering of that state
// to a file for providers that read it directly.
//
// A testbed for another provider SDK, or for another vendor, implements Target and reuses the server and its tests.
// The package imports the standard library only: it sits above the runtime and outlives it, so it cannot import it.
//
// Compatible with the launchpad of flagd-testbed v3.11.0 for every case that works there. Three quirks are fixed:
//   - a failed /start leaves the last good configuration remembered;
//   - an unknown configuration is answered at once, naming it, instead of after the readiness timeout;
//   - /change toggles the flag in memory and rewrites no file of the raw flags.
package control
