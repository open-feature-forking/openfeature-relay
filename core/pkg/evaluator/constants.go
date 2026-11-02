package evaluator

const (
	// SelectorMetadataKey is the metadata key under which the selector of a query is reported.
	SelectorMetadataKey = "scope"
	// ProtoVersionKey marks, in a context, that the request comes from an older proto source, which has
	// different fallback behavior.
	ProtoVersionKey = "__flagd.protoVersion__"
)
