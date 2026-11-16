package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
)

// EffectiveUpstreamFirstByteTimeoutSeconds preserves an explicit channel zero.
func EffectiveUpstreamFirstByteTimeoutSeconds(settings dto.ChannelSettings) int {
	seconds := model.CurrentRequestPolicy().UpstreamFirstByteTimeoutSeconds
	if settings.UpstreamFirstByteTimeoutSeconds != nil {
		seconds = *settings.UpstreamFirstByteTimeoutSeconds
	}
	return min(max(seconds, 0), dto.MaxUpstreamFirstByteTimeoutSeconds)
}

type upstreamFirstByteTimeoutError struct{}

func (*upstreamFirstByteTimeoutError) Error() string {
	return "upstream first response body byte timed out"
}

func (*upstreamFirstByteTimeoutError) Timeout() bool { return true }

// RetryableError prevents AWS SDK retries within this channel. Gateway retry
// policy remains responsible for selecting another channel.
func (*upstreamFirstByteTimeoutError) RetryableError() bool { return false }

func IsUpstreamFirstByteTimeout(err error) bool {
	var timeoutErr *upstreamFirstByteTimeoutError
	return errors.As(err, &timeoutErr)
}

type firstByteTimeoutTransport struct {
	next    http.RoundTripper
	timeout time.Duration
}

func withUpstreamFirstByteTimeout(next http.RoundTripper, seconds int) http.RoundTripper {
	if seconds <= 0 {
		return next
	}
	return &firstByteTimeoutTransport{
		next:    next,
		timeout: time.Duration(min(seconds, dto.MaxUpstreamFirstByteTimeoutSeconds)) * time.Second,
	}
}

func (t *firstByteTimeoutTransport) CloseIdleConnections() {
	if transport, ok := t.next.(interface{ CloseIdleConnections() }); ok {
		transport.CloseIdleConnections()
	}
}

func (t *firstByteTimeoutTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithCancelCause(req.Context())
	timeoutErr := types.NewErrorWithStatusCode(&upstreamFirstByteTimeoutError{}, types.ErrorCodeUpstreamFirstByteTimeout, http.StatusGatewayTimeout)
	var mu sync.Mutex
	pending, expired := true, false
	timer := time.AfterFunc(t.timeout, func() {
		mu.Lock()
		defer mu.Unlock()
		if pending && req.Context().Err() == nil {
			expired = true
			pending = false
			cancel(timeoutErr)
		}
	})

	resp, err := t.next.RoundTrip(req.WithContext(ctx))
	var first [1]byte
	n := 0
	if err == nil && resp != nil && resp.StatusCode != http.StatusSwitchingProtocols && resp.Body != nil && resp.Body != http.NoBody {
		// net/http body reads observe the request context. Read synchronously so
		// no reader can outlive a failed attempt or its channel capacity lease.
		n, err = io.ReadFull(resp.Body, first[:])
		if err == io.EOF {
			err = nil
		}
	}
	mu.Lock()
	pending = false
	timer.Stop()
	timedOut := expired
	mu.Unlock()
	if timedOut {
		err = timeoutErr
	} else if req.Context().Err() != nil {
		err = req.Context().Err()
	}
	if err != nil {
		cancel(err)
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		return nil, err
	}
	if resp == nil || resp.Body == nil || resp.Body == http.NoBody {
		cancel(nil)
		return resp, nil
	}
	if n == 0 && resp.StatusCode != http.StatusSwitchingProtocols {
		_ = resp.Body.Close()
		resp.Body = http.NoBody
		cancel(nil)
		return resp, nil
	}
	body := &firstByteResponseBody{body: resp.Body, cancel: cancel, first: first[0], pending: n > 0}
	if writer, ok := resp.Body.(io.Writer); ok && resp.StatusCode == http.StatusSwitchingProtocols {
		resp.Body = &firstByteUpgradeBody{firstByteResponseBody: body, writer: writer}
	} else {
		resp.Body = body
	}
	return resp, nil
}

type firstByteResponseBody struct {
	body    io.ReadCloser
	cancel  context.CancelCauseFunc
	first   byte
	pending bool
}

func (b *firstByteResponseBody) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if b.pending {
		p[0] = b.first
		b.pending = false
		return 1, nil
	}
	n, err := b.body.Read(p)
	if err != nil {
		b.cancel(err)
	}
	return n, err
}

func (b *firstByteResponseBody) Close() error {
	b.cancel(nil)
	return b.body.Close()
}

type firstByteUpgradeBody struct {
	*firstByteResponseBody
	writer io.Writer
}

func (b *firstByteUpgradeBody) Write(p []byte) (int, error) {
	return b.writer.Write(p)
}
