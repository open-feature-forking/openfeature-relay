package upgrade

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

const durationSeries = `# HELP http_server_request_duration_seconds Duration of requests.
# TYPE http_server_request_duration_seconds histogram
http_server_request_duration_seconds_bucket{http_response_status_code="200",http_route="/a",le="5"} 3
http_server_request_duration_seconds_count{http_response_status_code="200",http_route="/a"} 3
`

func TestMetricSeriesKeepsLabelValues(t *testing.T) {
	base := MetricSeries(durationSeries)

	t.Run("the sample values are masked", func(t *testing.T) {
		other := `# HELP http_server_request_duration_seconds Duration of requests.
# TYPE http_server_request_duration_seconds histogram
http_server_request_duration_seconds_bucket{http_response_status_code="200",http_route="/a",le="5"} 99
http_server_request_duration_seconds_count{http_response_status_code="200",http_route="/a"} 99
`
		require.Equal(t, base, MetricSeries(other))
	})
	t.Run("a renamed route differs", func(t *testing.T) {
		require.NotEqual(t, base, MetricSeries(durationSeries+`http_server_request_duration_seconds_count{http_response_status_code="200",http_route="/renamed"} 3`))
	})
	t.Run("a changed status code differs", func(t *testing.T) {
		changed := `# HELP http_server_request_duration_seconds Duration of requests.
# TYPE http_server_request_duration_seconds histogram
http_server_request_duration_seconds_bucket{http_response_status_code="500",http_route="/a",le="5"} 3
http_server_request_duration_seconds_count{http_response_status_code="500",http_route="/a"} 3
`
		require.NotEqual(t, base, MetricSeries(changed))
	})
	t.Run("another bucket boundary differs", func(t *testing.T) {
		require.NotEqual(t, base, MetricSeries(durationSeries+`http_server_request_duration_seconds_bucket{http_response_status_code="200",http_route="/a",le="7"} 3`))
	})
	t.Run("the host name of the resource is masked", func(t *testing.T) {
		a := `target_info{host_name="aaa",os_description="Linux aaa 6.6",service_name="flagd"} 1`
		b := `target_info{host_name="bbb",os_description="Linux bbb 6.6",service_name="flagd"} 1`
		require.Equal(t, MetricSeries(a), MetricSeries(b))
		require.NotEqual(t, MetricSeries(a), MetricSeries(`target_info{host_name="bbb",os_description="Linux bbb 6.7",service_name="flagd"} 1`),
			"the rest of the OS description is compared")
		require.NotEqual(t, MetricSeries(a), MetricSeries(`target_info{host_name="bbb",os_description="Linux bbb 6.6",service_name="other"} 1`))
	})
}

func TestJSONKeepsNumberLiterals(t *testing.T) {
	require.NotEqual(t, JSON([]byte(`{"value":1}`)), JSON([]byte(`{"value":1.0}`)))
	require.NotEqual(t, JSON([]byte(`{"value":9007199254740993}`)), JSON([]byte(`{"value":9007199254740992}`)))
	require.Equal(t, JSON([]byte(`{"a":1,"b":2}`)), JSON([]byte(`{"b":2,"a":1}`)), "the order of keys is masked")
}

func TestHeadersMasksOnlyTheVariableOnes(t *testing.T) {
	a := http.Header{"Date": {"Mon"}, "Etag": {`"x"`}, "Last-Modified": {"then"}, "Content-Type": {"application/json"}}
	b := http.Header{"Date": {"Tue"}, "Etag": {`"y"`}, "Last-Modified": {"later"}, "Content-Type": {"application/json"}}
	require.Equal(t, Headers(a), Headers(b))
	b.Set("Content-Type", "text/plain")
	require.NotEqual(t, Headers(a), Headers(b))
}
