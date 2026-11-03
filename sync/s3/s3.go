// Package s3 is the sync provider for flag configurations in Amazon S3 buckets, and S3-compatible stores: a fetcher on
// the AWS SDK for Go v2.
//
// A source is "s3://bucket/path/to/object", with these options in its query:
//
//   - region, endpoint and profile choose the region, a custom endpoint and the shared config profile;
//   - use_path_style (or s3ForcePathStyle), disable_https, dualstack, fips, accelerate, anonymous and
//     hostname_immutable shape how the bucket is addressed and whether requests are signed;
//   - rate_limiter_capacity sets the capacity of the client's retry token bucket;
//   - ssetype and kmskeyid are accepted, and unused for reading;
//   - awssdk=v2 is accepted; awssdk=v1 is rejected, as only the AWS SDK v2 is used.
//
// Credentials come from the SDK's default chain.
package s3

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/ratelimit"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync/blobfetch"
)

// Provider is the name by which a source selects this sync provider.
const Provider = "s3"

// scheme matches through the first "/" after the scheme, e.g. "s3://bucket/".
var scheme = regexp.MustCompile("^s3://.+?/")

// Matches reports whether uri is an s3:// URI.
func Matches(uri string) bool {
	return scheme.MatchString(uri)
}

// maxAttempts is how often a request is tried: a failed fetch is tried again on the next poll.
const maxAttempts = 3

// NewFactory creates the factory of s3:// sources.
func NewFactory() blobfetch.Factory {
	return factory()
}

// factory creates the factory; the tests pass the shared config options that reach a stub.
func factory(extra ...func(*s3.Options)) blobfetch.Factory {
	return blobfetch.Factory{
		Kind: Provider, Prefix: "s3://", Scheme: scheme,
		Open: func(bucketURL, object string) (blobfetch.Store, error) {
			return open(context.Background(), bucketURL, object, extra)
		},
	}
}

// store is an object of a bucket. The ETag it reads is the one its last Identity call saw, so that what is read is
// what was compared.
type store struct {
	client *s3.Client
	bucket string
	key    string

	etag *string
}

// parseBool reads a boolean option, failing with the option's name.
func parseBool(name, value string) (bool, error) {
	b, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("invalid value for %q: %v", name, err)
	}
	return b, nil
}

// open reads the options of a bucket URL, as "s3://bucket?region=eu-west-1", and creates the client.
func open(ctx context.Context, bucketURL, object string, extra []func(*s3.Options)) (*store, error) {
	u, err := url.Parse(bucketURL)
	if err != nil {
		return nil, err
	}
	q := u.Query()

	if sseType := q.Get("ssetype"); sseType != "" {
		q.Del("ssetype")
		valid := false
		for _, v := range types.ServerSideEncryptionAes256.Values() {
			valid = valid || strings.EqualFold(string(v), sseType)
		}
		if !valid {
			return nil, fmt.Errorf("%q is not a valid value for %q", sseType, "ssetype")
		}
	}
	q.Del("kmskeyid")

	var clientOptions []func(*s3.Options)
	if value := q.Get("accelerate"); value != "" {
		q.Del("accelerate")
		accelerate, err := parseBool("accelerate", value)
		if err != nil {
			return nil, err
		}
		clientOptions = append(clientOptions, func(o *s3.Options) { o.UseAccelerate = accelerate })
	}
	if value := q.Get("disable_https"); value != "" {
		q.Del("disable_https")
		disable, err := parseBool("disable_https", value)
		if err != nil {
			return nil, err
		}
		clientOptions = append(clientOptions, func(o *s3.Options) { o.EndpointOptions.DisableHTTPS = disable })
	}
	pathStyle := false
	for _, key := range []string{"use_path_style", "s3ForcePathStyle"} {
		if value := q.Get(key); value != "" {
			q.Del(key)
			if pathStyle, err = parseBool(key, value); err != nil {
				return nil, err
			}
		}
	}

	loadOptions, endpoint, hostnameImmutable, rateLimitCapacity, err := configOptions(q)
	if err != nil {
		return nil, fmt.Errorf("open bucket %v: %v", u, err)
	}
	cfg, err := config.LoadDefaultConfig(ctx, loadOptions...)
	if err != nil {
		return nil, fmt.Errorf("open bucket %v: %v", u, err)
	}

	var limiter retry.RateLimiter = ratelimit.None
	if rateLimitCapacity > 0 {
		limiter = ratelimit.NewTokenRateLimit(uint(rateLimitCapacity))
	}
	clientOptions = append(clientOptions, func(o *s3.Options) {
		o.UsePathStyle = pathStyle || (endpoint != "" && hostnameImmutable)
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
		}
		o.Retryer = retry.NewStandard(func(so *retry.StandardOptions) {
			so.RateLimiter = limiter
			so.MaxAttempts = maxAttempts
		})
	})
	return &store{
		client: s3.NewFromConfig(cfg, append(clientOptions, extra...)...),
		bucket: u.Host,
		key:    object,
	}, nil
}

