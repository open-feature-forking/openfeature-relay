package http

import (
	"context"
	"net/http"
	parseUrl "net/url"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

type oauthCredentialHandler struct {
	clientId           string
	clientSecret       string
	tokenUrl           string
	folderSource       string
	reloadDelaySeconds int

	lastUpdate time.Time
}

func (och *oauthCredentialHandler) loadOAuthConfiguration() (*clientcredentials.Config, error) {
	if och.folderSource == "" {
		oauth := &clientcredentials.Config{
			ClientID:     och.clientId,
			ClientSecret: och.clientSecret,
			TokenURL:     och.tokenUrl,
			AuthStyle:    oauth2.AuthStyleInParams,
		}
		return oauth, nil
	}
	// we load from files
	id, err := os.ReadFile(och.folderSource + "/client-id")
	if err != nil {
		return nil, err
	}

	secret, err := os.ReadFile(och.folderSource + "/client-secret")
	if err != nil {
		return nil, err
	}
	return &clientcredentials.Config{
		ClientID:     string(id),
		ClientSecret: string(secret),
		TokenURL:     och.tokenUrl,
		AuthStyle:    oauth2.AuthStyleInParams,
	}, nil
}

func (och *oauthCredentialHandler) getOAuthClient() (*http.Client, error) {
	oauth, err := och.loadOAuthConfiguration()
	if err != nil {
		return nil, err
	}
	return oauth.Client(context.Background()), nil
}

// Client defines the behaviour required of a http client
type Client interface {
	Do(req *http.Request) (*http.Response, error)
}

// getFileExtensions returns the file extension from the URL path
func getFileExtensions(url string) string {
	u, err := parseUrl.Parse(url)
	if err != nil {
		return ""
	}

	return filepath.Ext(u.Path)
}
