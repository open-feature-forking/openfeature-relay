package ofoperator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	msync "sync"
	"sync/atomic"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync/polling"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/cache"

	"github.com/open-feature/open-feature-operator/api/core/v1beta1"
)

var (
	resyncPeriod        = 1 * time.Minute
	apiVersion          = fmt.Sprintf("%s/%s", v1beta1.GroupVersion.Group, v1beta1.GroupVersion.Version)
	featureFlagResource = v1beta1.GroupVersion.WithResource("featureflags")
)

const invalidAPIVersionMsg = "invalid api version %s, expected %s"

// register into the global client-go scheme once at init; doing it in Sync.Init races the scheme's maps (#2023)
func init() {
	utilruntime.Must(v1beta1.AddToScheme(scheme.Scheme))
}

type SyncOption func(s *Sync)

type Sync struct {
	URI string

	ready         atomic.Bool
	namespace     string
	crdName       string
	logger        *logger.Logger
	dynamicClient dynamic.Interface
	informer      cache.SharedInformer

	// SyncMetricsRecorder is the source-agnostic client-side sync-metrics recorder. Nil is safe.
	SyncMetricsRecorder sync.MetricsRecorder
}

func NewK8sSync(
	logger *logger.Logger,
	uri string,
	dynamicClient dynamic.Interface,
) *Sync {
	return &Sync{
		logger:        logger,
		URI:           uri,
		dynamicClient: dynamicClient,
	}
}

func (k *Sync) ReSync(ctx context.Context, dataSync chan<- sync.DataSync) error {
	fetch, err := k.fetch(ctx)
	if err != nil {
		return fmt.Errorf("unable to fetch flag configuration: %w", err)
	}
	dataSync <- sync.DataSync{FlagData: fetch, Source: k.URI}
	sync.RecordFlagConfigReceived(ctx, k.SyncMetricsRecorder, sync.SourceKubernetes, k.URI, "")
	return nil
}

func (k *Sync) Init(_ context.Context) error {
	var err error

	k.namespace, k.crdName, err = parseURI(k.URI)
	if err != nil {
		return fmt.Errorf("unable to parse uri %s: %w", k.URI, err)
	}

	// The created informer will not do resyncs if the given defaultEventHandlerResyncPeriod is zero.
	// For more details on resync implications refer to tools/cache/shared_informer.go
	// Restrict the list/watch to the configured FeatureFlag so that every source only caches its own resource.
	crdName := k.crdName
	factory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(
		k.dynamicClient,
		resyncPeriod,
		k.namespace,
		func(options *metav1.ListOptions) {
			options.FieldSelector = fields.OneTermEqualSelector("metadata.name", crdName).String()
		},
	)

	k.informer = factory.ForResource(featureFlagResource).Informer()

	return nil
}

func (k *Sync) IsReady() bool {
	return k.ready.Load()
}

func (k *Sync) Sync(ctx context.Context, dataSync chan<- sync.DataSync) error {
	k.logger.Info(fmt.Sprintf("starting sync from %s", k.URI))

	// Initial fetch, repeated with a backoff until the resource can be read
	k.logger.Debug(fmt.Sprintf("initial fetch from %s", k.URI))
	fetch, ok := k.fetchWithRetry(ctx)
	if !ok {
		return nil
	}

	dataSync <- sync.DataSync{FlagData: fetch, Source: k.URI}
	sync.RecordFlagConfigReceived(ctx, k.SyncMetricsRecorder, sync.SourceKubernetes, k.URI, "")

	k.logger.Debug(fmt.Sprintf("watching %s for changes", k.URI))

	// Buffer ensures notifier can publish the initial Ready event even if the watcher
	// goroutine has not started reading yet.
	notifies := make(chan INotify, 1)

	var wg msync.WaitGroup

	// Start notifier watcher
	wg.Add(1)
	go func() {
		defer wg.Done()
		k.watcher(ctx, notifies, dataSync)
	}()

	// Start K8s resource notifier
	wg.Add(1)
	go func() {
		defer wg.Done()
		k.notify(ctx, notifies)
	}()

	wg.Wait()
	return nil
}

