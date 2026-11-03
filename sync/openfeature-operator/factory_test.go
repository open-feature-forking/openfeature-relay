package ofoperator

import (
	"errors"
	gosync "sync"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync/synctest"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/scheme"
	clienttesting "k8s.io/client-go/testing"
)

type fixedClient struct {
	client dynamic.Interface
	err    error
}

func (c fixedClient) GetK8sClient() (dynamic.Interface, error) { return c.client, c.err }

// watches hands out the watches the informer starts, and stops them.
type watches struct {
	mu      gosync.Mutex
	started []*watch.FakeWatcher
	cond    chan struct{}
}

func newWatches(fc *fake.FakeDynamicClient) *watches {
	w := &watches{cond: make(chan struct{}, 16)}
	fc.PrependWatchReactor("featureflags", func(clienttesting.Action) (bool, watch.Interface, error) {
		fw := watch.NewFakeWithChanSize(8, false)
		w.mu.Lock()
		w.started = append(w.started, fw)
		w.mu.Unlock()
		w.cond <- struct{}{}
		return true, fw, nil
	})
	return w
}

func (w *watches) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.started)
}

// latest waits until at least n watches have started, and returns the latest.
func (w *watches) latest(t testing.TB, n int) *watch.FakeWatcher {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for w.count() < n {
		select {
		case <-w.cond:
		case <-deadline:
			t.Fatalf("the informer started %d watches, not %d", w.count(), n)
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.started[len(w.started)-1]
}

func TestStreamingContract(t *testing.T) {
	fc := fake.NewSimpleDynamicClient(scheme.Scheme, featureFlag("1", simpleFlags("on")))
	watches := newWatches(fc)
	version := 1
	bump := func() string { version++; return string(rune('0' + version)) }

	synctest.RunStreaming(t, Factory{Client: fixedClient{client: fc}}, synctest.StreamingInputs{
		Source: sync.SourceConfig{URI: opURI, Provider: Provider},
		Expect: `{"flags":{"f":{"state":"ENABLED","variants":{"on":true},"defaultVariant":"on"}}}`,
		Change: func(t testing.TB) {
			watches.latest(t, 1).Modify(featureFlag(bump(), simpleFlags("changed")))
		},
		// the watch ends and the informer starts another; the resource then changes, which the provider delivers
		Break: func(t testing.TB) {
			watches.latest(t, 1).Stop()
		},
		Restore: func(t testing.TB) {
			watches.latest(t, 2).Modify(featureFlag(bump(), simpleFlags("changed")))
		},
		Remove: func(t testing.TB) {
			watches.latest(t, 2).Delete(featureFlag(bump(), simpleFlags("changed")))
		},
		// a deleted resource delivers nothing: the last accepted flags stay in force
		OnRemove: synctest.Keep,
		Timeout:  10 * time.Second,
	})
}

func TestFactory(t *testing.T) {
	f := Factory{}
	require.Equal(t, "kubernetes", f.Type())
	require.Equal(t, []string{"core.openfeature.dev/"}, f.Schemes())

	got, ok := f.ParseURI("core.openfeature.dev/ns/name")
	require.True(t, ok)
	require.Equal(t, sync.SourceConfig{URI: "ns/name", Provider: "kubernetes"}, got)
	_, ok = f.ParseURI("ns/name")
	require.False(t, ok)
}

func TestFactoryReportsClientErrors(t *testing.T) {
	_, err := Factory{Client: fixedClient{err: errors.New("not in a cluster")}}.CreateStreaming(sync.Settings{
		Source: sync.SourceConfig{URI: opURI}, Logger: logger.NewLogger(nil, false),
	})
	require.EqualError(t, err, "error creating kubernetes clients: not in a cluster")
}

func TestFactoryInjectsTheMetricsRecorder(t *testing.T) {
	recorder := &receivedRecorder{}
	provider, err := Factory{Client: fixedClient{client: fake.NewSimpleDynamicClient(scheme.Scheme)}}.CreateStreaming(
		sync.Settings{Source: sync.SourceConfig{URI: opURI}, Logger: logger.NewLogger(nil, false), Metrics: recorder})
	require.NoError(t, err)
	require.Same(t, recorder, provider.(*Sync).SyncMetricsRecorder)
}
