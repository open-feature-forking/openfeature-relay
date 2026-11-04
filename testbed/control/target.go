package control

import (
	"context"
	"errors"
)

// Target is what the control API drives: the thing under test. The flagd testbed implements it with the runtime and
// the gate of its listeners; the testbed of another provider SDK or vendor implements it with its own server.
//
// A Target is called from one goroutine at a time: the server serializes every call, including the delayed start of
// /restart (see Server). A Target may therefore hold no lock of its own around a call, and it must not call back into
// the server except through Server.Document, which never blocks.
type Target interface {
	// Start brings the target to a clean, ready state for the configuration, and leaves it closed to its clients. It
	// returns when the target is ready, or when ctx ends, which is the readiness timeout (Config.StartTimeout). A
	// start with the configuration in use cleans the target's store and loads every source again; a start with another
	// configuration may rebuild the target. An unknown configuration is an error that wraps ErrUnknownConfiguration,
	// and leaves the target as it was.
	//
	// What the sources of the target read is theirs to arrange. The combined flag document is the exception: the
	// server holds it, and the target reads it from Server.Document when it loads that source.
	//
	// The server renders the combined flag document once Start has returned, and only then calls Open, so that a
	// client that reads the file never finds the target open and the file missing.
	Start(ctx context.Context, config string) error

	// Open opens the ready target to its clients. It is called after Start, and after the render.
	Open(ctx context.Context) error

	// Stop closes the target to its clients and leaves it running behind, so that a later Start is quick. It is called
	// for /stop, for the first half of /restart, and after a Start or an Open that failed.
	Stop(ctx context.Context) error

	// Load hands the target the combined flag document after a /change, and returns once the target holds that
	// version, whether or not it is open to its clients: a /change during an outage loads and renders as well. The
	// version is the digest of the document, and the target must tie it to the delivery it triggers (the fetch
	// version of its source), because what it stores has versions of its own. Load returns the rejection when the
	// target's format refuses the document, and ctx's error when the target did not take it in before ctx ended
	// (Config.ChangeTimeout). The server takes a refusal back and keeps the document on a timeout, which settles
	// nothing: the target may still take it in late. A target that has no runtime to load into, such as after a
	// configuration switch that failed, returns ErrNoRuntime at once and does not wait.
	Load(ctx context.Context, document []byte, version string) error

	// Ready reports whether the target is ready for its clients, open or not. The server asks after Start has
	// returned.
	Ready() bool
}

var (
	// ErrUnknownConfiguration is what Start returns, wrapped, for a configuration the target does not have. The server
	// answers it at once, naming the configuration, and leaves the last good configuration remembered.
	ErrUnknownConfiguration = errors.New("unknown configuration")

	// ErrNoRuntime is what Load returns when there is nothing to load into.
	ErrNoRuntime = errors.New("no runtime")
)
