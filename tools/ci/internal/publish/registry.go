package publish

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var indexTypes = []string{
	"application/vnd.oci.image.index.v1+json",
	"application/vnd.docker.distribution.manifest.list.v2+json",
}

var accept = strings.Join(append(append([]string{}, indexTypes...),
	"application/vnd.oci.image.manifest.v1+json", "application/vnd.docker.distribution.manifest.v2+json"), ", ")

// statusError is a response the registry answered with a status outside 2xx.
type statusError struct {
	Code   int
	Method string
	URL    string
	Header http.Header
}

func (e *statusError) Error() string { return fmt.Sprintf("%s %s: HTTP %d", e.Method, e.URL, e.Code) }

// Client is the part of the distribution API that push and move-main use.
type Client struct {
	host   string
	local  bool
	base   string
	basic  string
	tokens map[string]string
	client *http.Client
}

// localHost reports whether a REGISTRY_HOST names a local registry: localhost or 127.0.0.1, with an optional numeric
// port and nothing else.
func localHost(host string) bool {
	name, port, hasPort := strings.Cut(host, ":")
	if name != "localhost" && name != "127.0.0.1" {
		return false
	}
	if !hasPort {
		return true
	}
	n, err := strconv.Atoi(port)
	return err == nil && n > 0 && n < 65536 && strconv.Itoa(n) == port
}

// NewClient returns a client for host: ghcr.io over https, a local registry over plain http. Basic credentials go to a
// local registry directly and to the token realm of any other.
func NewClient(host, user, token string) *Client {
	r := &Client{host: host, local: localHost(host), tokens: map[string]string{}, client: &http.Client{}}
	r.base = "https://" + host
	if r.local {
		r.base = "http://" + host
	}
	if token != "" {
		r.basic = base64.StdEncoding.EncodeToString([]byte(user + ":" + token))
	}
	return r
}

func (r *Client) open(method, target string, headers map[string]string, b func() io.Reader, scope string, length int64) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		var reader io.Reader
		if b != nil {
			reader = b()
		}
		req, err := http.NewRequest(method, target, reader)
		if err != nil {
			return nil, err
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		if token, ok := r.tokens[scope]; scope != "" && ok {
			req.Header.Set("Authorization", "Bearer "+token)
		} else if r.basic != "" && r.local {
			req.Header.Set("Authorization", "Basic "+r.basic)
		}
		if length >= 0 {
			req.ContentLength = length
			if length == 0 {
				req.Body = nil
			}
		}
		resp, err := r.client.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return resp, nil
		}
		_ = resp.Body.Close()
		_, haveToken := r.tokens[scope]
		challenge := resp.Header.Get("WWW-Authenticate")
		if resp.StatusCode == http.StatusUnauthorized && scope != "" && !haveToken && challenge != "" && attempt == 0 {
			if err := r.authenticate(challenge, scope); err != nil {
				return nil, err
			}
			continue
		}
		return nil, &statusError{Code: resp.StatusCode, Method: method, URL: target, Header: resp.Header}
	}
}

var challengeParam = regexp.MustCompile(`(\w+)="([^"]*)"`)

func (r *Client) authenticate(challenge, scope string) error {
	params := map[string]string{}
	for _, m := range challengeParam.FindAllStringSubmatch(challenge, -1) {
		params[m[1]] = m[2]
	}
	realm, ok := params["realm"]
	if !ok {
		return fmt.Errorf("the challenge %q names no realm", challenge)
	}
	target := realm + "?service=" + url.QueryEscape(params["service"]) + "&scope=" + url.QueryEscape(scope)
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	if r.basic != "" {
		req.Header.Set("Authorization", "Basic "+r.basic)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &statusError{Code: resp.StatusCode, Method: http.MethodGet, URL: target, Header: resp.Header}
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return fmt.Errorf("the token response: %w", err)
	}
	r.tokens[scope] = out.Token
	return nil
}

func scopeOf(repository string) string { return "repository:" + repository + ":push,pull" }

func isStatus(err error, code int) bool {
	var s *statusError
	return errors.As(err, &s) && s.Code == code
}

func (r *Client) blobExists(repository, digest string) (bool, error) {
	resp, err := r.open(http.MethodHead, r.base+"/v2/"+repository+"/blobs/"+digest, nil, nil, scopeOf(repository), -1)
	if err == nil {
		_ = resp.Body.Close()
		return true, nil
	}
	if isStatus(err, http.StatusNotFound) {
		return false, nil
	}
	return false, err
}

// PushBlob uploads a blob unless the registry has it. The bytes are the layout's own, with the size the manifest declares.
func (r *Client) PushBlob(repository, digest string, stream func() io.Reader, size int64) error {
	exists, err := r.blobExists(repository, digest)
	if err != nil || exists {
		return err
	}
	scope := scopeOf(repository)
	start, err := r.open(http.MethodPost, r.base+"/v2/"+repository+"/blobs/uploads/", nil, nil, scope, 0)
	if err != nil {
		return err
	}
	location := start.Header.Get("Location")
	_ = start.Body.Close()
	if location == "" {
		return fmt.Errorf("%s: the upload answered without a Location", repository)
	}
	if strings.HasPrefix(location, "/") {
		location = r.base + location
	}
	sep := "?"
	if strings.Contains(location, "?") {
		sep = "&"
	}
	location += sep + "digest=" + url.QueryEscape(digest)
	resp, err := r.open(http.MethodPut, location, map[string]string{"Content-Type": "application/octet-stream"}, stream, scope, size)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// PushManifest uploads a manifest under a reference and returns the digest the registry says it stored.
func (r *Client) PushManifest(repository, reference, mediaType string, content []byte) (string, error) {
	resp, err := r.open(http.MethodPut, r.base+"/v2/"+repository+"/manifests/"+reference,
		map[string]string{"Content-Type": mediaType}, func() io.Reader { return bytes.NewReader(content) }, scopeOf(repository), int64(len(content)))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	return resp.Header.Get("Docker-Content-Digest"), nil
}

// Manifest is a manifest as the registry serves it.
type Manifest struct {
	MediaType string
	Body      []byte
}

// GetManifest returns the manifest of a reference, or nil when the reference does not exist.
func (r *Client) GetManifest(repository, reference string) (*Manifest, error) {
	resp, err := r.open(http.MethodGet, r.base+"/v2/"+repository+"/manifests/"+reference,
		map[string]string{"Accept": accept}, nil, scopeOf(repository), -1)
	if err != nil {
		if isStatus(err, http.StatusNotFound) {
			return nil, nil
		}
		return nil, err
	}
	defer resp.Body.Close()
	content, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	media, _, _ := strings.Cut(resp.Header.Get("Content-Type"), ";")
	return &Manifest{MediaType: media, Body: content}, nil
}
