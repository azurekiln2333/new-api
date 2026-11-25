package middleware

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

// The envelope headers are listed once, in wire order. Request headers are
// stripped, copied and read from this single list so a header can never be
// signed without also being stripped from public traffic.
const (
	consoleForwardVersionHeader        = "X-New-Api-Console-Version"
	consoleForwardTimestampHeader      = "X-New-Api-Console-Timestamp"
	consoleForwardNonceHeader          = "X-New-Api-Console-Nonce"
	consoleForwardEntryNodeHeader      = "X-New-Api-Console-Entry-Node"
	consoleForwardClientIPHeader       = "X-New-Api-Console-Client-IP"
	consoleForwardSchemeHeader         = "X-New-Api-Console-Scheme"
	consoleForwardHostHeader           = "X-New-Api-Console-Host"
	consoleForwardRequestIDHeader      = "X-New-Api-Console-Request-ID"
	consoleForwardContentLengthHeader  = "X-New-Api-Console-Content-Length"
	consoleForwardProofHeader          = "X-New-Api-Console-Proof"
	consoleForwardProcessingNodeHeader = "X-New-Api-Console-Processing-Node"

	consoleForwardMaxHeaderValue = 256
	consoleForwardMaxEntryNode   = 128
	consoleForwardMaxRequestID   = 128
	consoleForwardClockSkew      = 30 * time.Second
	consoleForwardNonceTTL       = 60 * time.Second
	consoleForwardMaxNonces      = 100000
)

var consoleForwardReservedHeaders = []string{
	consoleForwardVersionHeader,
	consoleForwardTimestampHeader,
	consoleForwardNonceHeader,
	consoleForwardEntryNodeHeader,
	consoleForwardClientIPHeader,
	consoleForwardSchemeHeader,
	consoleForwardHostHeader,
	consoleForwardRequestIDHeader,
	consoleForwardContentLengthHeader,
	consoleForwardProofHeader,
}

// consoleForwardStreamingRoutes are console endpoints that keep the socket open.
// They must not receive the total request deadline. Every new streaming console
// route has to be registered here with its method and exact path.
var consoleForwardStreamingRoutes = map[string]struct{}{
	http.MethodPost + " /api/channel/ollama/pull/stream": {},
	http.MethodGet + " /api/log/export":                  {},
	http.MethodGet + " /api/log/self/export":             {},
}

// consoleForwardProcessLocalRoutes need the process that created the job. They
// are forwarded normally; only a proven pre-send fallback returns 503 instead of
// attempting to read or restart the master's job on the worker. Keys are
// "METHOD /path/pattern" with ":" marking a single dynamic segment.
var consoleForwardProcessLocalRoutes = map[string]struct{}{
	http.MethodGet + " /api/channel/:id/fingerprint/:job":         {},
	http.MethodPost + " /api/channel/:id/fingerprint/:job/cancel": {},
	http.MethodPost + " /api/channel/ollama/pull/stream":           {},
}

