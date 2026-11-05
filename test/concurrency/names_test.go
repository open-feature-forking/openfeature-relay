package concurrency

import "google.golang.org/protobuf/reflect/protoreflect"

func protoFullName(s string) protoreflect.FullName { return protoreflect.FullName(s) }
