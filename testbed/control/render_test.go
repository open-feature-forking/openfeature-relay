package control

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func golden(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "golden", name+".json"))
	require.NoError(t, err)
	return data
}

// TestRenderMatchesLaunchpad pins Q25 and BR-009: the rendered file is byte-identical to what the launchpad of
// flagd-testbed v3.11.0 writes for the same flag files, at start and after one and two /change calls. The goldens were
// recorded by running the launchpad's own CombineJSONFiles and ToggleChangingFlag on testdata/rawflags, which holds the
// flag files of the testbed.
func TestRenderMatchesLaunchpad(t *testing.T) {
	ts := newTestServer(t, &fakeTarget{}, nil)

	require.Equal(t, golden(t, "start"), ts.rendered(t), "at start")

	code, _ := ts.get(t, "/change")
	require.Equal(t, 200, code)
	require.Equal(t, golden(t, "change1"), ts.rendered(t), "after one change")

	code, _ = ts.get(t, "/change")
	require.Equal(t, 200, code)
	require.Equal(t, golden(t, "change2"), ts.rendered(t), "after two changes")

	require.NotEqual(t, golden(t, "start"), golden(t, "change1"), "the goldens differ in the toggled flag")
	require.Equal(t, golden(t, "start"), golden(t, "change2"), "two toggles are back where they began")
}

// The bytes the target is handed are the bytes of the file, and the version is their digest.
func TestDocumentIsWhatIsRendered(t *testing.T) {
	ts := newTestServer(t, &fakeTarget{}, nil)

	document, version := ts.Document()

	require.Equal(t, ts.rendered(t), document)
	_, want, err := encode(ts.combined)
	require.NoError(t, err)
	require.Equal(t, want, version)
}

// Q21, Q25: the file is rendered at start, after /change and after every start; it is removed during /restart; and it
// is never written before the target holds the document.
func TestRenderLifecycle(t *testing.T) {
	loadStarted := make(chan struct{})
	releaseLoad := make(chan struct{})
	target := &fakeTarget{}
	ts := newTestServer(t, target, nil)
	atStart := ts.rendered(t)
	require.Equal(t, golden(t, "start"), atStart, "rendered at start")

	// /change: not before Load has returned
	target.setLoad(func(_ context.Context, _ []byte, _ string) error {
		close(loadStarted)
		<-releaseLoad
		return nil
	})
	changed := make(chan int, 1)
	go func() {
		code, _ := ts.get(t, "/change")
		changed <- code
	}()
	<-loadStarted
	require.Equal(t, atStart, ts.rendered(t), "the old file stays while the target takes the document in")
	close(releaseLoad)
	require.Equal(t, 200, <-changed)
	require.Equal(t, golden(t, "change1"), ts.rendered(t), "rendered after the change")
	target.setLoad(nil)

	// /restart: removed while the target is closed, back after the delayed start
	code, _ := ts.get(t, "/restart?seconds=1")
	require.Equal(t, 200, code)
	_, err := os.Stat(ts.render)
	require.True(t, os.IsNotExist(err), "removed during /restart")
	require.Eventually(t, func() bool {
		data, err := os.ReadFile(ts.render)
		return err == nil && string(data) == string(golden(t, "change1"))
	}, 5*time.Second, 10*time.Millisecond, "rendered again after the start, from the state that survived: %v", target.log())

	// a start renders it again, even if it was removed
	require.NoError(t, os.Remove(ts.render))
	code, _ = ts.get(t, "/start")
	require.Equal(t, 200, code)
	require.Equal(t, golden(t, "change1"), ts.rendered(t), "rendered after a start")

	// /stop does not remove it, as the launchpad does not
	ts.get(t, "/stop")
	require.Equal(t, golden(t, "change1"), ts.rendered(t))
}

func TestNoRenderTurnsTheFileOff(t *testing.T) {
	dir := t.TempDir()
	ts := newTestServer(t, &fakeTarget{}, func(c *Config) {
		c.NoRender = true
		c.Render = filepath.Join(dir, "never.json")
	})

	ts.get(t, "/change")
	ts.get(t, "/start")

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, entries)
}

// A render failure does not fail a start: the target is up.
func TestRenderFailureDoesNotFailTheStart(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(blocker, nil, 0o644))
	s := New(Config{NoConfigs: true, RawFlags: filepath.Join("testdata", "rawflags"), Render: filepath.Join(blocker, "sub", "allFlags.json")}, &fakeTarget{})
	require.NoError(t, s.load())

	s.mu.Lock()
	err := s.start(context.Background(), "default")
	s.mu.Unlock()

	require.NoError(t, err)
}