// ConsoleIngress verifies the internal forwarding envelope before RequestId or
// any middleware that can touch Redis/SQL. Direct requests retain today's
// proxy-header behavior; only a verified envelope changes request provenance.
func ConsoleIngress() gin.HandlerFunc {
	config := common.ConsoleForwardSettings
	if config == nil {
		return func(c *gin.Context) {
			stripConsoleForwardHeaders(c.Request.Header)
			c.Next()
		}
	}
	verifier := &consoleForwardVerifier{
		config: config,
		nonces: make(map[string]time.Time),
	}
	return func(c *gin.Context) {
		peer, peerOK := consoleForwardPeerIP(c.Request)
		c.Request.Header.Del(consoleForwardProcessingNodeHeader)
		if !consoleForwardHasReservedHeaders(c.Request.Header) {
			c.Next()
			return
		}
		// An envelope that really carries our secret can never be forwarded
		// by another worker, even if a self/misrouted target is not in its
		// ingress peer set. This check only rejects; it never grants trust or
		// consumes a nonce. Public unsigned markers cannot disable forwarding.
		if !common.IsMasterNode {
			values, err := consoleForwardHeaderValues(c.Request)
			if err == nil && hmac.Equal([]byte(values.proof), []byte(consoleForwardProof(c.Request, values, config.Secret))) {
				common.SysLog(common.LogText("rejected console forwarding loop: peer=%q", c.Request.RemoteAddr))
				stripConsoleForwardHeaders(c.Request.Header)
				c.AbortWithStatus(http.StatusLoopDetected)
				return
			}
		}
		if !peerOK || !verifier.peerTrusted(peer) {
			// Public clients cannot use these names to disable ordinary worker
			// forwarding or influence master trust decisions.
			stripConsoleForwardHeaders(c.Request.Header)
			c.Next()
			return
		}

		identity, err := verifier.verify(c.Request)
		if err == nil && !common.IsMasterNode {
			// The worker loop check above would already have caught a valid
			// HMAC; this branch keeps provenance master-only by construction.
			err = errors.New("loop")
		}
		if err != nil {
			common.SysLog(common.LogText("rejected console forwarding envelope: peer=%s class=%s", peer.String(), consoleForwardErrorClass(err)))
			common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("invalid console forwarding envelope"))
			c.Abort()
			return
		}
		identity.PeerIP = append(net.IP(nil), peer...)
		identity.Forwarded = true
		c.Request = applyVerifiedConsoleIdentity(c.Request, identity)
		c.Header(consoleForwardProcessingNodeHeader, common.NodeName)
		common.SysLog(common.LogText("console ingress: entry_node=%q processing_node=%q forwarded=true request_id=%q", identity.EntryNode, common.NodeName, identity.RequestID))
		c.Next()
	}
}

