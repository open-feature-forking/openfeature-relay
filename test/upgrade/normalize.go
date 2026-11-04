package upgrade

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
)

// Every rule below removes something that differs between two runs of the same flagd: a clock, a port, a random id.
// A rule that would hide a difference between the two flagds does not belong here; that is accepted.yaml's job.

var (
	// timestamps of the console and JSON log encodings, and RFC 3339 times in bodies
	timestamp = regexp.MustCompile(`\d{4}[-/]\d{2}[-/]\d{2}[T ]\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})?`)
	// the ephemeral ports of the two containers: the mapped ones differ, the container ports do not
	hostPort = regexp.MustCompile(`127\.0\.0\.1:\d{4,5}`)
	// the opaque validators of the flag configuration
	etag = regexp.MustCompile(`"?(flagConfigEtag|etag)"?\s*[:=]\s*"?[0-9a-f]{16,}"?`)
)

// Text removes the clocks and ports from a line of output.
func Text(s string) string {
	s = timestamp.ReplaceAllString(s, "<time>")
	s = hostPort.ReplaceAllString(s, "<addr>")
	return strings.TrimSpace(s)
}

// JSON renders a JSON body with its keys sorted and the validators masked; a body that is not JSON is returned as text.
func JSON(body []byte) string {
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return Text(string(body))
	}
	out, err := json.Marshal(v) // map keys are sorted by encoding/json
	if err != nil {
		return Text(string(body))
	}
	return etag.ReplaceAllString(Text(string(out)), `"$1":"<etag>"`)
}

// MetricNames returns the sorted, de-duplicated names of the metrics a Prometheus exposition lists, without their
// values, labels and help texts.
func MetricNames(exposition string) []string {
	seen := map[string]bool{}
	for _, line := range strings.Split(exposition, "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name := line
		if i := strings.IndexAny(line, "{ "); i >= 0 {
			name = line[:i]
		}
		seen[name] = true
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
