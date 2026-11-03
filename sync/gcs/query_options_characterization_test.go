package gcs

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gocloud.dev/blob"
)

// TestQueryOptionsCharacterization pins which URL query options gocloud's GCS driver accepts when it opens
// a bucket, and how it fails for the others. Opening a bucket makes no request.
func TestQueryOptionsCharacterization(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "key.pem")
	require.NoError(t, os.WriteFile(keyPath, []byte("not a key"), 0o600))

	tests := []struct {
		query   string
		wantErr string
	}{
		{query: ""},
		{query: "anonymous=true"},
		{query: "anonymous=false"},
		{query: "access_id=id"},
		{query: "access_id=id&anonymous=true"},
		{query: "private_key_path=" + keyPath},
		{
			query:   "anonymous=maybe",
			wantErr: `open bucket gs://bucket?anonymous=maybe: invalid value "maybe" for query parameter "anonymous": strconv.ParseBool: parsing "maybe": invalid syntax`,
		},
		{
			query:   "private_key_path=/nonexistent",
			wantErr: "open bucket gs://bucket?private_key_path=/nonexistent: open /nonexistent: no such file or directory",
		},
		{query: "foo=bar", wantErr: `open bucket gs://bucket?foo=bar: invalid query parameter "foo"`},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			uri := "gs://bucket"
			if tt.query != "" {
				uri += "?" + tt.query
			}
			b, err := blob.OpenBucket(context.Background(), uri)
			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.NoError(t, b.Close())
		})
	}
}
