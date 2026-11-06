package s3

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/stretchr/testify/require"
)

func withoutAWSConfig(t *testing.T) {
	t.Helper()
	t.Setenv("AWS_ACCESS_KEY_ID", "key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test-secret-access-key")
	t.Setenv("AWS_CONFIG_FILE", "/nonexistent")
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", "/nonexistent")
}

func createFetcher(query string) error {
	_, err := NewFactory().CreateFetcher(sync.Settings{
		Source: sync.SourceConfig{URI: "s3://bucket/flags.json?" + query}, Logger: logger.NewLogger(nil, false),
	})
	return err
}

// TestQueryOptionErrors pins the error of each invalid option, which wins when two are invalid, and that a
// repeated option is read from its first value.
func TestQueryOptionErrors(t *testing.T) {
	withoutAWSConfig(t)
	const boolSyntax = `strconv.ParseBool: parsing "maybe": invalid syntax`
	for _, tt := range []struct{ query, err string }{
		{"hostname_immutable=maybe", "open bucket s3://bucket?hostname_immutable=maybe: invalid value for hostname_immutable: " + boolSyntax},
		{"dualstack=maybe", "open bucket s3://bucket?dualstack=maybe: invalid value for dualstack: " + boolSyntax},
		{"fips=maybe", "open bucket s3://bucket?fips=maybe: invalid value for fips: " + boolSyntax},
		{"anonymous=maybe", "open bucket s3://bucket?anonymous=maybe: invalid value for anonymous: " + boolSyntax},
		{"rate_limiter_capacity=many", `open bucket s3://bucket?rate_limiter_capacity=many: invalid value for capacity: strconv.ParseInt: parsing "many": invalid syntax`},
		{"rate_limiter_capacity=4294967296", `open bucket s3://bucket?rate_limiter_capacity=4294967296: invalid value for capacity: strconv.ParseInt: parsing "4294967296": value out of range`},
		{"disable_https=maybe", `invalid value for "disable_https": ` + boolSyntax},
		{"s3ForcePathStyle=maybe", `invalid value for "s3ForcePathStyle": ` + boolSyntax},
		// precedence: the checks run in a fixed order
		{"ssetype=nope&foo=bar", `"nope" is not a valid value for "ssetype"`},
		{"accelerate=maybe&use_path_style=maybe", `invalid value for "accelerate": ` + boolSyntax},
		{"use_path_style=maybe&hostname_immutable=maybe", `invalid value for "use_path_style": ` + boolSyntax},
		// a repeated option is read from its first value
		{"hostname_immutable=true&hostname_immutable=maybe", ""},
		{"accelerate=true&accelerate=maybe", ""},
		{"ssetype=AES256&ssetype=nope", ""},
		{"hostname_immutable=maybe&hostname_immutable=true", "open bucket s3://bucket?hostname_immutable=maybe&hostname_immutable=true: invalid value for hostname_immutable: " + boolSyntax},
		// every option at once, each valid
		{"ssetype=AES256&kmskeyid=key&accelerate=true&disable_https=true&use_path_style=true&s3ForcePathStyle=true" +
			"&hostname_immutable=true&region=eu-west-1&endpoint=http://127.0.0.1:1&dualstack=true&fips=true" +
			"&rate_limiter_capacity=5&anonymous=true&awssdk=v2", ""},
	} {
		t.Run(tt.query, func(t *testing.T) {
			err := createFetcher(tt.query)
			if tt.err == "" {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, tt.err)
		})
	}
}

func TestOpenUnparsableURL(t *testing.T) {
	_, err := open(context.Background(), "s3://bucket\x7f", "flags.json", nil)
	require.EqualError(t, err, `parse "s3://bucket\x7f": net/url: invalid control character in URL`)
}

// TestClientOptions pins the options of the client a bucket URL builds.
func TestClientOptions(t *testing.T) {
	withoutAWSConfig(t)
	const endpoint = "http://127.0.0.1:1"
	for _, tt := range []struct {
		query                               string
		pathStyle, accelerate, disableHTTPS bool
		baseEndpoint                        *string
		region                              string
	}{
		{query: "region=eu-west-1", region: "eu-west-1"},
		{query: "region=eu-west-1&region=us-east-1", region: "eu-west-1"},
		{query: "endpoint=" + endpoint, baseEndpoint: aws.String(endpoint)},
		{query: "endpoint=" + endpoint + "&hostname_immutable=true", pathStyle: true, baseEndpoint: aws.String(endpoint)},
		{query: "endpoint=" + endpoint + "&hostname_immutable=false", baseEndpoint: aws.String(endpoint)},
		{query: "hostname_immutable=true"},
		{query: "use_path_style=true", pathStyle: true},
		{query: "s3ForcePathStyle=true", pathStyle: true},
		{query: "use_path_style=true&s3ForcePathStyle=false"},
		{query: "use_path_style=false&s3ForcePathStyle=true", pathStyle: true},
		{query: "accelerate=true", accelerate: true},
		{query: "disable_https=true", disableHTTPS: true},
	} {
		t.Run(tt.query, func(t *testing.T) {
			st, err := open(context.Background(), "s3://bucket?"+tt.query, "flags.json", nil)
			require.NoError(t, err)
			o := st.client.Options()
			require.Equal(t, tt.pathStyle, o.UsePathStyle, "UsePathStyle")
			require.Equal(t, tt.accelerate, o.UseAccelerate, "UseAccelerate")
			require.Equal(t, tt.disableHTTPS, o.EndpointOptions.DisableHTTPS, "DisableHTTPS")
			require.Equal(t, tt.baseEndpoint, o.BaseEndpoint, "BaseEndpoint")
			if tt.region != "" {
				require.Equal(t, tt.region, o.Region)
			}
			require.Equal(t, maxAttempts, o.Retryer.MaxAttempts())
			require.Equal(t, "bucket", st.bucket)
			require.Equal(t, "flags.json", st.key)
		})
	}
}

// TestRandomChoiceBetweenTwoFaultsIsKept pins a decided quirk (#177, Q2 (a)): with two unknown parameters the
// reported one depends on the map iteration, so over many calls both are reported. Making the choice
// deterministic is a change of its own.
func TestRandomChoiceBetweenTwoFaultsIsKept(t *testing.T) {
	withoutAWSConfig(t)
	seen := map[string]bool{}
	for range 1000 {
		_, err := configOptions(map[string][]string{"foo": {"1"}, "bar": {"2"}})
		require.Error(t, err)
		seen[err.Error()] = true
	}
	require.Equal(t, map[string]bool{
		`unknown query parameter "foo"`: true,
		`unknown query parameter "bar"`: true,
	}, seen)
}
