package concurrency

import (
	"fmt"

	evalv1 "buf.build/gen/go/open-feature/flagd/protocolbuffers/go/flagd/evaluation/v1"
	evalv2 "buf.build/gen/go/open-feature/flagd/protocolbuffers/go/flagd/evaluation/v2"
	syncv1 "buf.build/gen/go/open-feature/flagd/protocolbuffers/go/flagd/sync/v1"
	schemav1 "buf.build/gen/go/open-feature/flagd/protocolbuffers/go/schema/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

// Protoset returns the serialized descriptor set k6 loads (loadProtoset): the files of the services flagd-minimal serves
// on its evaluation and sync ports, written from the Go packages flagd links, and every file they import, dependencies
// first. k6 does not walk imports.
func Protoset() ([]byte, error) {
	set := &descriptorpb.FileDescriptorSet{}
	seen := map[string]bool{}
	var add func(protoreflect.FileDescriptor)
	add = func(fd protoreflect.FileDescriptor) {
		if seen[fd.Path()] {
			return
		}
		seen[fd.Path()] = true
		imports := fd.Imports()
		for i := 0; i < imports.Len(); i++ {
			add(imports.Get(i).FileDescriptor)
		}
		set.File = append(set.File, protodesc.ToFileDescriptorProto(fd))
	}
	for _, fd := range []protoreflect.FileDescriptor{
		evalv1.File_flagd_evaluation_v1_evaluation_proto,
		evalv2.File_flagd_evaluation_v2_evaluation_proto,
		schemav1.File_schema_v1_schema_proto,
		syncv1.File_flagd_sync_v1_sync_proto,
	} {
		add(fd)
	}
	out, err := proto.Marshal(set)
	if err != nil {
		return nil, fmt.Errorf("marshalling the protoset: %w", err)
	}
	return out, nil
}
