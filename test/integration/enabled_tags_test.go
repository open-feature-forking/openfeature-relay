package integration_test

import (
	"os"
	"strings"
)

// enabledTagsEnv lists, comma separated, the scenarios that testbed-suites.sh switches on for both images: each tag
// loses its exclusion in the tag expression of the suites. Unset, the suites run as upstream b23f82ec runs them.
const enabledTagsEnv = "TESTBED_ENABLE_TAGS"

// withEnabledTags removes the exclusion of every tag in enabled from the tag expression.
func withEnabledTags(expression, enabled string) string {
	for _, tag := range strings.Split(enabled, ",") {
		if tag = strings.TrimSpace(tag); tag != "" {
			expression = strings.ReplaceAll(expression, " && ~"+tag, "")
		}
	}
	return expression
}

// suiteTags is the tag expression a suite runs with, after the tags the environment switches on.
func suiteTags(expression string) string {
	return withEnabledTags(expression, os.Getenv(enabledTagsEnv))
}
