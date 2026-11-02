package jsonlogic

import (
	"testing"

	"github.com/open-feature-forking/openfeature-relay/core/pkg/logger"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/store"
	"github.com/open-feature-forking/openfeature-relay/core/pkg/sync"
)

// foreignStore hides the *store.Store behind the IStore interface, as an implementation from
// elsewhere would.
type foreignStore struct{ store.IStore }

func TestSetStateWithForeignIStore(t *testing.T) {
	log := logger.NewLogger(nil, false)
	inner, err := store.NewStore(log, []string{"src"})
	if err != nil {
		t.Fatal(err)
	}
	je := NewJSON(log, foreignStore{inner})

	valid := `{"flags":{"f":{"state":"ENABLED","defaultVariant":"on","variants":{"on":true}}}}`
	if err := je.SetState(sync.DataSync{Source: "src", FlagData: valid}); err != nil {
		t.Fatal(err)
	}
	flags, _, err := inner.GetAll(t.Context(), &store.Selector{})
	if err != nil || len(flags) != 1 {
		t.Fatalf("want 1 flag, got %d, %v", len(flags), err)
	}
	if n := len(inner.Documents("flagd", "src")); n != 0 {
		t.Fatalf("a foreign store gets no documents, got %d", n)
	}

	invalid := `{"flags":{"f":{"state":"ENABLED","defaultVariant":"nope","variants":{"on":true}}}}`
	err = je.SetState(sync.DataSync{Source: "src", FlagData: invalid})
	if err == nil || err.Error() != "default variant: 'nope' isn't a valid variant of flag: 'f'" {
		t.Fatalf("want today's error text, got %v", err)
	}
}
