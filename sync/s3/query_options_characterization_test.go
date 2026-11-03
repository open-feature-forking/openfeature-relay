package s3

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"gocloud.dev/blob"
)

// TestQueryOptionsCharacterization pins which URL query options gocloud's S3 driver accepts when it opens a
// bucket, and how it fails for the others. Opening a bucket makes no request.
func TestQueryOptionsCharacterization(t *testing.T) {
	tests := []struct {
		query   string
		wantErr string
	}{
		{query: ""},
		{query: "region=eu-west-1"},
		{query: "endpoint=http://127.0.0.1:1"},
		{query: "use_path_style=true"},
		{query: "s3ForcePathStyle=true"},
		{query: "disable_https=true"},
		{query: "dualstack=true"},
		{query: "fips=true"},
		{query: "accelerate=true"},
		{query: "anonymous=true"},
		{query: "hostname_immutable=true"},
		{query: "rate_limiter_capacity=10"},
		{query: "awssdk=v2"},
		// accepted, although the driver only has the AWS SDK v2 path: the value is not checked
		{query: "awssdk=v1"},
		// write-only options are accepted
		{query: "ssetype=aws:kms"},
		{query: "kmskeyid=key"},
		{query: "profile=nope", wantErr: "open bucket s3://bucket?profile=nope: failed to get shared config profile, nope"},
		{
			query:   "use_path_style=maybe",
			wantErr: `invalid value for "use_path_style": strconv.ParseBool: parsing "maybe": invalid syntax`,
		},
		// the names of the AWS SDK v1 path are not accepted
		{query: "s3_force_path_style=true", wantErr: `open bucket s3://bucket?s3_force_path_style=true: unknown query parameter "s3_force_path_style"`},
		{query: "disableSSL=true", wantErr: `open bucket s3://bucket?disableSSL=true: unknown query parameter "disableSSL"`},
		{query: "disable_ssl=true", wantErr: `open bucket s3://bucket?disable_ssl=true: unknown query parameter "disable_ssl"`},
		{query: "foo=bar", wantErr: `open bucket s3://bucket?foo=bar: unknown query parameter "foo"`},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			uri := "s3://bucket"
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
