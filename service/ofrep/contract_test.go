package ofrep

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/service/servicetest"
	flagdService "github.com/open-feature-forking/openfeature-relay/service/transport"
	"github.com/stretchr/testify/require"
)

// TestOFREPContract runs the service endpoint contract on the OFREP endpoint. It names the features it does not
// have through the contract's inputs: it serves no documents, collects no events, gives clients no validator
// outside a live event stream. It honours a selector bound by middleware.
func TestOFREPContract(t *testing.T) {
	sse := func(ctx context.Context, addr, selector string) (*http.Response, error) {
		path := "/ofrep/v1/sse"
		if selector != "" {
			path += "/" + url.PathEscape(selector)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+path, nil)
		if err != nil {
			return nil, err
		}
		return http.DefaultClient.Do(req)
	}

	servicetest.Run(t, Factory{}, servicetest.Inputs{
		PortKey: "port",
		HTTP:    true,
		Config: map[string]any{ConfigKey: EndpointConfig{
			Service: SvcConfiguration{ServiceName: "flagd", SSEEnabled: true, SSEInactivityDelaySec: 60},
			Formats: []string{"flagd", servicetest.VendorFormat},
		}},
		Without: []servicetest.Feature{
			servicetest.FeatureDocuments, servicetest.FeatureValidator, servicetest.FeatureSendEvents,
		},
		Connect: func(_ context.Context, addr string) (<-chan struct{}, error) {
			resp, err := sse(context.Background(), addr, "")
			if err != nil {
				return nil, err
			}
			ended := make(chan struct{})
			go func() {
				defer close(ended)
				defer resp.Body.Close()
				buf := make([]byte, 512)
				for {
					if _, err := resp.Body.Read(buf); err != nil {
						return
					}
				}
			}()
			return ended, nil
		},
		Resolve: func(ctx context.Context, addr, key, selector string) (servicetest.Answer, error) {
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr+"/ofrep/v1/evaluate/flags/"+key, strings.NewReader("{}"))
			if err != nil {
				return servicetest.Answer{}, err
			}
			if selector != "" {
				req.Header.Set(flagdService.FLAGD_SELECTOR_HEADER, selector)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return servicetest.Answer{}, err
			}
			defer resp.Body.Close()
			var body struct {
				ErrorCode string `json:"errorCode"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&body)
			return servicetest.Answer{ErrorCode: body.ErrorCode}, nil
		},
		Listen: func(ctx context.Context, addr, selector string) (<-chan struct{}, error) {
			resp, err := sse(context.Background(), addr, selector)
			if err != nil {
				return nil, err
			}
			require.Equal(t, http.StatusOK, resp.StatusCode)
			announced := make(chan struct{}, 64)
			go func() {
				defer resp.Body.Close()
				scanner := bufio.NewScanner(resp.Body)
				for scanner.Scan() {
					if strings.Contains(scanner.Text(), "refetchEvaluation") {
						select {
						case announced <- struct{}{}:
						default:
						}
					}
				}
			}()
			// the stream registers once its first bytes are written
			time.Sleep(50 * time.Millisecond)
			return announced, nil
		},
	})
}
