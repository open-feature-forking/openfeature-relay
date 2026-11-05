package flagdgrpc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// pastDeadline is a context whose deadline has passed and whose timer has not yet reported it.
type pastDeadline struct {
	context.Context
	deadline time.Time
}

func (c pastDeadline) Deadline() (time.Time, bool) { return c.deadline, true }

func TestDeadlineElapsedBeforeTheContextReportsIt(t *testing.T) {
	s := defaultSync("host:8013")
	s.Options = Options{StreamDeadline: time.Second}
	parent := context.Background()
	passed := pastDeadline{context.Background(), time.Now().Add(-time.Millisecond)}
	coming := pastDeadline{context.Background(), time.Now().Add(time.Hour)}
	eof := errors.New("error receiving payload from stream: EOF")

	require.True(t, s.deadlineElapsed(parent, passed, eof), "the stream ended after its deadline")
	require.False(t, s.deadlineElapsed(parent, coming, eof), "the stream ended long before its deadline")
	require.True(t, s.deadlineElapsed(parent, coming, status.Error(codes.DeadlineExceeded, "")), "the server reported the deadline")

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	require.False(t, s.deadlineElapsed(cancelled, passed, eof), "a shutdown is not a recycle")

	s.Options = Options{}
	require.False(t, s.deadlineElapsed(parent, passed, status.Error(codes.DeadlineExceeded, "")), "without the setting nothing is recycled")
}
