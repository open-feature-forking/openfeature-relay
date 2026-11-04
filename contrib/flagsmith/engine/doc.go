// Package flagsmith is an engine module for Flagsmith: the configuration format of Flagsmith's environment document and
// the evaluator that resolves it with Flagsmith's own Go engine (FR-048, UC-019 BR-005). It imports only the core packages
// a configuration format and an evaluator may, and no transport or telemetry.
//
// Each source holds the environment document of one environment, kept as one document whose flag set is the
// environment's API key, the client key, which Flagsmith publishes to its client SDKs.
//
// The engine hashes with MD5 to place an identity in a percentage split, so a distribution that contains this module
// has no FIPS 140-3 variant.
package flagsmith
