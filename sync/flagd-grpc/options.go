package flagdgrpc

import (
	"fmt"
	"net/textproto"
	"strconv"
	"strings"
	"time"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
	"google.golang.org/grpc/codes"
)

const (
	// selectorHeader is the metadata key that carries the selector, as the flagd providers send it.
	selectorHeader = "flagd-selector"

	// The provider's defaults for the retry policy, which fill the one of the two delays that is not set.
	defaultRetryBackoff    = 1000 * time.Millisecond
	defaultRetryBackoffMax = 5000 * time.Millisecond

	// minKeepAliveTime is grpc-go's floor: it raises a shorter interval to this one when the dial option is built.
	minKeepAliveTime = 10 * time.Second
)

// Options are the opt-in settings of a gRPC sync source. A zero value is today's behaviour. The factory fills them from
// the `grpc` object of the source; a provider that embeds the module can fill them itself.
type Options struct {
	// RetryBackoff and RetryBackoffMax are the initial and the largest delay of the retry policy and of the reconnect
	// delay. Zero means not set; when only one is set, the default fills the other.
	RetryBackoff    time.Duration
	RetryBackoffMax time.Duration
	// StreamDeadline recycles the sync stream after this long. Zero means no deadline.
	StreamDeadline time.Duration
	// KeepAliveTime is the interval of HTTP/2 keepalive pings. Zero means no keepalive parameters; otherwise at least 10 s.
	KeepAliveTime time.Duration
	// FatalStatusCodes end the source when one of them arrives before its first payload.
	FatalStatusCodes []codes.Code
	// SelectorHeader sends the selector as the flagd-selector header too.
	SelectorHeader bool
	// ReadyOnFirstPayload makes the source ready when its first payload arrived, not when its stream opened.
	ReadyOnFirstPayload bool
}

// OptionsFrom validates the `grpc` object of a source and reads it into Options. A source without the object has
// the zero Options. An error names the source and the setting.
func OptionsFrom(source sync.SourceConfig) (Options, error) {
	settings := source.GRPC
	if settings == nil {
		return Options{}, nil
	}
	fail := func(key, format string, args ...any) (Options, error) {
		return Options{}, fmt.Errorf("source %q: grpc.%s %s", source.URI, key, fmt.Sprintf(format, args...))
	}

	for _, setting := range []struct {
		key   string
		value int64
	}{
		{"retryBackoffMs", settings.RetryBackoffMs}, {"retryBackoffMaxMs", settings.RetryBackoffMaxMs},
		{"streamDeadlineMs", settings.StreamDeadlineMs}, {"keepAliveTime", settings.KeepAliveTime},
	} {
		if setting.value < 0 {
			return fail(setting.key, "is %d, but must not be negative", setting.value)
		}
	}
	opts := Options{
		RetryBackoff:        time.Duration(settings.RetryBackoffMs) * time.Millisecond,
		RetryBackoffMax:     time.Duration(settings.RetryBackoffMaxMs) * time.Millisecond,
		StreamDeadline:      time.Duration(settings.StreamDeadlineMs) * time.Millisecond,
		KeepAliveTime:       time.Duration(settings.KeepAliveTime) * time.Millisecond,
		SelectorHeader:      settings.SelectorHeader,
		ReadyOnFirstPayload: settings.ReadyOnFirstPayload,
	}

	// the one that is not set is filled with the provider's default, so the pair is checked as it will be used
	initial, largest := opts.RetryBackoff, opts.RetryBackoffMax
	if initial != 0 || largest != 0 {
		if initial == 0 {
			initial = defaultRetryBackoff
		}
		if largest == 0 {
			largest = defaultRetryBackoffMax
		}
		if initial > largest {
			return fail("retryBackoffMs", "is %d ms, above retryBackoffMaxMs (%d ms, which is %d ms when it is not set)",
				initial.Milliseconds(), largest.Milliseconds(), defaultRetryBackoffMax.Milliseconds())
		}
	}

	if opts.KeepAliveTime != 0 && opts.KeepAliveTime < minKeepAliveTime {
		return fail("keepAliveTime", "is %d ms, below 10000: grpc-go raises a shorter interval to 10 s, so set 10000 or more, or leave it out for no keepalive",
			settings.KeepAliveTime)
	}

	for _, name := range settings.FatalStatusCodes {
		code, ok := statusCode(name)
		if !ok {
			return fail("fatalStatusCodes", "names %q, which is not a gRPC status code (for example PERMISSION_DENIED)", name)
		}
		opts.FatalStatusCodes = append(opts.FatalStatusCodes, code)
	}

	if opts.SelectorHeader {
		for name := range source.Headers {
			if strings.EqualFold(textproto.CanonicalMIMEHeaderKey(name), selectorHeader) {
				return fail("selectorHeader", "is set, and headers has an entry %q: the selector would be sent twice", name)
			}
		}
		for name := range source.HeadersFrom {
			if strings.EqualFold(name, selectorHeader) {
				return fail("selectorHeader", "is set, and headersFrom has an entry %q: the selector would be sent twice", name)
			}
		}
	}
	return opts, nil
}

// statusCode maps the name of a gRPC status code, such as PERMISSION_DENIED, to the code. OK is no failure and is not
// accepted.
func statusCode(name string) (codes.Code, bool) {
	var code codes.Code
	if err := code.UnmarshalJSON([]byte(strconv.Quote(name))); err != nil || code == codes.OK {
		return 0, false
	}
	return code, true
}
