package helper

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// StreamCommitGate holds the beginning of a streaming attempt until the attempt
// produces something a client could act on, so an upstream that answers HTTP 200
// and then fails can still be retried: nothing has reached the real writer, and
// the retry guard still sees an uncommitted response.
//
// Writes keep their order because every commit and content write holds wmu;
// state (committed/closed/buffer) is guarded by mu, and the lock order is always
// wmu -> mu. The gate writes only to the writer it replaced, so the existing
// response-plugin wrapper chain stays in place.
type StreamCommitGate struct {
	wmu sync.Mutex
	mu  sync.Mutex

	raw       gin.ResponseWriter
	installed *streamCommitWriter
	holding   bool
	committed bool
	closed    bool
	buffered  []byte
	maxBytes  int
	budget    StreamWriteBudget
	writeErr  error

	timer      *time.Timer
	timerDone  chan struct{}
	finishOnce sync.Once
}

const streamCommitGateContextKey = "stream_commit_gate"

// upstreamRetryAfterContextKey carries the upstream's Retry-After header value
// from the rejection point to the relay loop, which decides how long a channel
// stays busy. It is request-scoped: nothing is cached or shared across users.
const upstreamRetryAfterContextKey = "upstream_retry_after"

// StoreUpstreamRetryAfter records the upstream Retry-After header of a rejected
// attempt. It is called where the upstream response is still in hand; the relay
// loop reads it back when the channel is marked busy.
func StoreUpstreamRetryAfter(c *gin.Context, value string) {
	if c == nil || value == "" {
		return
	}
	c.Set(upstreamRetryAfterContextKey, value)
}

// UpstreamRetryAfter returns the recorded upstream Retry-After header, if any.
func UpstreamRetryAfter(c *gin.Context) string {
	if c == nil {
		return ""
	}
	return c.GetString(upstreamRetryAfterContextKey)
}

// ClearUpstreamRetryAfter drops a previous attempt's Retry-After so one
// channel's backoff is never applied to the next channel.
func ClearUpstreamRetryAfter(c *gin.Context) {
	if c == nil {
		return
	}
	c.Set(upstreamRetryAfterContextKey, "")
}

// DefaultStreamCommitHoldBudget bounds how long framing may be withheld when no
// upstream first-byte timeout is configured for the channel.
const (
	DefaultStreamCommitHoldBudget = 30 * time.Second
	streamCommitMaxBytes          = 64 * 1024
)

// NewStreamCommitGate prepares a gate for one attempt.
func NewStreamCommitGate(writer gin.ResponseWriter, holdBudget time.Duration, maxBytes int) *StreamCommitGate {
	if holdBudget <= 0 {
		holdBudget = DefaultStreamCommitHoldBudget
	}
	gate := &StreamCommitGate{raw: writer, maxBytes: maxBytes, timerDone: make(chan struct{})}
	gate.timer = time.AfterFunc(holdBudget, func() {
		defer close(gate.timerDone)
		_ = gate.Commit()
	})
	return gate
}

// InstallStreamCommitGate puts a gate in front of the real writer for this
// attempt and makes it discoverable by the response handler.
func InstallStreamCommitGate(c *gin.Context, holdBudget time.Duration) *StreamCommitGate {
	applyStreamHeaders(c)
	gate := NewStreamCommitGate(c.Writer, holdBudget, streamCommitMaxBytes)
	gate.Install(c)
	return gate
}

// StreamCommitGateFrom returns the gate installed for this attempt, if any.
func StreamCommitGateFrom(c *gin.Context) *StreamCommitGate {
	value, ok := c.Get(streamCommitGateContextKey)
	if !ok {
		return nil
	}
	gate, _ := value.(*StreamCommitGate)
	return gate
}

// Install makes the gate the response writer of this attempt.
func (g *StreamCommitGate) Install(c *gin.Context) {
	applyStreamHeaders(c)
	g.wmu.Lock()
	defer g.wmu.Unlock()
	g.mu.Lock()
	g.raw = c.Writer
	writer := &streamCommitWriter{ResponseWriter: c.Writer, gate: g}
	g.installed = writer
	g.mu.Unlock()
	c.Set(streamCommitGateContextKey, g)
	c.Writer = writer
}

// Hold keeps buffering until the attempt produces usable output or the hold
// budget elapses. Only handlers that understand the protocol call it.
func (g *StreamCommitGate) Hold() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.holding = true
}

// Committed reports whether output has been released to the client.
func (g *StreamCommitGate) Committed() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.committed
}

// Commit releases the buffered bytes in order, then passes later writes through.
func (g *StreamCommitGate) Commit() error {
	g.wmu.Lock()
	defer g.wmu.Unlock()
	if g.writeErr != nil {
		return g.writeErr
	}
	buffered, raw := g.markCommitted()
	if len(buffered) != 0 && raw != nil {
		_, g.writeErr = g.writeFrame(raw, buffered)
	}
	return g.writeErr
}