// ConsoleForwarder sends only shared console API requests to the configured
// master. Relay, SPA, health-test and static paths continue locally.
func ConsoleForwarder() gin.HandlerFunc {
	config := common.ConsoleForwardSettings
	if config == nil || config.MasterURL == nil || common.IsMasterNode {
		return func(c *gin.Context) { c.Next() }
	}
	transport := newConsoleForwardTransport(config)
	proxyTemplate := &httputil.ReverseProxy{
		Transport:     transport,
		FlushInterval: -1,
		Rewrite: func(request *httputil.ProxyRequest) {
			publicHost := request.In.Host
			request.SetURL(config.MasterURL)
			request.Out.Host = publicHost
			// Rewrite otherwise drops unparsable query parameters. Forward the
			// original bytes so the proof and signature-bearing callbacks agree.
			request.Out.URL.RawQuery = request.In.URL.RawQuery
			request.Out.URL.RawPath = request.In.URL.RawPath
			request.Out.Trailer = request.In.Trailer
			stripConsoleForwardHeaders(request.Out.Header)
			stripUntrustedProxyHeaders(request.Out.Header)
			copyConsoleForwardHeaders(request.Out.Header, request.In.Header)
			// Connection-nominated headers were removed by ReverseProxy. Sign
			// those final end-to-end headers, not headers absent on the wire.
			if values, err := consoleForwardHeaderValues(request.Out); err == nil {
				request.Out.Header.Set(consoleForwardProofHeader, consoleForwardProof(request.Out, values, config.Secret))
			}
		},
	}
	return func(c *gin.Context) {
		if !shouldForwardConsoleRequest(c.Request) {
			if isConsoleAPIPath(c.Request.URL.Path) && ambiguousConsolePath(c.Request) {
				c.AbortWithStatus(http.StatusBadRequest)
				return
			}
			c.Next()
			return
		}
		identity := consoleWorkerIdentity(c)
		if identity == nil {
			c.Next()
			return
		}
		if !validConsolePublicOrigin(config, identity.Scheme, identity.Host) {
			common.SysLog(common.LogText("console forwarding skipped: public origin is not configured request_id=%s", c.GetString(common.RequestIdKey)))
			c.Next()
			return
		}
		var trace consoleForwardTrace
		outbound := c.Request.Clone(httptrace.WithClientTrace(c.Request.Context(), trace.clientTrace()))
		if outbound.URL != nil {
			outbound.URL.RawPath = c.Request.URL.RawPath
			outbound.URL.RawQuery = c.Request.URL.RawQuery
		}
		// Request trailers arrive while the server-owned body is read. Cloning
		// the map before that would strand the eventual values on the input.
		outbound.Trailer = c.Request.Trailer
		// Transport and ReverseProxy close outbound bodies even on dial failure.
		// The server owns the original body; leave it open for a safe fallback.
		if outbound.Body != nil && outbound.Body != http.NoBody {
			outbound.Body = io.NopCloser(outbound.Body)
		}
		makeConsoleRequestNonReplayable(outbound)
		if err := addConsoleForwardEnvelope(outbound, *identity, config.Secret); err != nil {
			common.SysError(common.LogText("console forwarding envelope failed request_id=%s: %v", c.GetString(common.RequestIdKey), err))
			c.Next()
			return
		}
		_, streaming := consoleForwardStreamingRoutes[c.Request.Method+" "+c.Request.URL.Path]
		if !streaming && !strings.EqualFold(c.Request.Header.Get("Upgrade"), "websocket") {
			timeoutContext, cancel := context.WithTimeout(outbound.Context(), time.Duration(config.RequestTimeoutSecond)*time.Second)
			defer cancel()
			outbound = outbound.WithContext(timeoutContext)
		}

		proxy := *proxyTemplate
		processingNode := "unknown"
		proxy.ModifyResponse = func(response *http.Response) error {
			if node := response.Header.Get(consoleForwardProcessingNodeHeader); node != "" && len(node) <= consoleForwardMaxEntryNode {
				processingNode = node
			}
			response.Header.Del(consoleForwardProcessingNodeHeader)
			return nil
		}
		streamAborted := false
		proxyErr := error(nil)
		proxy.ErrorHandler = func(_ http.ResponseWriter, _ *http.Request, err error) {
			proxyErr = err
		}
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					if recovered != http.ErrAbortHandler {
						panic(recovered)
					}
					// Do not let Gin append JSON or dump request headers after
					// a truncated response. Close HTTP/1 clients so a partial
					// SSE/download cannot look complete to the caller.
					streamAborted = true
					c.Abort()
					failureClass := "stream_abort_no_hijack"
					// Gin advertises Hijacker even over HTTP/2, where its method
					// would panic. Inspect the underlying writer's capability.
					writer := http.ResponseWriter(c.Writer)
					for {
						unwrapper, ok := writer.(interface{ Unwrap() http.ResponseWriter })
						if !ok {
							break
						}
						writer = unwrapper.Unwrap()
					}
					if hijacker, ok := writer.(http.Hijacker); ok {
						if connection, _, err := hijacker.Hijack(); err == nil {
							_ = connection.Close()
							failureClass = "stream_abort_closed"
						}
					}
					common.SysLog(common.LogText("console forwarding failed: entry_node=%q processing_node=%q forwarded=true request_id=%q method=%q route=%q class=%s", identity.EntryNode, processingNode, identity.RequestID, c.Request.Method, safeConsoleRoute(c.Request.URL.Path), failureClass))
				}
			}()
			proxy.ServeHTTP(c.Writer, outbound)
		}()
		if streamAborted {
			return
		}
		if proxyErr == nil {
			common.SysLog(common.LogText("console forwarding complete: entry_node=%q processing_node=%q forwarded=true request_id=%q method=%q route=%q status=%d", identity.EntryNode, processingNode, identity.RequestID, c.Request.Method, safeConsoleRoute(c.Request.URL.Path), c.Writer.Status()))
			c.Abort()
			return
		}
		if isFreshDialFailure(proxyErr, trace.connectionObtained()) && !c.Writer.Written() && c.Request.Context().Err() == nil {
			if consoleForwardProcessLocalRoute(c.Request.Method, c.Request.URL.Path) {
				common.SysLog(common.LogText("console forwarding refused: entry_node=%q processing_node=%q forwarded=false request_id=%q method=%q route=%q class=master_local_unavailable", identity.EntryNode, common.NodeName, identity.RequestID, c.Request.Method, safeConsoleRoute(c.Request.URL.Path)))
				common.ApiErrorStatus(c, http.StatusServiceUnavailable, common.NewMessage("console master unavailable"))
				c.Abort()
				return
			}
			// The master was never reached on this request: it is safe to run
			// the worker's own chain once from the untouched request.
			common.SysLog(common.LogText("console forwarding pre-send fallback: entry_node=%q processing_node=%q forwarded=false request_id=%q method=%q route=%q class=dial", identity.EntryNode, common.NodeName, identity.RequestID, c.Request.Method, safeConsoleRoute(c.Request.URL.Path)))
			c.Next()
			return
		}
		common.SysLog(common.LogText("console forwarding failed: entry_node=%q processing_node=%q forwarded=true request_id=%q method=%q route=%q class=post-send", identity.EntryNode, processingNode, identity.RequestID, c.Request.Method, safeConsoleRoute(c.Request.URL.Path)))
		if !c.Writer.Written() {
			common.ApiErrorStatus(c, http.StatusBadGateway, common.NewMessage("console master unavailable"))
			c.Abort()
			return
		}
		c.Abort()
	}
}

