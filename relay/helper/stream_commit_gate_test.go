package helper

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newStreamTestContext(t *testing.T) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	return c, recorder
}

// Expose completed writes without reading a recorder while a timer mutates it.
// Optional barriers hold exactly one frame, allowing deterministic overlap.
type observedStreamWriter struct {
	gin.ResponseWriter
	frames   chan string
	entered  chan struct{}
	release  chan struct{}
	deadline time.Time
	flushErr error
}

func (w *observedStreamWriter) Write(p []byte) (int, error) {
	if w.entered != nil {
		close(w.entered)
		w.entered = nil
		<-w.release
	}
	n, err := w.ResponseWriter.Write(p)
	select {
	case w.frames <- string(p):
	default:
	}
	return n, err
}

func (w *observedStreamWriter) SetWriteDeadline(deadline time.Time) error {
	w.deadline = deadline
	return nil
}

func (w *observedStreamWriter) FlushError() error {
	w.ResponseWriter.Flush()
	return w.flushErr
}

func TestStreamCommitGateCommitsThroughEnvelope(t *testing.T) {
	c, recorder := newStreamTestContext(t)
	original := c.Writer
	envelope := InstallStreamCommentEnvelope(c)
	gate := InstallStreamCommitGate(c, time.Minute)
	t.Cleanup(func() { gate.Finish(c); envelope.Finish(c) })
	gate.Hold()
	const frame = "event: response.created\ndata: {}\n\n"
	_, err := c.Writer.Write([]byte(frame))
	require.NoError(t, err)
	assert.False(t, c.Writer.Written())
	assert.Empty(t, recorder.Body.String())
	require.NoError(t, gate.Commit())
	_, err = c.Writer.Write([]byte("data: later\n\n"))
	require.NoError(t, err)
	gate.Finish(c)
	gate.Finish(c) // A stopped timer must never be joined a second time.
	envelope.Finish(c)
	assert.Same(t, original, c.Writer)
	assert.Equal(t, frame+"data: later\n\n", recorder.Body.String())
}

func TestStreamCommitGateCommitsOnHoldBudget(t *testing.T) {
	c, recorder := newStreamTestContext(t)
	gate := InstallStreamCommitGate(c, 10*time.Millisecond)
	t.Cleanup(func() { gate.Finish(c) })
	gate.Hold()
	_, err := c.Writer.Write([]byte("data: held\n\n"))
	require.NoError(t, err)
	select {
	case <-gate.timerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("hold budget did not commit")
	}
	gate.Finish(c)
	gate.Finish(c)
	assert.Equal(t, "data: held\n\n", recorder.Body.String())
}

func TestStreamCommitGateRetryAfterStoppedKeepAlive(t *testing.T) {
	c, recorder := newStreamTestContext(t)
	envelope := InstallStreamCommentEnvelope(c)
	t.Cleanup(func() { envelope.Finish(c) })
	for attempt := range 2 {
		gate := InstallStreamCommitGate(c, time.Minute)
		t.Cleanup(func() { gate.Finish(c) })
		stop := StartStreamKeepAlive(c, time.Minute)
		reused := StartStreamKeepAlive(c, time.Minute)
		reused() // Nested callers do not own the live keepalive or its writer.
		require.NoError(t, PingData(c))
		assert.False(t, gate.Committed(), "keepalive before protocol selection must remain retryable")
		gate.Hold()
		_, err := c.Writer.Write([]byte("data: response.created\n\n"))
		require.NoError(t, err)
		stop()
		stop()
		if attempt == 0 {
			gate.Discard()
		} else {
			require.NoError(t, gate.Commit())
			_, err = c.Writer.Write([]byte("data: successful-response\n\n"))
			require.NoError(t, err)
		}
		gate.Finish(c)
		assert.Same(t, envelope, c.Writer, "the finished attempt must not remain in the writer chain")
		if attempt == 0 {
			assert.Empty(t, recorder.Body.String())
			assert.False(t, c.Writer.Written())
		}
	}
	assert.Equal(t, ": PING\n\ndata: response.created\n\ndata: successful-response\n\n", recorder.Body.String())
}

func TestStreamCommitGateWithoutProtocolHoldWritesThrough(t *testing.T) {
	c, recorder := newStreamTestContext(t)
	gate := InstallStreamCommitGate(c, time.Minute)
	t.Cleanup(func() { gate.Finish(c) })
	require.NoError(t, PingData(c))
	assert.False(t, gate.Committed(), "transport keepalive cannot commit before the handler chooses its protocol")
	assert.Empty(t, recorder.Body.String())
	require.NoError(t, StringData(c, "converted response"))
	assert.True(t, gate.Committed())
	gate.Finish(c)
	assert.Equal(t, ": PING\n\ndata: converted response\n\n", recorder.Body.String())
}

