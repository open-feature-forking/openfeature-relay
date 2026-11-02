package jsonlogic

import (
	"fmt"
	"strings"
	"testing"

	"github.com/open-feature/flagd/core/pkg/logger"
	"github.com/open-feature/flagd/core/pkg/store"
	"github.com/open-feature/flagd/core/pkg/sync"
)

// BenchmarkSetState applies 1,000 flags spread over 3 flag sets.
func BenchmarkSetState(b *testing.B) {
	const flagCount = 1000
	flags := make([]string, 0, flagCount)
	for i := 0; i < flagCount; i++ {
		flags = append(flags, fmt.Sprintf(
			`"flag-%d":{"state":"ENABLED","defaultVariant":"on","variants":{"on":true,"off":false},`+
				`"metadata":{"flagSetId":"set-%d"}}`, i, i%3))
	}
	payload := sync.DataSync{Source: "bench", FlagData: `{"flags":{` + strings.Join(flags, ",") + `}}`}

	log := logger.NewLogger(nil, false)
	s, err := store.NewStore(log, []string{"bench"})
	if err != nil {
		b.Fatal(err)
	}
	je := NewJSON(log, s)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := je.SetState(payload); err != nil {
			b.Fatal(err)
		}
	}
}
