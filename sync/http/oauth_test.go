package http

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

func TestFetcher_getClient(t *testing.T) {
	oauth := &sync.OAuthCredentialHandler{
		ClientID:     "myClientID",
		ClientSecret: "myClientSecret",
		TokenURL:     "http://localhost",
	}
	oauthDelay := &sync.OAuthCredentialHandler{
		ClientID:     "myClientID",
		ClientSecret: "myClientSecret",
		TokenURL:     "http://localhost",
		ReloadDelayS: 10000,
	}
	client := &http.Client{
		Timeout: time.Second * 10,
	}
	oauthClientCredential := &clientcredentials.Config{
		ClientID:     oauth.ClientID,
		ClientSecret: oauth.ClientSecret,
		TokenURL:     oauth.TokenURL,
		AuthStyle:    oauth2.AuthStyleAutoDetect,
	}
	tests := map[string]struct {
		config sync.SourceConfig
		client *http.Client
	}{
		"no http client no oauth": {
			config: sync.SourceConfig{},
		},
		"no http client yes oauth": {
			config: sync.SourceConfig{
				OAuth: oauth,
			},
		},
		"no http client yes oauth reload": {
			config: sync.SourceConfig{
				OAuth: oauthDelay,
			},
		},
		"yes http client no oauth": {
			config: sync.SourceConfig{},
			client: client,
		},
		"yes http client yes oauth": {
			config: sync.SourceConfig{
				OAuth: oauth,
			},
			client: client,
		},
		"yes http client yes oauth reload": {
			config: sync.SourceConfig{
				OAuth: oauthDelay,
			},
			client: oauthClientCredential.Client(context.Background()),
		},
	}

	l := logger.NewLogger(nil, false)
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			httpSync := NewFetcher(tt.config, l)
			if tt.client != nil {
				// we have a cached HTTP client already
				httpSync.client = tt.client
				httpClient, ok := httpSync.getClient().(*http.Client)
				require.True(t, ok, "expected http client")
				if tt.config.OAuth != nil {
					// we use oauth so client should be different
					require.IsType(t, &oauth2.Transport{}, httpClient.Transport)
				} else {
					// we don't use oauth so client should be the same
					require.Equal(t, tt.client, httpClient)
				}

			}
			require.NotNil(t, httpSync.getClient())
		})
	}
}

type oauthHttpMock struct {
	count      int
	lastParams url.Values
	lastHeader http.Header
}

func TestFetcher_OAuth(t *testing.T) {
	// given
	const clientID = "clientID"
	const clientSecret = "clientSecret"
	const oauthPath = "/oauth"
	const bearerToken = "mySecretBearerToken"

	tests := map[string]struct {
		oauthResponse          int
		expectedHttpCallCount  int
		expectedOauthCallCount int
		expectedBeaerToken     string
	}{
		"success": {
			oauthResponse:          http.StatusOK,
			expectedHttpCallCount:  2,
			expectedOauthCallCount: 1,
			expectedBeaerToken:     fmt.Sprintf("Bearer %s", bearerToken),
		},
		"oauth error": {
			oauthResponse:          http.StatusInternalServerError,
			expectedHttpCallCount:  0,
			expectedOauthCallCount: 2,
			expectedBeaerToken:     "",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			oauthMock := &oauthHttpMock{}
			httpMock := &oauthHttpMock{}
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, oauthPath) {
					body, _ := io.ReadAll(r.Body)
					params, _ := url.ParseQuery(string(body))
					oauthMock.lastParams = params
					oauthMock.lastHeader = r.Header
					oauthMock.count++
					w.WriteHeader(tt.oauthResponse)
					w.Header().Set("Content-Type", "application/x-www-form-urlencoded")
					_, err := w.Write([]byte(fmt.Sprintf("access_token=%s&scope=mockscope&token_type=bearer", bearerToken)))
					if err != nil {
						t.Fatalf("cannot write response: %v", err)
					}
					return
				}
				httpMock.lastHeader = r.Header
				httpMock.count++
				w.WriteHeader(http.StatusInternalServerError)
			}))
			defer ts.Close()
			l := logger.NewLogger(nil, false)
			s := NewFetcher(sync.SourceConfig{
				URI:        ts.URL,
				AuthHeader: "Bearer it_should_be_replaced_by_oauth",
				OAuth: &sync.OAuthCredentialHandler{
					ClientID:     clientID,
					ClientSecret: clientSecret,
					TokenURL:     ts.URL + oauthPath,
					ReloadDelayS: 10000,
				},
			}, l)
			// when we call resync multiple times
			_, err := s.Fetch(context.Background(), "")
			_, err2 := s.Fetch(context.Background(), "")
			// then

			// we only get errors when fetching the HTTP data
			require.ErrorContains(t, err, "500 Internal Server Error")
			require.ErrorContains(t, err2, "500 Internal Server Error")

			// the OAuth endpoint is the right amount of times
			require.Equal(t, tt.expectedOauthCallCount, oauthMock.count)

			// the Beaerer token is replaced by the OAuth values
			require.Equal(t, tt.expectedBeaerToken, httpMock.lastHeader.Get("Authorization"))
			require.Equal(t, tt.expectedHttpCallCount, httpMock.count)
		})
	}
}

