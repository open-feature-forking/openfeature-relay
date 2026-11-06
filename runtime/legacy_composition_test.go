//go:build unix

package runtime

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	evalv1 "buf.build/gen/go/open-feature/flagd/connectrpc/go/flagd/evaluation/v1/evaluationv1connect"
	schemav1 "buf.build/gen/go/open-feature/flagd/connectrpc/go/schema/v1/schemav1connect"
	evaluationpb "buf.build/gen/go/open-feature/flagd/protocolbuffers/go/flagd/evaluation/v1"
	schemapb "buf.build/gen/go/open-feature/flagd/protocolbuffers/go/schema/v1"
	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
)

// TestLegacyStreamsShareTheHostsEvents pins, on the composition flagd-full has (the official endpoints with the
// deprecated flag evaluation endpoint mounted on the flag evaluation endpoint), that a schema.v1 and an evaluation.v1
// event stream get what a stream of the host gets: provider_ready, configuration_change after a store update, and
// provider_shutdown when the runtime stops, within the endpoint's budget.
func TestLegacyStreamsShareTheHostsEvents(t *testing.T) {
	openers := map[string]func(base string) func() (string, error){
		"schema.v1": func(base string) func() (string, error) {
			stream, err := schemav1.NewServiceClient(http.DefaultClient, base).
				EventStream(context.Background(), connect.NewRequest(&schemapb.EventStreamRequest{}))
			require.NoError(t, err)
			return func() (string, error) {
				if !stream.Receive() {
					if err := stream.Err(); err != nil {
						return "", err
					}
					return "", io.EOF
				}
				return stream.Msg().Type, nil
			}
		},
		"evaluation.v1": func(base string) func() (string, error) {
			stream, err := evalv1.NewServiceClient(http.DefaultClient, base).
				EventStream(context.Background(), connect.NewRequest(&evaluationpb.EventStreamRequest{}))
			require.NoError(t, err)
			return func() (string, error) {
				if !stream.Receive() {
					if err := stream.Err(); err != nil {
						return "", err
					}
					return "", io.EOF
				}
				return stream.Msg().Type, nil
			}
		},
	}

	for name, open := range openers {
		t.Run(name, func(t *testing.T) {
			p := newPorts(t)
			cfg := characterizedConfig(t, p)
			file := strings.TrimPrefix(cfg.SyncProviders[0].URI, "file:")
			stop, done := startRuntime(t, cfg)

			next := open(fmt.Sprintf("http://127.0.0.1:%d", p.service))
			events := make(chan string, 64)
			go func() {
				for {
					e, err := next()
					if err != nil {
						events <- "<end>"
						return
					}
					events <- e
				}
			}()
			receive := func(until time.Duration) string {
				select {
				case e := <-events:
					return e
				case <-time.After(until):
					return "<timeout>"
				}
			}
			require.Equal(t, "provider_ready", receive(5*time.Second))

			// the file source announces a change once it has read the file; rewrite it until one arrives
			variants := []string{"off", "on"}
			var seen string
			for i := 0; seen != "configuration_change" && i < 100; i++ {
				changed := fmt.Sprintf(`{"flags":{"f1":{"state":"ENABLED","defaultVariant":%q,"variants":{"on":true,"off":false}}}}`, variants[i%2])
				require.NoError(t, os.WriteFile(file, []byte(changed), 0o600))
				if e := receive(200 * time.Millisecond); e != "<timeout>" {
					seen = e
				}
			}
			require.Equal(t, "configuration_change", seen)

			began := time.Now()
			stop()
			select {
			case err := <-done:
				require.NoError(t, err)
			case <-time.After(20 * time.Second):
				t.Fatal("start did not return")
			}
			require.Less(t, time.Since(began), 2*time.Second, "the runtime returns within the endpoint's budget")

			var rest []string
			for e := receive(3 * time.Second); e != "<end>" && e != "<timeout>"; e = receive(3 * time.Second) {
				rest = append(rest, e)
			}
			require.Contains(t, rest, "provider_shutdown")
		})
	}
}
