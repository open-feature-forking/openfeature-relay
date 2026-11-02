package jsonlogic

import (
	"testing"

	flagconfigtest "github.com/open-feature/flagd/core/pkg/configformat/flagd/flagdtest"
	"github.com/open-feature/flagd/core/pkg/logger"
	"github.com/open-feature/flagd/core/pkg/store"
	"github.com/open-feature/flagd/core/pkg/sync"
)

func TestSetStateCharacterization(t *testing.T) {
	flagconfigtest.Run(t, flagconfigtest.NewStore, func(s store.IStore, d sync.DataSync) error {
		return NewJSON(logger.NewLogger(nil, false), s).SetState(d)
	})
}
