// Package upgrade is the differential test of UC-009: today's flagd and the flagd-full that the Builder made run side
// by side, with the same start command, configuration file, environment, sources and requests, and every difference
// they show is either listed in accepted.yaml or fails the run.
//
// The baseline is flagd built from the upstream merge-base of this repository with upstream's own
// flagd/build.Dockerfile; the candidate is built from this working tree with the same Dockerfile. Both images get the
// same build arguments, so their version output is identical on purpose.
package upgrade