func TestStreamCommentKeepAliveSpansTwoWaitCycles(t *testing.T) {
	c, recorder := newStreamTestContext(t)
	writer := &observedStreamWriter{ResponseWriter: c.Writer, frames: make(chan string, 16)}
	c.Writer = writer
	envelope := InstallStreamCommentEnvelope(c)
	t.Cleanup(func() { envelope.Finish(c) })
	for range 2 {
		stop := envelope.StartKeepAlive(time.Millisecond)
		select {
		case frame := <-writer.frames:
			assert.Equal(t, ": keepalive\n\n", frame)
		case <-time.After(2 * time.Second):
			t.Fatal("keepalive cycle wrote no frame")
		}
		stop()
		stop()
		assert.Same(t, envelope, c.Writer, "background writes never replace c.Writer")
		assert.False(t, c.Writer.Written(), "comments must not prevent failover")
		assert.True(t, envelope.ConnectionWritten())
		for len(writer.frames) > 0 {
			<-writer.frames
		}
	}
	stop := envelope.StartKeepAlive(time.Minute)
	envelope.Finish(c)
	stop() // Finish and a retained stop closure share one close operation.
	envelope.Finish(c)
	assert.GreaterOrEqual(t, strings.Count(recorder.Body.String(), ": keepalive"), 2)
	assert.Same(t, writer, c.Writer)
}

func TestStreamCommentAndContentStayFrameAligned(t *testing.T) {
	c, recorder := newStreamTestContext(t)
	entered, release := make(chan struct{}), make(chan struct{})
	writer := &observedStreamWriter{ResponseWriter: c.Writer, frames: make(chan string, 2), entered: entered, release: release}
	c.Writer = writer
	envelope := InstallStreamCommentEnvelope(c)
	results := make(chan error, 2)
	go func() { _, err := envelope.Write([]byte(": keepalive\n\n")); results <- err }()
	<-entered
	contentStarted := make(chan struct{})
	go func() { close(contentStarted); _, err := envelope.Write([]byte("data: content\n\n")); results <- err }()
	<-contentStarted
	close(release)
	for range 2 {
		require.NoError(t, <-results)
	}
	envelope.Finish(c)
	assert.Equal(t, ": keepalive\n\ndata: content\n\n", recorder.Body.String())
	assert.False(t, writer.deadline.IsZero(), "the sink must bound client writes")
}

func TestStreamCommitGatePreservesWriteFailures(t *testing.T) {
	for _, withEnvelope := range []bool{false, true} {
		t.Run(map[bool]string{false: "standalone", true: "envelope"}[withEnvelope], func(t *testing.T) {
			c, _ := newStreamTestContext(t)
			failure := errors.New("downstream flush failed")
			writer := &observedStreamWriter{ResponseWriter: c.Writer, frames: make(chan string, 2), flushErr: failure}
			c.Writer = writer
			var envelope *StreamCommentEnvelope
			if withEnvelope {
				envelope = InstallStreamCommentEnvelope(c)
			}
			gate := InstallStreamCommitGate(c, time.Minute)
			t.Cleanup(func() {
				gate.Finish(c)
				if envelope != nil {
					envelope.Finish(c)
				}
			})
			gate.Hold()
			_, err := c.Writer.Write([]byte("data: first\n\n"))
			require.NoError(t, err)
			assert.ErrorIs(t, gate.Commit(), failure)
			assert.True(t, gate.Committed(), "a failed write must never allow replay")
			assert.ErrorIs(t, FlushWriter(c), failure)
			assert.ErrorIs(t, gate.Commit(), failure)
			assert.False(t, writer.deadline.IsZero())
		})
	}
}

// Preserve the old SSE encoding while making each event a single write.
func TestStreamHelpersPreserveWholeFrames(t *testing.T) {
	for _, tc := range []struct {
		name  string
		event string
		data  string
		send  func(*gin.Context) error
	}{
		{name: "data", data: "hello\rworld\nagain", send: func(c *gin.Context) error { return StringData(c, "hello\rworld\nagain") }},
		{name: "responses", event: "response.created", data: "{\"id\":\"r1\"}", send: func(c *gin.Context) error {
			return ResponseChunkData(c, dto.ResponsesStreamResponse{Type: "response.created"}, "{\"id\":\"r1\"}")
		}},
		{name: "claude", event: "ping", send: func(c *gin.Context) error { return ClaudeData(c, dto.ClaudeResponse{Type: "ping"}) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, recorder := newStreamTestContext(t)
			writer := &observedStreamWriter{ResponseWriter: c.Writer, frames: make(chan string, 1)}
			c.Writer = writer
			want := httptest.NewRecorder()
			if tc.event != "" {
				require.NoError(t, (common.CustomEvent{Data: "event: " + tc.event + "\n"}).Render(want))
			}
			data := tc.data
			if tc.name == "claude" {
				body, err := common.Marshal(dto.ClaudeResponse{Type: "ping"})
				require.NoError(t, err)
				data = string(body)
			}
			require.NoError(t, (common.CustomEvent{Data: "data: " + data}).Render(want))
			require.NoError(t, tc.send(c))
			assert.Equal(t, want.Body.String(), recorder.Body.String())
			assert.Len(t, writer.frames, 1)
			assert.Equal(t, want.Body.String(), <-writer.frames)
		})
	}
}