// consoleForwardTrace records any acquired connection, including a reused one.
// Once a connection was obtained, no subsequent dial error can authorize fallback.
type consoleForwardTrace struct {
	mu            sync.Mutex
	gotConnection bool
}

func (t *consoleForwardTrace) clientTrace() *httptrace.ClientTrace {
	return &httptrace.ClientTrace{
		GotConn: func(_ httptrace.GotConnInfo) {
			t.mu.Lock()
			defer t.mu.Unlock()
			t.gotConnection = true
		},
	}
}

func (t *consoleForwardTrace) connectionObtained() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.gotConnection
}

// consoleForwardVerifier verifies and consumes the internal forwarding envelope.
type consoleForwardVerifier struct {
	config    *common.ConsoleForwardConfig
	mu        sync.Mutex
	nonces    map[string]time.Time
	nextSweep time.Time
}

func (v *consoleForwardVerifier) peerTrusted(peer net.IP) bool {
	for _, network := range v.config.TrustedPeers {
		if network.Contains(peer) {
			return true
		}
	}
	return false
}

// verify returns a bounded sentinel class (never a secret) for every rejection.
func (v *consoleForwardVerifier) verify(request *http.Request) (common.ConsoleIngressIdentity, error) {
	values, err := consoleForwardHeaderValues(request)
	if err != nil {
		return common.ConsoleIngressIdentity{}, err
	}
	if !hmac.Equal([]byte(values.proof), []byte(consoleForwardProof(request, values, v.config.Secret))) {
		return common.ConsoleIngressIdentity{}, errors.New("proof")
	}
	if err := v.checkEnvelope(request, values); err != nil {
		return common.ConsoleIngressIdentity{}, err
	}
	if !v.claimNonce(values.nonce, time.Now()) {
		return common.ConsoleIngressIdentity{}, errors.New("replay")
	}
	return common.ConsoleIngressIdentity{
		EntryNode: values.entryNode,
		ClientIP:  net.ParseIP(values.clientIP),
		Scheme:    values.scheme,
		Host:      values.host,
		RequestID: values.requestID,
	}, nil
}

