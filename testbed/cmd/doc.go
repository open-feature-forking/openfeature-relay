// Package cmd is the command of the flagd testbed distribution (UC-022): the generated main of
// distributions/flagd-testbed calls Execute, which serves the control API of testbed/control and drives a flagd runtime
// as its target.
//
// The runtime listens through a gate, so that /stop and /restart close its ports the way a killed flagd does, and it
// reads the combined flag document from the testbed source, so that it reads no file the testbed changes. A start with
// the configuration in use resets the runtime's store; a start with another configuration builds a new runtime in the
// same process.
package cmd
