package publish

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/open-feature-forking/openfeature-relay/tools/ci/internal/ocitest"
)

// Rows for what the Go port refuses and the Python did not: credentials leaving the registry's origin, digests that are
// not digests, an image under the wrong sha tag. Each says so in its name or comment ("stricter than the Python"); the
// parity tests do not cover them.

func creds(t *testing.T) {
	t.Setenv("REGISTRY_USER", "bot")
	t.Setenv("REGISTRY_TOKEN", "secret")
}

// spy is a server that records every request it gets.
type spy struct {
	*httptest.Server
	mu   sync.Mutex
	seen []string
}

func newSpy(t *testing.T, listen string) *spy {
	t.Helper()
	s := &spy{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.seen = append(s.seen, r.Method+" "+r.URL.RequestURI()+" auth="+r.Header.Get("Authorization"))
		s.mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"token":"stolen"}`))
	})
	if listen == "" {
		s.Server = httptest.NewServer(handler)
	} else {
		l, err := net.Listen("tcp", listen)
		if err != nil {
			t.Skipf("%s is not available: %v", listen, err)
		}
		s.Server = httptest.NewUnstartedServer(handler)
		s.Server.Listener = l
		s.Server.Start()
	}
	t.Cleanup(s.Server.Close)
	return s
}

func (s *spy) requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.seen...)
}

func TestUploadLocationStaysOnTheRegistry(t *testing.T) {
	t.Run("stricter than the Python: an absolute Location on another server gets no request and no token", func(t *testing.T) {
		e := newEnv(t)
		creds(t)
		other := newSpy(t, "")
		e.reg.Set(func(r *ocitest.Registry) {
			r.Token, r.User, r.Password = "tok", "bot", "secret"
			r.Location = func(string) string { return other.URL + "/v2/open-feature-forking/flagd-sync/blobs/uploads/1" }
		})
		e.layouts(short, fullSha("a"), "flagd-sync")
		err := e.push(func(o *PushOptions) { o.Names = []string{"flagd-sync"} })
		if err == nil || !strings.Contains(err.Error(), "refusing the upload Location") {
			t.Fatalf("error = %v", err)
		}
		if got := other.requests(); len(got) != 0 {
			t.Errorf("the other server got %v", got)
		}
	})

	c := NewClient("127.0.0.1:5000", "", "")
	tests := []struct {
		location string
		want     string // "" means refused
	}{
		{"/v2/x/blobs/uploads/1", "http://127.0.0.1:5000/v2/x/blobs/uploads/1"},
		{"/v2/x/blobs/uploads/1?_state=abc", "http://127.0.0.1:5000/v2/x/blobs/uploads/1?_state=abc"},
		{"http://127.0.0.1:5000/v2/x/blobs/uploads/1", "http://127.0.0.1:5000/v2/x/blobs/uploads/1"},
		{"http://127.0.0.1:5001/v2/x", ""},
		{"https://127.0.0.1:5000/v2/x", ""},
		{"http://127.0.0.2:5000/v2/x", ""},
		{"http://127.0.0.1:5000@evil.example.com/v2/x", ""},
		{"http://user:pw@127.0.0.1:5000/v2/x", ""},
		{"//evil.example.com/v2/x", ""},
		{"https://evil.example.com/v2/x", ""},
		{"v2/x/blobs/uploads/1", ""},
		{"ftp://127.0.0.1:5000/x", ""},
		{"%zz", ""},
	}
	for _, tt := range tests {
		t.Run("stricter than the Python: "+tt.location, func(t *testing.T) {
			got, err := c.resolveLocation(tt.location)
			if (err == nil) != (tt.want != "") || got != tt.want {
				t.Errorf("resolveLocation(%q) = %q, %v; want %q", tt.location, got, err, tt.want)
			}
		})
	}
}

func TestTokenRealmIsWhereCredentialsMayGo(t *testing.T) {
	ghcr := NewClient("ghcr.io", "bot", "secret")
	local := NewClient("127.0.0.1:5000", "bot", "secret")
	tests := []struct {
		name   string
		client *Client
		realm  string
		ok     bool
	}{
		{"ghcr: exactly https://ghcr.io", ghcr, "https://ghcr.io/token", true},
		{"ghcr: a longer path", ghcr, "https://ghcr.io/token/x?y=1", true},
		{"ghcr: plain http", ghcr, "http://ghcr.io/token", false},
		{"ghcr: a port", ghcr, "https://ghcr.io:8443/token", false},
		{"ghcr: a suffix host", ghcr, "https://ghcr.io.evil.example.com/token", false},
		{"ghcr: user information", ghcr, "https://ghcr.io@evil.example.com/token", false},
		{"ghcr: another host", ghcr, "https://auth.example.com/token", false},
		{"ghcr: a loopback host", ghcr, "http://127.0.0.1:5000/token", false},
		{"ghcr: no host", ghcr, "/token", false},
		{"ghcr: empty", ghcr, "", false},
		{"local: 127.0.0.1 with a port", local, "http://127.0.0.1:5000/token", true},
		{"local: another loopback port", local, "http://127.0.0.1:6000/token", true},
		{"local: localhost", local, "http://localhost:5000/token", true},
		{"local: ghcr.io", local, "https://ghcr.io/token", false},
		{"local: another host", local, "https://evil.example.com/token", false},
		{"local: user information", local, "http://127.0.0.1@evil.example.com/token", false},
		{"local: a suffix host", local, "http://localhost.evil.example.com/token", false},
		{"local: another loopback address", local, "http://127.0.0.2:5000/token", false},
		{"local: another scheme", local, "ftp://localhost/token", false},
	}
	for _, tt := range tests {
		t.Run("stricter than the Python: "+tt.name, func(t *testing.T) {
			if err := tt.client.checkRealm(tt.realm); (err == nil) != tt.ok {
				t.Errorf("checkRealm(%q) = %v, want ok=%v", tt.realm, err, tt.ok)
			}
		})
	}

	t.Run("stricter than the Python: a challenge naming another realm sends no login", func(t *testing.T) {
		other := newSpy(t, "127.0.0.2:0")
		err := local.authenticate(`Bearer realm="`+other.URL+`/token",service="x"`, "scope")
		if err == nil || !strings.Contains(err.Error(), "refusing the token realm") {
			t.Fatalf("error = %v", err)
		}
		err = ghcr.authenticate(`Bearer realm="`+other.URL+`/token",service="x"`, "scope")
		if err == nil || !strings.Contains(err.Error(), "refusing the token realm") {
			t.Fatalf("ghcr: error = %v", err)
		}
		if got := other.requests(); len(got) != 0 {
			t.Errorf("the other server got %v", got)
		}
	})
	t.Run("stricter than the Python: a push whose registry names another realm fails and sends no login", func(t *testing.T) {
		e := newEnv(t)
		creds(t)
		other := newSpy(t, "127.0.0.2:0")
		e.reg.Set(func(r *ocitest.Registry) {
			r.Token, r.User, r.Password = "tok", "bot", "secret"
			r.Realm = other.URL + "/token"
		})
		e.layouts(short, fullSha("a"), "flagd-sync")
		err := e.push(func(o *PushOptions) { o.Names = []string{"flagd-sync"} })
		if err == nil || !strings.Contains(err.Error(), "refusing the token realm") {
			t.Fatalf("error = %v", err)
		}
		if got := other.requests(); len(got) != 0 {
			t.Errorf("the other server got %v", got)
		}
	})
}

func TestRedirectsStayOnTheRegistry(t *testing.T) {
	other := newSpy(t, "")
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/elsewhere", http.StatusFound)
	}))
	t.Cleanup(registry.Close)
	c := NewClient(strings.TrimPrefix(registry.URL, "http://"), "bot", "secret")
	if _, err := c.GetManifest("open-feature-forking/flagd-sync", "main"); err == nil || !strings.Contains(err.Error(), "refusing a redirect") {
		t.Fatalf("error = %v (stricter than the Python, which followed any redirect)", err)
	}
	if got := other.requests(); len(got) != 0 {
		t.Errorf("the other server got %v", got)
	}
}

func TestBasicLoginGoesOnlyToALocalRegistry(t *testing.T) {
	var mu sync.Mutex
	var auth []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auth = append(auth, r.Header.Get("Authorization"))
		mu.Unlock()
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	c := NewClient("ghcr.io", "bot", "secret") // not local: https
	c.base, c.client = srv.URL, srv.Client()
	c.client.CheckRedirect = c.sameOriginRedirect
	if m, err := c.GetManifest("open-feature-forking/flagd-sync", "main"); err != nil || m != nil {
		t.Fatalf("GetManifest = %v, %v", m, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(auth) != 1 || auth[0] != "" {
		t.Errorf("a non-local registry got Authorization %q on its first request", auth)
	}
}

// A layout whose descriptors carry anything but a sha256 digest is refused before the first request: digests go into URLs.
func TestDigestsOfALayoutAreValidated(t *testing.T) {
	good := "sha256:" + strings.Repeat("a", 64)
	tests := []struct{ name, digest string }{
		{"a path", "sha256:../../x"},
		{"a query", good + "?mount=other"},
		{"upper case", "sha256:" + strings.Repeat("A", 64)},
		{"too short", "sha256:" + strings.Repeat("a", 63)},
		{"too long", good + "a"},
		{"another algorithm", "sha512:" + strings.Repeat("a", 64)},
		{"no algorithm", strings.Repeat("a", 64)},
		{"empty", ""},
		{"a newline", good + "\n"},
	}
	for _, tt := range tests {
		t.Run("stricter than the Python: "+tt.name, func(t *testing.T) {
			e := newEnv(t)
			e.layouts(short, fullSha("a"))
			ocitest.WriteLayout(t, e.dir+"/flagd-sync.tar", ocitest.LayoutSpec{
				Refs: ocitest.RefsFor("flagd-sync", short), Revision: fullSha("a"),
				MutateIndex: func(index map[string]any) {
					index["manifests"].([]map[string]any)[0]["digest"] = tt.digest
				}})
			wantRefused(t, e.push())
			if n := len(e.reg.Requests()); n != 0 {
				t.Errorf("the registry saw %d requests", n)
			}
		})
	}
	t.Run("stricter than the Python: the client refuses a blob digest that is not one", func(t *testing.T) {
		e := newEnv(t)
		client, _ := RegistryFromEnv()
		if err := client.PushBlob("open-feature-forking/flagd-sync", "sha256:../x", nil, 0); err == nil {
			t.Error("a bad digest was pushed")
		}
		if n := len(e.reg.Requests()); n != 0 {
			t.Errorf("the registry saw %d requests", n)
		}
	})
}

// Each of the digest checks of pushLayout is pinned by a row that faults only that digest: dropping one fails its row.
// The attested digest is the index's, so the first two are the ones the attestation step depends on.
func TestEachIndexDigestIsCompared(t *testing.T) {
	zero := "sha256:" + strings.Repeat("0", 64)
	tests := []struct {
		name    string
		fault   func(index string) func(reference, actual string) string
		wantErr string
	}{
		{"the index pushed by digest", func(index string) func(string, string) string {
			return func(ref, actual string) string {
				if ref == index {
					return zero
				}
				return actual
			}
		}, "pushed sha256:"},
		{"the index pushed under the sha tag", func(string) func(string, string) string {
			return func(ref, actual string) string {
				if ref == "sha-"+short {
					return zero
				}
				return actual
			}
		}, "the tag sha-" + short + " holds"},
		{"a child manifest", func(index string) func(string, string) string {
			faulted := false
			return func(ref, actual string) string {
				if strings.HasPrefix(ref, "sha256:") && ref != index && !faulted {
					faulted = true
					return zero
				}
				return actual
			}
		}, "the registry stored"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			index := e.layouts(short, fullSha("a"), "flagd-sync")["flagd-sync"]
			e.reg.Set(func(r *ocitest.Registry) { r.ManifestDigest = tt.fault(index) })
			err := e.push(func(o *PushOptions) { o.Names = []string{"flagd-sync"} })
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want one containing %q", err, tt.wantErr)
			}
			if tt.name == "the index pushed by digest" && !strings.Contains(err.Error(), "pushed "+index) {
				t.Errorf("the error does not name the index digest %s: %v", index, err)
			}
		})
	}
}

func TestMoveMainChecksTheRevisionOfTheImageItMoves(t *testing.T) {
	// stricter than the Python, which moved whatever sat under sha-<short>
	tests := []struct{ name, revision string }{
		{"another commit", fullSha("c")},
		{"no revision annotation", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			r := newRepo(t)
			sha := r.commit("old")
			e.publishCommit(sha, tt.revision)
			err := e.move(r, sha, false)
			if err == nil || !strings.Contains(err.Error(), "was built from") {
				t.Fatalf("error = %v", err)
			}
			if got := e.reg.Tag(syncRepo, "main"); got != "" {
				t.Errorf("main was moved to %s", got)
			}
		})
	}
}

func TestRevisionsThatLookLikeOptionsNeverReachGit(t *testing.T) {
	var calls [][]string
	o := MoveOptions{Sha: fullSha("b"), gitRun: func(args ...string) bool { calls = append(calls, args); return true }}
	for _, revision := range []string{"-x", "--upload-pack=touch /tmp/x", "--help", "-"} {
		if o.hasCommit(revision) || o.isAncestor(revision) {
			t.Errorf("%q was treated as a commit", revision)
		}
	}
	if len(calls) != 0 {
		t.Errorf("git was run with %v", calls)
	}
	if !o.hasCommit(fullSha("a")) || !o.isAncestor(fullSha("a")) || len(calls) != 2 {
		t.Errorf("an ordinary revision must reach git: %v", calls)
	}
	if got := strings.Join(calls[1], " "); got != "merge-base --is-ancestor "+fullSha("a")+" "+fullSha("b") {
		t.Errorf("git ran %q", got)
	}
}

func TestMoveMainKeepsMainForAnOptionLikeRevisionWithoutRunningGit(t *testing.T) {
	e := newEnv(t)
	r, next := e.history("--upload-pack=x")
	var calls int
	err := MoveMain(MoveOptions{Short: next[:7], Sha: next, Names: []string{"flagd-sync"}, GitDir: r.dir,
		gitRun: func(...string) bool { calls++; return true }}, func(s string) { e.log = append(e.log, s) })
	if err != nil {
		t.Fatal(err)
	}
	// without --force :main stays; the guard means git is not asked about the revision at all
	if calls != 0 || !e.kept() {
		t.Errorf("git ran %d times, log %v", calls, e.log)
	}
}