func (k *Sync) watcher(ctx context.Context, notifies chan INotify, dataSync chan<- sync.DataSync) {
	for {
		select {
		case <-ctx.Done():
			return
		case w := <-notifies:
			switch w.GetEvent().EventType {
			case DefaultEventTypeCreate:
				k.logger.Debug("new configuration created")
				msg, err := k.fetch(ctx)
				if err != nil {
					k.logger.Error(fmt.Sprintf("error fetching after create notification: %s", err.Error()))
					continue
				}

				dataSync <- sync.DataSync{FlagData: msg, Source: k.URI}
				sync.RecordFlagConfigReceived(ctx, k.SyncMetricsRecorder, sync.SourceKubernetes, k.URI, "")
			case DefaultEventTypeModify:
				k.logger.Debug("Configuration modified")
				msg, err := k.fetch(ctx)
				if err != nil {
					k.logger.Error(fmt.Sprintf("error fetching after update notification: %s", err.Error()))
					continue
				}

				dataSync <- sync.DataSync{FlagData: msg, Source: k.URI}
				sync.RecordFlagConfigReceived(ctx, k.SyncMetricsRecorder, sync.SourceKubernetes, k.URI, "")
			case DefaultEventTypeDelete:
				k.logger.Debug("configuration deleted")
			case DefaultEventTypeReady:
				k.logger.Debug("notifier ready")
				k.ready.Store(true)
			}
		}
	}
}

// fetch attempts to retrieve the latest feature flag configurations
func (k *Sync) fetch(ctx context.Context) (string, error) {
	// first check the store - avoid overloading API
	item, exist, err := k.informer.GetStore().GetByKey(k.URI)
	if err != nil {
		return "", fmt.Errorf("unable to get %s from store: %w", k.URI, err)
	}

	if exist {
		// the resource is checked to be a FeatureFlag, and delivered as it was written
		_, err := toFFCfg(item)
		if err != nil {
			return "", err
		}

		k.logger.Debug(fmt.Sprintf("resource %s served from the informer cache", k.URI))
		return marshalFlagSpec(item)
	}

	// fallback to API access - this is an informer cache miss. Could happen at the startup where cache is not filled

	ffObj, err := k.dynamicClient.
		Resource(featureFlagResource).
		Namespace(k.namespace).
		Get(ctx, k.crdName, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("unable to fetch FeatureFlag %s/%s: %w", k.namespace, k.crdName, err)
	}

	k.logger.Debug(fmt.Sprintf("resource %s served from API server", k.URI))

	_, err = toFFCfg(ffObj)
	if err != nil {
		return "", fmt.Errorf("unable to convert object %s/%s to FeatureFlag: %w", k.namespace, k.crdName, err)
	}
	return marshalFlagSpec(ffObj)
}

func (k *Sync) notify(ctx context.Context, c chan<- INotify) {
	// The handlers block on their send. Once ctx is done nobody reads c, and a handler stuck on it keeps the
	// informer from stopping, so events go through a channel that is drained until the informer has returned.
	events := make(chan INotify)
	forwarded := make(chan struct{})
	go func() {
		defer close(forwarded)
		for e := range events {
			select {
			case c <- e:
			case <-ctx.Done():
			}
		}
	}()
	defer func() {
		close(events)
		<-forwarded
	}()

	objectKey := types.NamespacedName{
		Name:      k.crdName,
		Namespace: k.namespace,
	}

	if _, err := k.informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			k.logger.Info(fmt.Sprintf("kube sync notifier event: add: %s %s", objectKey.Namespace, objectKey.Name))
			if err := commonHandler(obj, objectKey, DefaultEventTypeCreate, events); err != nil {
				k.logger.Warn(err.Error())
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			k.logger.Info(fmt.Sprintf("kube sync notifier event: update: %s %s", objectKey.Namespace, objectKey.Name))
			if err := updateFuncHandler(oldObj, newObj, objectKey, events); err != nil {
				k.logger.Warn(err.Error())
			}
		},
		DeleteFunc: func(obj interface{}) {
			k.logger.Info(fmt.Sprintf("kube sync notifier event: delete: %s %s", objectKey.Namespace, objectKey.Name))
			if err := commonHandler(obj, objectKey, DefaultEventTypeDelete, events); err != nil {
				k.logger.Warn(err.Error())
			}
		},
	}); err != nil {
		k.logger.Fatal(err.Error())
	}

	events <- &Notifier{
		Event: Event[DefaultEventType]{
			EventType: DefaultEventTypeReady,
		},
	}

	k.informer.Run(ctx.Done())
}