func (g *StreamCommitGate) markCommitted() ([]byte, gin.ResponseWriter) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.committed || g.closed {
		return nil, nil
	}
	g.committed = true
	buffered := g.buffered
	g.buffered = nil
	return buffered, g.raw
}

// writeFrame is called under wmu, including by the commit timer. A gate also
// works without a request envelope, so it owns a bounded write budget too.
func (g *StreamCommitGate) writeFrame(raw gin.ResponseWriter, frame []byte) (int, error) {
	_ = http.NewResponseController(raw).SetWriteDeadline(g.budget.Deadline(time.Now(), len(frame)))
	n, err := raw.Write(frame)
	if err == nil {
		err = flushStreamWriter(raw)
	}
	return n, err
}

// Discard drops everything buffered without writing it.
func (g *StreamCommitGate) Discard() {
	g.wmu.Lock()
	defer g.wmu.Unlock()
	g.mu.Lock()
	defer g.mu.Unlock()
	g.closed = true
	g.buffered = nil
}

// Finish stops and joins the commit timer, then restores the real writer.
func (g *StreamCommitGate) Finish(c *gin.Context) {
	g.finishOnce.Do(func() {
		if g.timer != nil && !g.timer.Stop() {
			<-g.timerDone
		}
		g.wmu.Lock()
		g.mu.Lock()
		g.closed = true
		g.buffered = nil
		raw := g.raw
		writer := g.installed
		g.mu.Unlock()
		g.wmu.Unlock()

		if current, ok := c.Get(streamCommitGateContextKey); ok && current == g {
			c.Set(streamCommitGateContextKey, nil)
		}
		// The attempt gate is above Reply. Remove it before Reply finishes so
		// the plugin can release its final buffered output to the same sink.
		detachWriter(c, writer, raw)
	})
}

// writeContent is the only content path: it buffers while holding, otherwise
// commits and writes the frame in one ordered step.
func (g *StreamCommitGate) writeContent(p []byte) (int, error) {
	g.wmu.Lock()
	defer g.wmu.Unlock()

	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return len(p), nil
	}
	if g.writeErr != nil {
		g.mu.Unlock()
		return 0, g.writeErr
	}
	if g.committed {
		raw := g.raw
		g.mu.Unlock()
		n, err := g.writeFrame(raw, p)
		g.writeErr = err
		return n, g.writeErr
	}
	if g.holding && len(g.buffered)+len(p) <= g.maxBytes {
		g.buffered = append(g.buffered, p...)
		g.mu.Unlock()
		return len(p), nil
	}
	raw := g.raw
	buffered := g.buffered
	g.buffered = nil
	g.committed = true
	g.mu.Unlock()

	if len(buffered) != 0 {
		if _, err := g.writeFrame(raw, buffered); err != nil {
			g.writeErr = err
			return 0, err
		}
	}
	n, err := g.writeFrame(raw, p)
	g.writeErr = err
	return n, err
}

// streamCommitWriter routes one attempt's writes through the gate.
type streamCommitWriter struct {
	gin.ResponseWriter
	gate *StreamCommitGate
}

func (w *streamCommitWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *streamCommitWriter) Write(p []byte) (int, error) {
	return w.gate.writeContent(p)
}

func (w *streamCommitWriter) WriteString(s string) (int, error) {
	return w.gate.writeContent([]byte(s))
}

// Written reports the gate's commit state: buffered bytes and keepalive
// comments were never a delivered response.
func (w *streamCommitWriter) Written() bool {
	return w.gate.Committed()
}

func (w *streamCommitWriter) WriteHeader(code int) {
	w.gate.wmu.Lock()
	defer w.gate.wmu.Unlock()
	w.ResponseWriter.WriteHeader(code)
}

func (w *streamCommitWriter) Status() int {
	w.gate.wmu.Lock()
	defer w.gate.wmu.Unlock()
	return w.ResponseWriter.Status()
}

func (w *streamCommitWriter) Size() int {
	w.gate.wmu.Lock()
	defer w.gate.wmu.Unlock()
	return w.ResponseWriter.Size()
}

// WriteHeaderNow is a no-op before the commit: committing the status is what
// would make the attempt non-retryable.
func (w *streamCommitWriter) WriteHeaderNow() {
	w.gate.wmu.Lock()
	defer w.gate.wmu.Unlock()
	if w.gate.Committed() {
		_ = http.NewResponseController(w.ResponseWriter).SetWriteDeadline(w.gate.budget.Deadline(time.Now(), 0))
		w.ResponseWriter.WriteHeaderNow()
	}
}

func (w *streamCommitWriter) Flush() {
	_ = w.FlushError()
}

func (w *streamCommitWriter) FlushError() error {
	w.gate.wmu.Lock()
	defer w.gate.wmu.Unlock()
	if w.gate.writeErr != nil {
		return w.gate.writeErr
	}
	if w.gate.Committed() {
		_ = http.NewResponseController(w.ResponseWriter).SetWriteDeadline(w.gate.budget.Deadline(time.Now(), 0))
		w.gate.writeErr = flushStreamWriter(w.ResponseWriter)
	}
	return w.gate.writeErr
}
