package helper

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// streamConn is the last writer in a streaming response: it serializes frames
// to the real connection and owns the SSE headers. Comments and content both
// pass through it, so frames never interleave.
type streamConn struct {
	mu     sync.Mutex
	raw    gin.ResponseWriter
	budget StreamWriteBudget
	closed bool
}

func newStreamConn(c *gin.Context) *streamConn {
	return &streamConn{raw: c.Writer}
}

func (s *streamConn) write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return len(p), nil
	}
	// Keep the same bounded client-write contract as streamResponseWriter.
	// Both comments and content share this budget under the connection lock.
	_ = http.NewResponseController(s.raw).SetWriteDeadline(s.budget.Deadline(time.Now(), len(p)))
	n, err := s.raw.Write(p)
	if err == nil {
		err = flushStreamWriter(s.raw)
	}
	return n, err
}

// flushStreamWriter preserves wrapper buffers and propagates the underlying
// connection's flush error. Its caller owns the writer's serialization lock.
func flushStreamWriter(raw gin.ResponseWriter) error {
	if flusher, ok := raw.(interface{ FlushError() error }); ok {
		return flusher.FlushError()
	}
	raw.Flush()
	var writer http.ResponseWriter = raw
	for {
		if flusher, ok := writer.(interface{ FlushError() error }); ok {
			return flusher.FlushError()
		}
		unwrapper, ok := writer.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return nil
		}
		writer = unwrapper.Unwrap()
	}
}

// applyStreamHeaders sets the SSE headers once, on the request goroutine, without
// replacing c.Writer. SetEventStreamHeaders installs a wrapper by rewriting
// c.Writer, which would break writer restoration and race with any goroutine, so
// the relay layers own the header decision instead.
func applyStreamHeaders(c *gin.Context) {
	if c == nil {
		return
	}
	if _, exists := c.Get("event_stream_headers_set"); exists {
		return
	}
	c.Set("event_stream_headers_set", true)
	header := c.Writer.Header()
	header.Set("Content-Type", "text/event-stream")
	header.Set("Cache-Control", "no-cache")
	header.Set("Connection", "keep-alive")
	header.Set("Transfer-Encoding", "chunked")
	header.Set("X-Accel-Buffering", "no")
}

func (s *streamConn) close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
}

// StreamCommentEnvelope is the request-wide sink for keepalive comments. It
// sits *below* the response wrappers (gate, then plugin reply wrapper): it never
// calls into them, and it reports its own record of whether real content has
// started, which is what the retry guard needs while the connection is kept
// alive with comments.
type StreamCommentEnvelope struct {
	gin.ResponseWriter

	conn *streamConn

	mu         sync.Mutex
	started    bool
	pinging    bool
	finished   bool
	stop       chan struct{}
	joined     chan struct{}
	stopOnce   *sync.Once
	finishOnce sync.Once
}

const streamCommentEnvelopeContextKey = "stream_comment_envelope"

// InstallStreamCommentEnvelope wraps the current writer for the whole request.
func InstallStreamCommentEnvelope(c *gin.Context) *StreamCommentEnvelope {
	applyStreamHeaders(c)
	conn := newStreamConn(c)
	envelope := &StreamCommentEnvelope{ResponseWriter: c.Writer, conn: conn}
	c.Set(streamCommentEnvelopeContextKey, envelope)
	c.Writer = envelope
	return envelope
}

// StreamCommentEnvelopeFrom returns the envelope installed for this request.
func StreamCommentEnvelopeFrom(c *gin.Context) *StreamCommentEnvelope {
	value, ok := c.Get(streamCommentEnvelopeContextKey)
	if !ok {
		return nil
	}
	envelope, _ := value.(*StreamCommentEnvelope)
	return envelope
}

// StartKeepAlive writes comment frames until the returned stop function is
// called. Each cycle owns its own stop channel, stop is idempotent, and it joins
// the goroutine before returning so a later cycle starts clean.
func (e *StreamCommentEnvelope) StartKeepAlive(interval time.Duration) func() {
	if interval <= 0 {
		interval = DefaultPingInterval
	}
	e.mu.Lock()
	if e.pinging || e.finished || e.conn == nil {
		e.mu.Unlock()
		return func() {}
	}
	e.pinging = true
	stop := make(chan struct{})
	joined := make(chan struct{})
	once := &sync.Once{}
	e.stop, e.joined = stop, joined
	e.stopOnce = once
	e.mu.Unlock()

	go func() {
		defer close(joined)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				if _, err := e.conn.write([]byte(": keepalive\n\n")); err != nil {
					return
				}
			}
		}
	}()

	return func() {
		once.Do(func() { close(stop) })
		<-joined
		e.mu.Lock()
		if e.stop == stop {
			e.pinging = false
			e.stop, e.joined = nil, nil
			e.stopOnce = nil
		}
		e.mu.Unlock()
	}
}

