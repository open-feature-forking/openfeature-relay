package ofoperator

import (
	"context"
	stdsync "sync"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/scheme"
	clienttesting "k8s.io/client-go/testing"
)

const (
	opName = "myFF"
	opNS   = "myNS"
	opURI  = opNS + "/" + opName
)

func featureFlag(resourceVersion string, flagSpec map[string]interface{}) *unstructured.Unstructured {
	ff := &unstructured.Unstructured{}
	ff.SetUnstructuredContent(map[string]interface{}{
		"apiVersion": "core.openfeature.dev/v1beta1",
		"kind":       "FeatureFlag",
		"metadata": map[string]interface{}{
			"name":            opName,
			"namespace":       opNS,
			"resourceVersion": resourceVersion,
		},
		"spec": map[string]interface{}{"flagSpec": flagSpec},
	})
	return ff
}

func simpleFlags(variant string) map[string]interface{} {
	return map[string]interface{}{
		"flags": map[string]interface{}{
			"f": map[string]interface{}{
				"state":          "ENABLED",
				"variants":       map[string]interface{}{variant: true},
				"defaultVariant": variant,
			},
		},
	}
}

// receivedRecorder records the calls a provider makes to its sync.MetricsRecorder.
type receivedRecorder struct {
	mu    stdsync.Mutex
	calls [][3]string
}

func (r *receivedRecorder) RecordFlagConfigReceived(_ context.Context, sourceType, uri, selector string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, [3]string{sourceType, uri, selector})
}

func (r *receivedRecorder) snapshot() [][3]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][3]string(nil), r.calls...)
}

func startOperatorSync(t *testing.T, s *Sync) (chan sync.DataSync, chan error) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	deliveries := make(chan sync.DataSync, 8)
	stopped := make(chan error, 1)
	require.NoError(t, s.Init(ctx))
	go func() { stopped <- s.Sync(ctx, deliveries) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-stopped:
		case <-time.After(10 * time.Second):
			t.Error("the sync provider did not stop")
		}
	})
	return deliveries, stopped
}

func requireOperatorDelivery(t *testing.T, deliveries chan sync.DataSync, wantJSON string) {
	t.Helper()

	select {
	case got := <-deliveries:
		require.Equal(t, opURI, got.Source)
		require.False(t, got.IncrementalUpdates)
		require.JSONEq(t, wantJSON, got.FlagData)
	case <-time.After(10 * time.Second):
		t.Fatalf("no delivery of %s", wantJSON)
	}
}

// TestDeliveryCharacterization pins what the operator provider delivers and when it is ready, through
// NewK8sSync and the ISync methods, against a fake dynamic client.
func TestDeliveryCharacterization(t *testing.T) {
	newSync := func(fc *fake.FakeDynamicClient) *Sync {
		return NewK8sSync(logger.NewLogger(nil, false), opURI, fc)
	}

	t.Run("a failed initial fetch is repeated until the resource exists", func(t *testing.T) {
		defer func(previous time.Duration) { retryInterval = previous }(retryInterval)
		retryInterval = 10 * time.Millisecond

		fc := fake.NewSimpleDynamicClient(scheme.Scheme)
		s := newSync(fc)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		deliveries := make(chan sync.DataSync, 4)
		stopped := make(chan error, 1)
		require.NoError(t, s.Init(ctx))
		go func() { stopped <- s.Sync(ctx, deliveries) }()

		select {
		case err := <-stopped:
			t.Fatalf("the provider stopped with %v", err)
		case <-deliveries:
			t.Fatal("unexpected delivery")
		case <-time.After(300 * time.Millisecond):
		}
		require.False(t, s.IsReady())

		_, err := fc.Resource(featureFlagResource).Namespace(opNS).Create(ctx, featureFlag("1", simpleFlags("on")), metav1.CreateOptions{})
		require.NoError(t, err)
		requireOperatorDelivery(t, deliveries, `{"flags":{"f":{"state":"ENABLED","variants":{"on":true},"defaultVariant":"on"}}}`)

		cancel()
		require.NoError(t, <-stopped)
	})

	t.Run("the delivered JSON is the operator's typed flag specification", func(t *testing.T) {
		ff := featureFlag("1", map[string]interface{}{
			"$schema": "https://flagd.dev/schema/v0/flags.json",
			"flags": map[string]interface{}{
				"f": map[string]interface{}{
					"state":          "ENABLED",
					"variants":       map[string]interface{}{"on": true, "off": false},
					"defaultVariant": "on",
					"unknownKey":     "dropped",
				},
			},
		})
		s := newSync(fake.NewSimpleDynamicClient(scheme.Scheme, ff))
		deliveries, _ := startOperatorSync(t, s)

		// `$schema` and the flag-level key the operator's type does not define are not delivered
		requireOperatorDelivery(t, deliveries, `{"flags":{"f":{"state":"ENABLED","variants":{"on":true,"off":false},"defaultVariant":"on"}}}`)
	})

	t.Run("readiness, changes and deletion", func(t *testing.T) {
		fc := fake.NewSimpleDynamicClient(scheme.Scheme, featureFlag("1", simpleFlags("on")))
		watchers := make(chan *watch.FakeWatcher, 8)
		fc.PrependWatchReactor("featureflags", func(clienttesting.Action) (bool, watch.Interface, error) {
			w := watch.NewFakeWithChanSize(8, false)
			watchers <- w
			return true, w, nil
		})
		s := newSync(fc)
		rec := &receivedRecorder{}
		s.SyncMetricsRecorder = rec
		require.False(t, s.IsReady())
		deliveries, _ := startOperatorSync(t, s)

		// the initial fetch delivers, and the informer's create event for the existing resource delivers the
		// same flags once more
		initial := `{"flags":{"f":{"state":"ENABLED","variants":{"on":true},"defaultVariant":"on"}}}`
		requireOperatorDelivery(t, deliveries, initial)
		requireOperatorDelivery(t, deliveries, initial)
		var watcher *watch.FakeWatcher
		select {
		case watcher = <-watchers:
		case <-time.After(10 * time.Second):
			t.Fatal("the informer never started a watch")
		}
		require.Eventually(t, s.IsReady, 10*time.Second, 20*time.Millisecond)

		// a modification delivers
		watcher.Modify(featureFlag("2", simpleFlags("changed")))
		requireOperatorDelivery(t, deliveries, `{"flags":{"f":{"state":"ENABLED","variants":{"changed":true},"defaultVariant":"changed"}}}`)

		// a deletion delivers nothing: the last flags stay in force
		watcher.Delete(featureFlag("2", simpleFlags("changed")))
		select {
		case got := <-deliveries:
			t.Fatalf("unexpected delivery after the deletion: %s", got.FlagData)
		case <-time.After(time.Second):
		}

		// each of the three deliveries was recorded as received from a kubernetes source named by the
		// resource, without a selector
		require.Equal(t, [][3]string{
			{"kubernetes", opURI, ""}, {"kubernetes", opURI, ""}, {"kubernetes", opURI, ""},
		}, rec.snapshot())

		// the provider stays ready when the watch ends
		watcher.Stop()
		time.Sleep(time.Second)
		require.True(t, s.IsReady())
	})
}