// commonHandler emits the desired event if and only if handler receive an object matching apiVersion and resource name
func commonHandler(obj interface{}, object types.NamespacedName, emitEvent DefaultEventType, c chan<- INotify) error {
	ffObj, err := toFFCfg(obj)
	if err != nil {
		return err
	}

	if ffObj.APIVersion != apiVersion {
		return fmt.Errorf(invalidAPIVersionMsg, ffObj.APIVersion, apiVersion)
	}

	if ffObj.Name == object.Name {
		c <- &Notifier{
			Event: Event[DefaultEventType]{
				EventType: emitEvent,
			},
		}
	}

	return nil
}

// updateFuncHandler handles updates. Event is emitted if and only if resource name, apiVersion of old & new are equal
func updateFuncHandler(oldObj interface{}, newObj interface{}, object types.NamespacedName, c chan<- INotify) error {
	ffOldObj, err := toFFCfg(oldObj)
	if err != nil {
		return err
	}

	if ffOldObj.APIVersion != apiVersion {
		return fmt.Errorf(invalidAPIVersionMsg, ffOldObj.APIVersion, apiVersion)
	}

	ffNewObj, err := toFFCfg(newObj)
	if err != nil {
		return err
	}

	if ffNewObj.APIVersion != apiVersion {
		return fmt.Errorf(invalidAPIVersionMsg, ffNewObj.APIVersion, apiVersion)
	}

	if object.Name == ffNewObj.Name && ffOldObj.ResourceVersion != ffNewObj.ResourceVersion {
		c <- &Notifier{
			Event: Event[DefaultEventType]{
				EventType: DefaultEventTypeModify,
			},
		}
	}
	return nil
}

func toFFCfg(object interface{}) (*v1beta1.FeatureFlag, error) {
	u, ok := object.(*unstructured.Unstructured)
	if !ok {
		if tombstone, tOk := object.(cache.DeletedFinalStateUnknown); tOk {
			u, ok = tombstone.Obj.(*unstructured.Unstructured)
			if !ok {
				return nil, fmt.Errorf("tombstone object is not of type *unstructured.Unstructured")
			}
		} else {
			return nil, fmt.Errorf("provided value is not of type *unstructured.Unstructured")
		}
	}

	ff := &v1beta1.FeatureFlag{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, ff); err != nil {
		return nil, fmt.Errorf("unable to convert unstructured to FeatureFlag: %w", err)
	}

	return ff, nil
}

// marshalFlagSpec returns the flag configuration of a FeatureFlag as written in the resource: its spec.flagSpec, not
// the operator's typed view of it, which has no place for fields it does not define. A resource without one holds no
// flags.
func marshalFlagSpec(resource any) (string, error) {
	if tombstone, ok := resource.(cache.DeletedFinalStateUnknown); ok {
		resource = tombstone.Obj
	}
	u, ok := resource.(*unstructured.Unstructured)
	if !ok {
		return "", fmt.Errorf("provided value is not of type *unstructured.Unstructured")
	}
	flagSpec, found, err := unstructured.NestedFieldNoCopy(u.Object, "spec", "flagSpec")
	if err != nil {
		return "", fmt.Errorf("unable to read spec.flagSpec: %w", err)
	}
	if !found || flagSpec == nil {
		return "{}", nil
	}
	b, err := json.Marshal(flagSpec)
	if err != nil {
		return "", fmt.Errorf("failed to marshal spec.flagSpec: %w", err)
	}
	return string(b), nil
}

// parseURI parse provided uri in the format of <namespace>/<crdName> to namespace, crdName. Results in an error
// for invalid format or failed parsing
func parseURI(uri string) (string, string, error) {
	s := strings.Split(uri, "/")
	if len(s) != 2 || len(s[0]) == 0 || len(s[1]) == 0 {
		return "", "", fmt.Errorf("invalid resource uri format, expected <namespace>/<crdName> but got: %s", uri)
	}
	return s[0], s[1], nil
}

// retryInterval is the base of the backoff between the attempts of the initial fetch.
var retryInterval = time.Duration(polling.DefaultInterval) * time.Second

// fetchWithRetry fetches the resource until it succeeds. It reports false when ctx is done first.
func (k *Sync) fetchWithRetry(ctx context.Context) (string, bool) {
	backoff := polling.Backoff{Interval: retryInterval}
	for failures := 1; ; failures++ {
		fetch, err := k.fetch(ctx)
		if err == nil {
			return fetch, true
		}
		wait := backoff.Retry(failures)
		k.logger.Error(fmt.Sprintf("error with the initial fetch, trying again in %s: %s", wait, err.Error()))
		select {
		case <-ctx.Done():
			return "", false
		case <-time.After(wait):
		}
	}
}
