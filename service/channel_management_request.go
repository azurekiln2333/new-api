package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/relaykit/dto"
)

// ChannelManagementCall describes one trusted console or background HTTP
// exchange. Callers prepare the endpoint and static headers. With admission,
// Reload may rebuild the request from current channel and key settings after
// the grant. A replacement request must have a fresh body, if any.
type ChannelManagementCall struct {
	Request    *http.Request
	Settings   dto.ChannelSettings
	ChannelID  int
	Admission  *ChannelDiagnosticAdmission
	Timeout    time.Duration
	NoRedirect bool
	Reload     func() (*http.Request, dto.ChannelSettings, error)
}

// DoChannelManagementRequest acquires at most one active lease and returns it
// with the response body. The caller must close every returned body, including
// on non-2xx responses. The caller's context and optional total deadline are
// kept even when Reload returns a request built with a different context. A
// management deadline takes precedence over the relay client's total timeout;
// the shared transport (including its connection/header timeouts) is unchanged.
func DoChannelManagementRequest(call ChannelManagementCall) (*http.Response, error) {
	if call.Request == nil {
		return nil, errors.New("invalid upstream request")
	}
	ctx := call.Request.Context()
	cancel := func() {}
	if call.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, call.Timeout)
	}
	req, settings := call.Request, call.Settings
	requestSent := false
	responseReturned := false
	var lease *ChannelCapacityLease
	defer func() {
		if !requestSent && req != nil && req.Body != nil {
			_ = req.Body.Close()
		}
		if !responseReturned {
			lease.Release()
			cancel()
		}
	}()

	if call.Admission != nil {
		for attempt := 0; ; attempt++ {
			granted, apiErr := call.Admission.Acquire(ctx, call.ChannelID, settings)
			if apiErr != nil {
				return nil, apiErr
			}
			lease = granted
			if call.Reload == nil {
				break
			}
			fresh, current, err := call.Reload()
			if fresh != req {
				if req.Body != nil {
					_ = req.Body.Close()
				}
				req = fresh
			}
			if err != nil {
				return nil, err
			}
			if req == nil {
				return nil, errors.New("invalid upstream request")
			}
			limitChanged := managementConcurrencyLimit(settings) != managementConcurrencyLimit(current)
			settings = current
			if !limitChanged || call.ChannelID == 0 {
				break
			}
			lease.Release()
			lease = nil
			if attempt == 2 {
				return nil, errors.New("channel concurrency settings changed during admission")
			}
		}
		ctx = lease.Context()
	}
	if err := ctx.Err(); err != nil {
		if lease != nil && lease.Err() != nil {
			return nil, lease.Err()
		}
		return nil, err
	}
	if req.URL == nil || req.URL.Host == "" || req.URL.User != nil || (req.URL.Scheme != "http" && req.URL.Scheme != "https") {
		return nil, errors.New("invalid upstream address")
	}
	client, err := GetHttpClientWithProxySettings(settings.Proxy, settings)
	if err != nil {
		// A proxy URL can contain credentials. Do not return its parse error.
		return nil, errors.New("Invalid channel proxy")
	}
	outbound := req.Clone(ctx)
	if host := outbound.Header.Get("Host"); host != "" {
		// net/http sends Host from the request field, not from Header.
		outbound.Host = host
		outbound.Header.Del("Host")
	}
	// Share the transport, not the client's generation-wide total timeout or
	// its mutable redirect policy. Long Ollama pulls have their own deadline.
	managementClient := *client
	if _, bounded := ctx.Deadline(); bounded {
		managementClient.Timeout = 0
	}
	if call.NoRedirect || call.Admission != nil || len(outbound.Header) != 0 || outbound.Host != outbound.URL.Host {
		// Go's standard redirect rules only strip some sensitive header names;
		// channel overrides are arbitrary and must stay on the same origin.
		origin := outbound.URL
		redirectCheck := client.CheckRedirect
		managementClient.CheckRedirect = func(next *http.Request, via []*http.Request) error {
			if call.NoRedirect || next.URL.Scheme != origin.Scheme || !strings.EqualFold(next.URL.Host, origin.Host) || next.URL.User != nil {
				return http.ErrUseLastResponse
			}
			if outbound.Host != outbound.URL.Host {
				// Absolute same-origin redirects otherwise discard a custom Host.
				next.Host = outbound.Host
			}
			if redirectCheck != nil {
				return redirectCheck(next, via)
			}
			if len(via) >= 10 {
				return errors.New("too many upstream redirects")
			}
			return nil
		}
	}

	requestSent = true
	resp, err := managementClient.Do(outbound)
	if err != nil {
		if lease != nil && lease.Err() != nil {
			return nil, lease.Err()
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &channelManagementTransportError{err: err}
	}
	success := resp.StatusCode >= 200 && resp.StatusCode < 300
	resp.Body = &channelManagementBody{
		ReadCloser: resp.Body,
		release: func() {
			lease.Release()
			cancel()
		},
		complete: func() {
			if success && ctx.Err() == nil {
				lease.MarkUpstreamAvailable()
			}
		},
	}
	responseReturned = true
	return resp, nil
}

func managementConcurrencyLimit(settings dto.ChannelSettings) int {
	if settings.MaxConcurrentRequests == nil {
		return 0
	}
	return max(0, *settings.MaxConcurrentRequests)
}

// Keep the transport error available for retry decisions without exposing URLs
// or upstream request data through the message shown to an administrator.
type channelManagementTransportError struct{ err error }

func (e *channelManagementTransportError) Error() string { return "Upstream request failed" }
func (e *channelManagementTransportError) Unwrap() error { return e.err }

type channelManagementBody struct {
	io.ReadCloser
	release  func()
	complete func()
	eof      sync.Once
	close    sync.Once
	closeErr error
}

func (b *channelManagementBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err == io.EOF {
		b.eof.Do(b.complete)
	}
	return n, err
}

func (b *channelManagementBody) Close() error {
	b.close.Do(func() {
		defer b.release()
		b.closeErr = b.ReadCloser.Close()
	})
	return b.closeErr
}
