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
	deadlineExceeded := status.Error(codes.DeadlineExceeded, "")
	require.False(t, s.deadlineElapsed(parent, coming, deadlineExceeded), "DEADLINE_EXCEEDED long before the deadline is a failure")

	// the server ends the stream a little earlier than the client's deadline: within the slack of 100 ms it is a recycle
	near := pastDeadline{context.Background(), time.Now().Add(50 * time.Millisecond)}
	require.True(t, s.deadlineElapsed(parent, near, deadlineExceeded), "DEADLINE_EXCEEDED just before the deadline")
	require.False(t, s.deadlineElapsed(parent, near, eof), "another error just before the deadline is a failure")
	short := &Sync{Options: Options{StreamDeadline: 10 * time.Second}} // 5% of it, 500 ms, is the slack
	nearer := pastDeadline{context.Background(), time.Now().Add(400 * time.Millisecond)}
	require.True(t, short.deadlineElapsed(parent, nearer, deadlineExceeded), "the slack is 5% of a long deadline")

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	require.False(t, s.deadlineElapsed(cancelled, passed, eof), "a shutdown is not a recycle")

	s.Options = Options{}
	require.False(t, s.deadlineElapsed(parent, passed, deadlineExceeded), "without the setting nothing is recycled")
}
