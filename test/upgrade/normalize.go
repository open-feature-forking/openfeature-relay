package upgrade

import (
	"bytes"
	"encoding/json"
	"net/http"
	"regexp"
	"sort"
	"strings"
)

// Every rule below removes something that differs between two runs of the same flagd: a clock, a port, a random id.
// A rule that would hide a difference between the two flagds does not belong here; that is accepted.yaml's job.
//
// The rules, each with what it masks:
//   - Text: clocks (a date and time of day) and the host ports the containers are published on.
//   - JSON: key order, and the value of flagConfigEtag and etag (opaque validators; #78). Numbers keep their literal.
//   - Headers: the Date header, and the values of ETag and Last-Modified (opaque validators and a clock).
//   - MetricSeries: the sample values and nothing else of a series.
//   - the span attributes in otlp_test.go: the keys listed in volatile, by exact name.

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
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return Text(string(body))
	}
	out, err := json.Marshal(v) // map keys are sorted by encoding/json
	if err != nil {
		return Text(string(body))
	}
	return etag.ReplaceAllString(Text(string(out)), `"$1":"<etag>"`)
}

// MetricSeries returns the sorted, de-duplicated series a Prometheus exposition lists: the # HELP and # TYPE lines, and
// for each sample its name, its label names and, for a histogram bucket, the le boundary. The values and the other label
// values are left out.
func MetricSeries(exposition string) []string {
	seen := map[string]bool{}
	for _, line := range strings.Split(exposition, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			seen[line] = true
			continue
		}
		name, labels := line, ""
		if i := strings.IndexAny(line, "{ "); i >= 0 {
			name = line[:i]
			if line[i] == '{' {
				if j := strings.LastIndex(line, "}"); j > i {
					labels = line[i+1 : j]
				}
			}
		}
		seen[name+"{"+strings.Join(labelNames(labels), ",")+"}"] = true
	}
	series := make([]string, 0, len(seen))
	for s := range seen {
		series = append(series, s)
	}
	sort.Strings(series)
	return series
}

var label = regexp.MustCompile(`([a-zA-Z_][a-zA-Z0-9_]*)="((?:[^"\\]|\\.)*)"`)

// labelNames returns the sorted label names of a sample, with the value of le.
func labelNames(labels string) []string {
	var names []string
	for _, m := range label.FindAllStringSubmatch(labels, -1) {
		if m[1] == "le" {
			names = append(names, "le="+m[2])
			continue
		}
		names = append(names, m[1])
	}
	sort.Strings(names)
	return names
}

// Headers renders the response headers, one sorted line each, without the Date header and with the values of ETag and
// Last-Modified reduced to "<set>".
func Headers(h http.Header) string {
	var lines []string
	for name, values := range h {
		switch http.CanonicalHeaderKey(name) {
		case "Date":
			continue
		case "Etag", "Last-Modified":
			values = []string{"<set>"}
		}
		lines = append(lines, http.CanonicalHeaderKey(name)+": "+strings.Join(values, ", "))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}
