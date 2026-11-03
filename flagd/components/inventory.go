package components

import (
	"github.com/open-feature-forking/openfeature-relay/core/pkg/distribution"
)

const relay = "github.com/open-feature-forking/openfeature-relay/"

// syncModules is the module that holds each official sync component, by the type its factory gives.
var syncModules = map[string]string{
	"file":       relay + "sync/file",
	"fsnotify":   relay + "sync/file",
	"fileinfo":   relay + "sync/file",
	"kubernetes": relay + "sync/openfeature-operator",
	"http":       relay + "sync/http",
	"grpc":       relay + "sync/flagd-grpc",
	"gcs":        relay + "sync/gcs",
	"azblob":     relay + "sync/azblob",
	"s3":         relay + "sync/s3",
}

// endpointModules is the module that holds each service endpoint, by the type its factory gives.
var endpointModules = map[string]string{
	"flag-evaluation": relay + "service/flag-evaluation",
	"ofrep":           relay + "service/ofrep",
	"flag-sync":       relay + "service/flag-sync",
}

// Inventory lists the components of flagd-full by group, type and module; the command resolves the versions from the
// build information.
func Inventory() distribution.Inventory {
	official := Official()
	var list []distribution.ComponentInfo
	for _, f := range official.Sync {
		list = append(list, distribution.ComponentInfo{Group: distribution.GroupSync, Type: f.Type(), Module: syncModules[f.Type()]})
	}
	for _, f := range official.ConfigurationFormats {
		list = append(list, distribution.ComponentInfo{
			Group: distribution.GroupConfigurationFormat, Type: f.Type(), Module: relay + "configformat/flagd",
		})
	}
	for _, f := range official.Evaluators {
		list = append(list, distribution.ComponentInfo{Group: distribution.GroupEvaluator, Type: f.Type(), Module: relay + "evaluator/flagd"})
	}
	for _, f := range official.Endpoints {
		list = append(list, distribution.ComponentInfo{Group: distribution.GroupServiceEndpoint, Type: f.Type(), Module: endpointModules[f.Type()]})
	}
	list = append(list, distribution.ComponentInfo{Group: distribution.GroupTelemetry, Type: "otel", Module: relay + "telemetry/otel"})
	return distribution.Inventory{Components: list}
}