// checkEnvelope validates every signed field except the nonce.
func (v *consoleForwardVerifier) checkEnvelope(request *http.Request, values consoleForwardHeaders) error {
	timestamp, err := strconv.ParseInt(values.timestamp, 10, 64)
	if err != nil || time.Since(time.Unix(timestamp, 0)) > consoleForwardClockSkew || time.Until(time.Unix(timestamp, 0)) > consoleForwardClockSkew {
		return errors.New("timestamp")
	}
	contentLength, err := strconv.ParseInt(values.contentLength, 10, 64)
	if err != nil || contentLength < -1 || contentLength != request.ContentLength {
		return errors.New("content_length")
	}
	if net.ParseIP(values.clientIP) == nil {
		return errors.New("client_ip")
	}
	if values.version != "1" || len(values.nonce) > consoleForwardMaxHeaderValue || len(values.entryNode) == 0 || len(values.entryNode) > consoleForwardMaxEntryNode || len(values.requestID) == 0 || len(values.requestID) > consoleForwardMaxRequestID {
		return errors.New("bounds")
	}
	if values.scheme != "http" && values.scheme != "https" {
		return errors.New("scheme")
	}
	if !validConsolePublicOrigin(v.config, values.scheme, values.host) {
		return errors.New("origin")
	}
	if _, err := base64.RawURLEncoding.DecodeString(values.nonce); err != nil || len(values.nonce) < 20 {
		return errors.New("nonce")
	}
	return nil
}

// claimNonce atomically reserves a verified nonce until the freshness window ends.
func (v *consoleForwardVerifier) claimNonce(nonce string, now time.Time) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !now.Before(v.nextSweep) {
		for key, expiresAt := range v.nonces {
			if !expiresAt.After(now) {
				delete(v.nonces, key)
			}
		}
		v.nextSweep = now.Add(time.Second)
	}
	if _, exists := v.nonces[nonce]; exists || len(v.nonces) >= consoleForwardMaxNonces {
		return false
	}
	v.nonces[nonce] = now.Add(consoleForwardNonceTTL)
	return true
}

type consoleForwardHeaders struct {
	version       string
	timestamp     string
	nonce         string
	entryNode     string
	clientIP      string
	scheme        string
	host          string
	requestID     string
	contentLength string
	proof         string
}

func consoleForwardHeaderValues(request *http.Request) (consoleForwardHeaders, error) {
	// Absent reserved headers read as "" and are rejected by verify.
	get := func(name string, max int) (string, error) {
		values := request.Header.Values(name)
		if len(values) > 1 {
			return "", errors.New("header")
		}
		if len(values) == 0 {
			return "", nil
		}
		if len(values[0]) == 0 || len(values[0]) > max || strings.ContainsAny(values[0], "\r\n") {
			return "", errors.New("header")
		}
		return values[0], nil
	}
	var result consoleForwardHeaders
	var err error
	if result.version, err = get(consoleForwardVersionHeader, 8); err != nil {
		return result, err
	}
	if result.timestamp, err = get(consoleForwardTimestampHeader, 32); err != nil {
		return result, err
	}
	if result.nonce, err = get(consoleForwardNonceHeader, consoleForwardMaxHeaderValue); err != nil {
		return result, err
	}
	if result.entryNode, err = get(consoleForwardEntryNodeHeader, consoleForwardMaxEntryNode); err != nil {
		return result, err
	}
	if result.clientIP, err = get(consoleForwardClientIPHeader, 64); err != nil {
		return result, err
	}
	if result.scheme, err = get(consoleForwardSchemeHeader, 8); err != nil {
		return result, err
	}
	if result.host, err = get(consoleForwardHostHeader, consoleForwardMaxHeaderValue); err != nil {
		return result, err
	}
	if result.requestID, err = get(consoleForwardRequestIDHeader, consoleForwardMaxRequestID); err != nil {
		return result, err
	}
	if result.contentLength, err = get(consoleForwardContentLengthHeader, 32); err != nil {
		return result, err
	}
	if result.proof, err = get(consoleForwardProofHeader, 128); err != nil {
		return result, err
	}
	return result, nil
}