// Finish stops and joins any keepalive, closes the sink and restores the
// request's writer.
func (e *StreamCommentEnvelope) Finish(c *gin.Context) {
	e.finishOnce.Do(func() {
		e.mu.Lock()
		e.finished = true
		stop := e.stop
		joined := e.joined
		once := e.stopOnce
		e.pinging = false
		e.stop, e.joined = nil, nil
		e.stopOnce = nil
		e.mu.Unlock()
		if stop != nil && once != nil {
			once.Do(func() { close(stop) })
		}
		if joined != nil {
			<-joined
		}
		if e.conn != nil {
			e.conn.close()
		}
		if current, ok := c.Get(streamCommentEnvelopeContextKey); ok && current == e {
			c.Set(streamCommentEnvelopeContextKey, nil)
		}
		if e.conn != nil {
			detachWriter(c, e, e.conn.raw)
		}
	})
}

// detachWriter removes one of this package's wrappers from the request writer
// chain and relinks the layer above it. Assigning c.Writer directly only works
// while the wrapper is outermost; a gate can sit above the envelope, so the
// removal has to look one level down.
func detachWriter(c *gin.Context, target, replacement gin.ResponseWriter) {
	if c == nil || target == nil || replacement == nil {
		return
	}
	if gate, ok := c.Writer.(*streamCommitWriter); ok {
		if gate.ResponseWriter == target {
			gate.ResponseWriter = replacement
		}
		if gate == target {
			c.Writer = replacement
		}
		return
	}
	if c.Writer == target {
		c.Writer = replacement
	}
}

func (e *StreamCommentEnvelope) Unwrap() http.ResponseWriter {
	return e.conn.raw
}

func (e *StreamCommentEnvelope) SetWriteDeadline(deadline time.Time) error {
	e.conn.mu.Lock()
	defer e.conn.mu.Unlock()
	return http.NewResponseController(e.conn.raw).SetWriteDeadline(deadline)
}

// Write forwards everything to the connection and records whether real content
// (anything that is not a bare comment frame) has started.
func (e *StreamCommentEnvelope) Write(p []byte) (int, error) {
	if !isStreamCommentFrame(p) {
		e.mu.Lock()
		e.started = true
		e.mu.Unlock()
	}
	return e.conn.write(p)
}

func (e *StreamCommentEnvelope) WriteString(s string) (int, error) {
	return e.Write([]byte(s))
}

// ContentStarted reports whether a non-comment frame reached the connection.
// Comment keepalives deliberately do not count.
func (e *StreamCommentEnvelope) ContentStarted() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.started
}

// Written reports the envelope's own content state. The gate above keeps its
// own committed state; both must be false for an attempt to stay retryable.
func (e *StreamCommentEnvelope) Written() bool {
	return e.ContentStarted()
}

// ConnectionWritten includes keepalive comments. Final errors need this state
// to stay in SSE after headers were sent; retry decisions use Written instead.
func (e *StreamCommentEnvelope) ConnectionWritten() bool {
	e.conn.mu.Lock()
	defer e.conn.mu.Unlock()
	return e.conn.raw.Written()
}

// Commit marks the connection as carrying content, which the layer above calls
// when it releases real output.
func (e *StreamCommentEnvelope) Commit() {
	e.mu.Lock()
	e.started = true
	e.mu.Unlock()
}

func (e *StreamCommentEnvelope) WriteHeader(code int) {
	e.conn.mu.Lock()
	defer e.conn.mu.Unlock()
	if !e.conn.closed {
		e.conn.raw.WriteHeader(code)
	}
}

func (e *StreamCommentEnvelope) Status() int {
	e.conn.mu.Lock()
	defer e.conn.mu.Unlock()
	return e.conn.raw.Status()
}

func (e *StreamCommentEnvelope) Size() int {
	e.conn.mu.Lock()
	defer e.conn.mu.Unlock()
	return e.conn.raw.Size()
}

func (e *StreamCommentEnvelope) WriteHeaderNow() {
	e.mu.Lock()
	started := e.started
	e.mu.Unlock()
	if started {
		e.conn.mu.Lock()
		defer e.conn.mu.Unlock()
		if !e.conn.closed {
			_ = http.NewResponseController(e.conn.raw).SetWriteDeadline(e.conn.budget.Deadline(time.Now(), 0))
			e.conn.raw.WriteHeaderNow()
		}
	}
}

func (e *StreamCommentEnvelope) Flush() {
	_ = e.FlushError()
}

func (e *StreamCommentEnvelope) FlushError() error {
	e.mu.Lock()
	started := e.started
	e.mu.Unlock()
	if !started {
		return nil
	}
	e.conn.mu.Lock()
	defer e.conn.mu.Unlock()
	if e.conn.closed {
		return nil
	}
	_ = http.NewResponseController(e.conn.raw).SetWriteDeadline(e.conn.budget.Deadline(time.Now(), 0))
	return flushStreamWriter(e.conn.raw)
}

// isStreamCommentFrame reports whether the payload is only SSE comment lines,
// the one frame type a client ignores.
func isStreamCommentFrame(p []byte) bool {
	trimmed := strings.TrimSpace(string(p))
	if trimmed == "" {
		return false
	}
	for _, line := range strings.Split(trimmed, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, ":") {
			return false
		}
	}
	return true
}
