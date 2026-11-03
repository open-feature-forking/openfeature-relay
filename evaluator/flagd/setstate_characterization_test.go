package flagd

import (
	"testing"

	flagconfigtest "github.com/open-feature-forking/openfeature-relay/configformat/flagd/flagdtest"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
)

func TestSetStateCharacterization(t *testing.T) {
	flagconfigtest.Run(t, flagconfigtest.NewStore, func(s store.IStore, d sync.DataSync) error {
		return NewJSON(logger.NewLogger(nil, false), s).SetState(d)
	})
}