func addConsoleForwardEnvelope(request *http.Request, identity common.ConsoleIngressIdentity, secret []byte) error {
	if request == nil || request.URL == nil || len(secret) < 32 || request.Header == nil {
		return errors.New("invalid forwarding envelope input")
	}
	if len(identity.EntryNode) == 0 || len(identity.EntryNode) > consoleForwardMaxEntryNode || len(identity.RequestID) == 0 || len(identity.RequestID) > consoleForwardMaxRequestID || len(identity.Host) == 0 || len(identity.Host) > consoleForwardMaxHeaderValue || (identity.Scheme != "http" && identity.Scheme != "https") || identity.ClientIP == nil {
		return errors.New("invalid forwarding identity")
	}
	nonceBytes := make([]byte, 16)
	if _, err := rand.Read(nonceBytes); err != nil {
		return err
	}
	request.Header.Set(consoleForwardVersionHeader, "1")
	request.Header.Set(consoleForwardTimestampHeader, strconv.FormatInt(time.Now().Unix(), 10))
	request.Header.Set(consoleForwardNonceHeader, base64.RawURLEncoding.EncodeToString(nonceBytes))
	request.Header.Set(consoleForwardEntryNodeHeader, identity.EntryNode)
	request.Header.Set(consoleForwardClientIPHeader, identity.ClientIP.String())
	request.Header.Set(consoleForwardSchemeHeader, identity.Scheme)
	request.Header.Set(consoleForwardHostHeader, identity.Host)
	request.Header.Set(consoleForwardRequestIDHeader, identity.RequestID)
	request.Header.Set(consoleForwardContentLengthHeader, strconv.FormatInt(request.ContentLength, 10))
	values, err := consoleForwardHeaderValues(request)
	if err != nil {
		return err
	}
	request.Header.Set(consoleForwardProofHeader, consoleForwardProof(request, values, secret))
	return nil
}