// configOptions reads the options that shape the SDK's shared config.
func configOptions(q url.Values) (opts []func(*config.LoadOptions) error, endpoint string, hostnameImmutable bool, rateLimitCapacity int64, err error) {
	for param, values := range q {
		value := values[0]
		switch param {
		case "hostname_immutable":
			if hostnameImmutable, err = strconv.ParseBool(value); err != nil {
				return nil, "", false, 0, fmt.Errorf("invalid value for hostname_immutable: %w", err)
			}
		case "region":
			opts = append(opts, config.WithRegion(value))
		case "endpoint":
			endpoint = value
		case "profile":
			opts = append(opts, config.WithSharedConfigProfile(value))
		case "dualstack":
			dualStack, err := strconv.ParseBool(value)
			if err != nil {
				return nil, "", false, 0, fmt.Errorf("invalid value for dualstack: %w", err)
			}
			if dualStack {
				opts = append(opts, config.WithUseDualStackEndpoint(aws.DualStackEndpointStateEnabled))
			}
		case "fips":
			fips, err := strconv.ParseBool(value)
			if err != nil {
				return nil, "", false, 0, fmt.Errorf("invalid value for fips: %w", err)
			}
			if fips {
				opts = append(opts, config.WithUseFIPSEndpoint(aws.FIPSEndpointStateEnabled))
			}
		case "rate_limiter_capacity":
			if rateLimitCapacity, err = strconv.ParseInt(value, 10, 32); err != nil {
				return nil, "", false, 0, fmt.Errorf("invalid value for capacity: %w", err)
			}
		case "anonymous":
			anon, err := strconv.ParseBool(value)
			if err != nil {
				return nil, "", false, 0, fmt.Errorf("invalid value for anonymous: %w", err)
			}
			if anon {
				opts = append(opts, config.WithCredentialsProvider(aws.AnonymousCredentials{}))
			}
		case "awssdk":
			if value == "v1" {
				return nil, "", false, 0, fmt.Errorf("unsupported value %q for \"awssdk\": only the AWS SDK v2 is supported", value)
			}
		default:
			return nil, "", false, 0, fmt.Errorf("unknown query parameter %q", param)
		}
	}
	return opts, endpoint, hostnameImmutable, rateLimitCapacity, nil
}

// Identity is the ETag of the object.
func (s *store) Identity(ctx context.Context) (string, time.Time, error) {
	head, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(s.key)})
	if err != nil {
		return "", time.Time{}, err
	}
	s.etag = head.ETag
	return aws.ToString(head.ETag), aws.ToTime(head.LastModified), nil
}

// Read reads the object with the ETag of the last Identity call, and fails when the object has changed since.
func (s *store) Read(ctx context.Context) ([]byte, string, error) {
	response, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket), Key: aws.String(s.key), IfMatch: s.etag,
	})
	if err != nil {
		return nil, "", err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, "", err
	}
	return data, aws.ToString(response.ContentType), nil
}

// NewFactories returns the factories of the s3 sync component, as a generated main of a distribution calls it.
func NewFactories() []sync.Factory { return []sync.Factory{NewFactory()} }