// TestRenderIsAtomic pins Q25: a reader that reads the file while it is rendered over and over reads the old version
// or the new one, whole, every time.
func TestRenderIsAtomic(t *testing.T) {
	const renders, reads = 1000, 1000
	older, newer := golden(t, "start"), golden(t, "change1")
	path := filepath.Join(t.TempDir(), "flags", "allFlags.json")
	s := &Server{cfg: Config{Render: path}.WithDefaults()}
	s.cfg.Render = path
	require.NoError(t, s.render(older))

	var writing sync.WaitGroup
	writing.Add(1)
	stop := make(chan struct{})
	go func() {
		defer writing.Done()
		defer close(stop)
		for i := range renders {
			document := newer
			if i%2 == 1 {
				document = older
			}
			if err := s.render(document); err != nil {
				t.Errorf("render %d: %v", i, err)
				return
			}
		}
	}()

	count, torn := 0, 0
	for done := false; !done || count < reads; {
		select {
		case <-stop:
			done = true
		default:
		}
		data, err := os.ReadFile(path)
		require.NoError(t, err, "the file is there for every read")
		count++
		if string(data) != string(older) && string(data) != string(newer) {
			torn++
		}
		if !json.Valid(data) {
			t.Fatalf("read %d does not parse", count)
		}
	}
	writing.Wait()

	require.GreaterOrEqual(t, count, reads)
	require.Zero(t, torn, "every read is the old or the new version, whole")

	entries, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	require.Len(t, entries, 1, "no temporary file is left behind")
}

// The file is written the way the launchpad writes it: mode 0644, in a directory that is created.
func TestRenderWritesMode0644(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a", "b", "allFlags.json")
	s := &Server{cfg: Config{Render: path}.WithDefaults()}
	s.cfg.Render = path

	require.NoError(t, s.render([]byte("{}")))

	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o644), info.Mode().Perm())
}

func TestCombineSkipsSelectorFilesAndMergesDeeply(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
	}
	write("a.json", `{"flags": {"one": {"state": "ENABLED", "n": 1}}, "metadata": {"x": 1}}`)
	write("b.json", `{"flags": {"two": {"state": "DISABLED"}, "one": {"extra": true}}, "metadata": "replaced"}`)
	write("selector-c.json", `{"flags": {"three": {}}}`)
	write("notes.txt", `not json`)

	combined, err := combine(dir)
	require.NoError(t, err)
	document, version, err := encode(combined)
	require.NoError(t, err)

	require.JSONEq(t, `{"flags":{"one":{"state":"ENABLED","n":1,"extra":true},"two":{"state":"DISABLED"}},"metadata":"replaced"}`, string(document))
	require.Contains(t, string(document), "\n  \"flags\": {\n    \"one\": {", "two-space indentation")
	require.NotEqual(t, byte('\n'), document[len(document)-1], "no trailing newline")
	require.Len(t, version, 64)
}

func TestCombineReportsABadFile(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bad.json"), []byte("{"), 0o644))

	_, err := combine(dir)

	require.ErrorContains(t, err, "failed to parse JSON file bad.json")
	_, err = combine(filepath.Join(dir, "missing"))
	require.ErrorContains(t, err, "failed to read input directory")
}

func TestNumbersAreEncodedAsFloats(t *testing.T) {
	combined := map[string]any{}
	require.NoError(t, json.Unmarshal([]byte(`{"n": 10.0, "m": 1e3}`), &combined))

	document, _, err := encode(combined)

	require.NoError(t, err)
	require.JSONEq(t, `{"n":10,"m":1000}`, string(document))
	require.Contains(t, string(document), "\"n\": 10\n", "10.0 becomes 10")
}

// The source flag files are never written.
func TestRawFlagsAreNeverWritten(t *testing.T) {
	dir := t.TempDir()
	raw := filepath.Join(dir, "rawflags")
	require.NoError(t, os.MkdirAll(raw, 0o755))
	entries, err := os.ReadDir(filepath.Join("testdata", "rawflags"))
	require.NoError(t, err)
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join("testdata", "rawflags", e.Name()))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(raw, e.Name()), data, 0o444))
	}
	before := snapshot(t, raw)
	ts := newTestServer(t, &fakeTarget{}, func(c *Config) { c.RawFlags = raw })

	ts.get(t, "/change")
	ts.get(t, "/change")
	ts.get(t, "/restart?seconds=0")

	require.Equal(t, before, snapshot(t, raw), "no raw flag file changes, by content and modification time")
}

func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, e := range entries {
		info, err := e.Info()
		require.NoError(t, err)
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		require.NoError(t, err)
		out[e.Name()] = info.ModTime().String() + "|" + string(data)
	}
	return out
}