// consoleForwardProof signs the request line, envelope metadata and hashes of
// credential/origin headers. The proof header itself is never part of the input.
func consoleForwardProof(request *http.Request, values consoleForwardHeaders, secret []byte) string {
	hashHeader := func(name string) []byte {
		sum := sha256.Sum256([]byte(strings.Join(request.Header.Values(name), "\x00")))
		return sum[:]
	}
	var canonical []byte
	appendField := func(value []byte) {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		canonical = append(canonical, length[:]...)
		canonical = append(canonical, value...)
	}
	path := request.URL.EscapedPath()
	if request.URL.RawPath != "" {
		path = request.URL.RawPath
	}
	appendField([]byte(values.version))
	appendField([]byte(values.timestamp))
	appendField([]byte(values.nonce))
	appendField([]byte(values.entryNode))
	appendField([]byte(values.clientIP))
	appendField([]byte(values.scheme))
	appendField([]byte(values.host))
	appendField([]byte(values.requestID))
	appendField([]byte(values.contentLength))
	appendField([]byte(strings.ToUpper(request.Method)))
	appendField([]byte(path))
	appendField([]byte(request.URL.RawQuery))
	appendField(hashHeader("Authorization"))
	appendField(hashHeader("Cookie"))
	appendField(hashHeader("Origin"))
	appendField(hashHeader("Referer"))
	appendField(hashHeader("Content-Type"))
	appendField(hashHeader("Content-Encoding"))
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(canonical)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func newConsoleForwardTransport(config *common.ConsoleForwardConfig) http.RoundTripper {
	base := &http.Transport{
		Proxy:                 nil,
		DisableCompression:    true,
		ForceAttemptHTTP2:     false,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   32,
		IdleConnTimeout:       45 * time.Second,
		ResponseHeaderTimeout: time.Duration(config.HeaderTimeoutSecond) * time.Second,
		TLSHandshakeTimeout:   3 * time.Second,
		DialContext: (&net.Dialer{
			Timeout:   time.Duration(config.DialTimeoutMillis) * time.Millisecond,
			KeepAlive: 30 * time.Second,
		}).DialContext,
	}
	return &consoleNoReplayTransport{base: base}
}

// consoleNoReplayTransport restores the guard after ReverseProxy cloning and
// prevents both net/http.Transport retry predicates (isReplayable and zero length).
type consoleNoReplayTransport struct{ base *http.Transport }

func (t *consoleNoReplayTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	outbound := request.Clone(request.Context())
	outbound.Trailer = request.Trailer
	makeConsoleRequestNonReplayable(outbound)
	return t.base.RoundTrip(outbound)
}

// makeConsoleRequestNonReplayable defeats both isReplayable and outgoingLength's
// zero-body retry branch in net/http.Transport. Empty bodies use explicit empty
// chunked framing so the single-use reader survives ReverseProxy, and the signed
// length (-1) matches what the master actually receives. No payload bytes change.
func makeConsoleRequestNonReplayable(request *http.Request) {
	request.GetBody = nil
	if request.Body == nil || request.Body == http.NoBody {
		request.Body = io.NopCloser(strings.NewReader(""))
	}
	if request.ContentLength <= 0 {
		request.ContentLength = -1
		request.TransferEncoding = []string{"chunked"}
		request.Header.Del("Content-Length")
	}
}

func shouldForwardConsoleRequest(request *http.Request) bool {
	path := request.URL.Path
	if !isConsoleAPIPath(path) || request.Method == http.MethodGet && path == "/api/status/test" {
		return false
	}
	return !ambiguousConsolePath(request)
}

func isConsoleAPIPath(path string) bool { return path == "/api" || strings.HasPrefix(path, "/api/") }

func ambiguousConsolePath(request *http.Request) bool {
	path := request.URL.Path
	if !isConsoleAPIPath(path) {
		return false
	}
	raw := strings.ToLower(request.URL.RawPath)
	if strings.Contains(raw, "%2f") || strings.Contains(raw, "%5c") || strings.Contains(raw, "%2e") {
		return true
	}
	for _, part := range strings.Split(path, "/") {
		if part == "." || part == ".." || strings.Contains(part, "\\") {
			return true
		}
	}
	return false
}

// consoleForwardProcessLocalRoute identifies jobs which must never fall back
// from their master process to the worker. Empty dynamic segments do not match.
func consoleForwardProcessLocalRoute(method, path string) bool {
	requestPath := strings.Trim(path, "/")
	parts := strings.Split(requestPath, "/")
	for pattern := range consoleForwardProcessLocalRoutes {
		patternMethod, patternPath, found := strings.Cut(pattern, " ")
		if !found || patternMethod != method {
			continue
		}
		patternParts := strings.Split(strings.Trim(patternPath, "/"), "/")
		if len(patternParts) != len(parts) {
			continue
		}
		matched := true
		for index, patternPart := range patternParts {
			if strings.HasPrefix(patternPart, ":") {
				matched = parts[index] != ""
			} else {
				matched = parts[index] == patternPart
			}
			if !matched {
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

func consoleWorkerIdentity(c *gin.Context) *common.ConsoleIngressIdentity {
	request := c.Request
	clientIP := net.ParseIP(c.ClientIP())
	if clientIP == nil {
		return nil
	}
	scheme := common.EffectiveRequestScheme(request)
	if peer, ok := consoleForwardPeerIP(request); ok && consoleForwardIngressPeerTrusted(peer) {
		values := request.Header.Values("X-Forwarded-Proto")
		if len(values) == 1 && (values[0] == "http" || values[0] == "https") {
			scheme = values[0]
		}
	}
	return &common.ConsoleIngressIdentity{EntryNode: common.NodeName, ClientIP: clientIP, Scheme: scheme, Host: request.Host, RequestID: c.GetString(common.RequestIdKey)}
}

func consoleForwardIngressPeerTrusted(peer net.IP) bool {
	config := common.ConsoleForwardSettings
	if config == nil {
		return false
	}
	for _, network := range config.IngressTrustedPeers {
		if network.Contains(peer) {
			return true
		}
	}
	return false
}

func validConsolePublicOrigin(config *common.ConsoleForwardConfig, scheme, host string) bool {
	if host == "" || len(host) > consoleForwardMaxHeaderValue || strings.ContainsAny(host, "\r\n\t @/?#\\") {
		return false
	}
	origin, err := common.NormalizeOrigin(scheme + "://" + host)
	if err != nil {
		return false
	}
	_, ok := config.PublicOrigins[origin]
	return ok
}

func consoleForwardPeerIP(request *http.Request) (net.IP, bool) {
	if request == nil {
		return nil, false
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(request.RemoteAddr))
	if err != nil {
		return nil, false
	}
	ip := net.ParseIP(host)
	return ip, ip != nil
}

// applyVerifiedConsoleIdentity installs the signed public provenance. The
// internal transport must leave no trace: proxy headers and the private
// envelope are dropped, and the client address becomes the signed one.
func applyVerifiedConsoleIdentity(request *http.Request, identity common.ConsoleIngressIdentity) *http.Request {
	request.RemoteAddr = net.JoinHostPort(identity.ClientIP.String(), "0")
	request.Host = identity.Host
	request.URL.Scheme = identity.Scheme
	stripConsoleForwardHeaders(request.Header)
	stripUntrustedProxyHeaders(request.Header)
	return common.WithConsoleIngressIdentity(request, identity)
}

func stripConsoleForwardHeaders(header http.Header) {
	header.Del(consoleForwardProcessingNodeHeader)
	for _, name := range consoleForwardReservedHeaders {
		header.Del(name)
	}
}

func stripUntrustedProxyHeaders(header http.Header) {
	for _, name := range []string{"X-Forwarded-For", "X-Real-IP", "Forwarded", "CF-Connecting-IP", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Forwarded-Protocol"} {
		header.Del(name)
	}
}

func copyConsoleForwardHeaders(dst, src http.Header) {
	for _, name := range consoleForwardReservedHeaders {
		for _, value := range src.Values(name) {
			dst.Add(name, value)
		}
	}
}

func consoleForwardHasReservedHeaders(header http.Header) bool {
	for _, name := range consoleForwardReservedHeaders {
		if len(header.Values(name)) > 0 {
			return true
		}
	}
	return false
}

// isFreshDialFailure permits fallback only when no connection was acquired and
// the fixed-IP dial failed. Every failure after GotConn is conservatively unsafe.
func isFreshDialFailure(err error, connectionObtained bool) bool {
	if err == nil || connectionObtained {
		return false
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		err = urlErr.Err
	}
	var opErr *net.OpError
	return errors.As(err, &opErr) && opErr.Op == "dial"
}

func consoleForwardErrorClass(err error) string {
	if err == nil {
		return "unknown"
	}
	return err.Error()
}

// safeConsoleRoute logs only static API families, never a path parameter/query.
// Unknown families collapse to /api rather than exposing caller-chosen segments.
func safeConsoleRoute(path string) string {
	if !isConsoleAPIPath(path) {
		return "/"
	}
	family, _, _ := strings.Cut(strings.TrimPrefix(path, "/api/"), "/")
	switch family {
	case "setup", "status", "uptime", "official-status", "models", "notice",
		"user-agreement", "privacy-policy", "about", "home_page_content", "pricing",
		"perf-metrics", "rankings", "verification", "reset_password", "oauth", "verify",
		"user", "subscription", "stripe", "creem", "waffo", "waffo-pancake", "option",
		"custom-oauth-provider", "performance", "ratio_sync", "plugin", "task_plugin_options",
		"request_plugin", "channel", "authz", "token", "usage", "invitation", "redemption",
		"audit", "log", "system-task", "system-info", "data", "group", "prefill_group",
		"mj", "task", "vendors", "deployments":
		return "/api/" + family
	default:
		return "/api"
	}
}
