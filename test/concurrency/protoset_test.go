package concurrency

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/descriptorpb"
)

// TestProtosetClosed (Q1): k6's loadProtoset hands the set to protodesc.NewFiles without walking imports, so the set holds
// every transitive import, well-known types included.
func TestProtosetClosed(t *testing.T) {
	raw, err := Protoset()
	require.NoError(t, err)
	set := &descriptorpb.FileDescriptorSet{}
	require.NoError(t, proto.Unmarshal(raw, set))

	files, err := protodesc.NewFiles(set)
	require.NoError(t, err)
	_, err = files.FindFileByPath("google/protobuf/struct.proto")
	require.NoError(t, err)
	for _, service := range []string{"flagd.evaluation.v2.Service", "flagd.sync.v1.FlagSyncService"} {
		_, err := files.FindDescriptorByName(protoFullName(service))
		require.NoError(t, err, service)
	}
}