func TestFetcher_OAuthFolderSecrets(t *testing.T) {
	// given
	const (
		clientID               = "clientID"
		clientSecret           = "clientSecret"
		oauthPath              = "/oauth"
		bearerToken            = "mySecretBearerToken"
		folderName             = "flagd_oauth_test"
		secretClientID         = "mySecretClientID"
		secretClientSecret     = "mySecretClientSecret"
		secretClientID_new     = "newClientID"
		secretClientSecret_new = "newClientSecret"
	)
	// given an oauth server
	oauthMock := &oauthHttpMock{}
	flagsPath := "/flags.json"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, oauthPath) {
			// mock token response
			body, _ := io.ReadAll(r.Body)
			params, _ := url.ParseQuery(string(body))
			oauthMock.lastParams = params
			oauthMock.lastHeader = r.Header
			oauthMock.count++
			w.WriteHeader(http.StatusOK)
			w.Header().Set("Content-Type", "application/x-www-form-urlencoded")
			_, err := w.Write([]byte(fmt.Sprintf("access_token=%s&scope=mockscope&token_type=bearer", bearerToken)))
			if err != nil {
				t.Fatalf("cannot write response: %v", err)
			}
			return
		} else if strings.HasSuffix(r.URL.Path, flagsPath) {
			// mock flags response
			_, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("cannot read request: %v", err)
			}

			w.WriteHeader(http.StatusOK)
			w.Header().Set("Content-Type", "application/json")
			_, err = w.Write([]byte(fmt.Sprintf(`{"flagKey": {"default": true}}`)))
			if err != nil {
				t.Fatalf("cannot write response: %v", err)
			}
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()
	// given credentials stored on a folder
	dir, err := os.MkdirTemp("", folderName)
	defer func(path string) {
		e := os.RemoveAll(path)
		if e != nil {
			fmt.Printf("Cannot delete %s: %v", path, e)
		}
	}(dir)
	require.NoError(t, err)

	err = os.WriteFile(dir+"/client-id", []byte(secretClientID), 0644)
	require.NoError(t, err)
	err = os.WriteFile(dir+"/client-secret", []byte(secretClientSecret), 0644)
	require.NoError(t, err)

	l := logger.NewLogger(nil, false)
	s := NewFetcher(sync.SourceConfig{
		URI:        ts.URL + flagsPath,
		AuthHeader: "Bearer it_should_be_replaced_by_oauth",
		OAuth: &sync.OAuthCredentialHandler{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			Folder:       dir,
			TokenURL:     ts.URL + oauthPath,
			ReloadDelayS: 0, // we force loading the secret at each req
		},
	}, l)
	// when we fire the HTTP call
	_, err = s.Fetch(context.Background(), "")
	if err != nil {
		t.Fatalf("resync failed: %v", err)
	}

	// then the right secrets are used
	require.Equal(t, secretClientID, oauthMock.lastParams.Get("client_id"))
	require.Equal(t, secretClientSecret, oauthMock.lastParams.Get("client_secret"))

	// when we change the secrets
	err = os.WriteFile(dir+"/client-id", []byte(secretClientID_new), 0644)
	require.NoError(t, err)
	err = os.WriteFile(dir+"/client-secret", []byte(secretClientSecret_new), 0644)
	require.NoError(t, err)

	// then the new HTTP call will use the new value
	_, err = s.Fetch(context.Background(), "")
	if err != nil {
		t.Fatalf("resync failed: %v", err)
	}
	require.Equal(t, secretClientID_new, oauthMock.lastParams.Get("client_id"))
	require.Equal(t, secretClientSecret_new, oauthMock.lastParams.Get("client_secret"))
}
