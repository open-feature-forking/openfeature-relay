package configformat

import (
	"errors"
	"testing"
)

func TestRejectionNamesSourceAndEntry(t *testing.T) {
	var err error = Rejection{Source: "file:flags.json", Entry: "myFlag", Reason: "no default variant"}

	var r Rejection
	if !errors.As(err, &r) {
		t.Fatal("a Rejection must be usable as an error")
	}
	want := `source "file:flags.json", entry "myFlag": no default variant`
	if err.Error() != want {
		t.Fatalf("want %q, got %q", want, err.Error())
	}
}
