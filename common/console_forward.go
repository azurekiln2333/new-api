package common

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
)

const (
	consoleForwardTargetEnv         = "CONSOLE_FORWARD_MASTER_URL"
	consoleForwardSecretEnv         = "CONSOLE_FORWARD_SECRET"
	consoleForwardPeersEnv          = "CONSOLE_FORWARD_TRUSTED_CIDRS"
	consoleForwardOriginsEnv        = "CONSOLE_FORWARD_PUBLIC_ORIGINS"
	consoleForwardDialTimeoutEnv    = "CONSOLE_FORWARD_DIAL_TIMEOUT_MS"
	consoleForwardHeaderTimeoutEnv  = "CONSOLE_FORWARD_HEADER_TIMEOUT_SECONDS"
	consoleForwardRequestTimeoutEnv = "CONSOLE_FORWARD_REQUEST_TIMEOUT_SECONDS"
)

// ConsoleForwardConfig contains the immutable startup configuration for the
// authenticated console forwarding boundary. Secret bytes must never be logged.
type ConsoleForwardConfig struct {
	MasterURL            *url.URL
	Secret               []byte
	TrustedPeers         []*net.IPNet
	IngressTrustedPeers  []*net.IPNet
	PublicOrigins        map[string]struct{}
	DialTimeoutMillis    int
	HeaderTimeoutSecond  int
	RequestTimeoutSecond int
}

// ConsoleForwardSettings is loaded once during InitEnv and remains immutable.
var ConsoleForwardSettings *ConsoleForwardConfig

// ConsoleIngressIdentity is installed only after the internal peer and signed
// forwarding envelope have both been verified.
type ConsoleIngressIdentity struct {
	EntryNode string
	ClientIP  net.IP
	Scheme    string
	Host      string
	RequestID string
	PeerIP    net.IP
	Forwarded bool
}

type consoleIngressContextKey struct{}

// WithConsoleIngressIdentity attaches verified ingress provenance to a request.
func WithConsoleIngressIdentity(request *http.Request, identity ConsoleIngressIdentity) *http.Request {
	return request.WithContext(context.WithValue(request.Context(), consoleIngressContextKey{}, identity))
}

// VerifiedConsoleIngress returns signed ingress provenance, if the ingress
// middleware validated it.
func VerifiedConsoleIngress(request *http.Request) (ConsoleIngressIdentity, bool) {
	if request == nil {
		return ConsoleIngressIdentity{}, false
	}
	identity, ok := request.Context().Value(consoleIngressContextKey{}).(ConsoleIngressIdentity)
	return identity, ok
}

// EffectiveRequestScheme uses signed public scheme only after ingress proof
// validation; ordinary requests continue to use their actual TLS state.
func EffectiveRequestScheme(request *http.Request) string {
	if identity, ok := VerifiedConsoleIngress(request); ok && identity.Forwarded {
		return identity.Scheme
	}
	if request != nil && request.TLS != nil {
		return "https"
	}
	return "http"
}

// LoadConsoleForwardConfig parses forwarding configuration once at startup.
// An entirely unset configuration preserves the existing local behavior.
func LoadConsoleForwardConfig() (*ConsoleForwardConfig, error) {
	target := strings.TrimSpace(os.Getenv(consoleForwardTargetEnv))
	secret := os.Getenv(consoleForwardSecretEnv)
	peers := strings.TrimSpace(os.Getenv(consoleForwardPeersEnv))
	origins := strings.TrimSpace(os.Getenv(consoleForwardOriginsEnv))
	dialTimeout := strings.TrimSpace(os.Getenv(consoleForwardDialTimeoutEnv))
	headerTimeout := strings.TrimSpace(os.Getenv(consoleForwardHeaderTimeoutEnv))
	requestTimeout := strings.TrimSpace(os.Getenv(consoleForwardRequestTimeoutEnv))

	if target == "" && secret == "" && peers == "" && origins == "" && dialTimeout == "" && headerTimeout == "" && requestTimeout == "" {
		return nil, nil
	}
	if len(strings.TrimSpace(secret)) < 32 {
		return nil, fmt.Errorf("%s must contain at least 32 non-whitespace bytes", consoleForwardSecretEnv)
	}
	if peers == "" {
		return nil, fmt.Errorf("%s is required when console forwarding is configured", consoleForwardPeersEnv)
	}
	if origins == "" {
		return nil, fmt.Errorf("%s is required when console forwarding is configured", consoleForwardOriginsEnv)
	}
	if IsMasterNode && target != "" {
		return nil, fmt.Errorf("%s must be unset on the master node", consoleForwardTargetEnv)
	}
	if !IsMasterNode && target == "" {
		return nil, fmt.Errorf("%s is required on worker nodes", consoleForwardTargetEnv)
	}

	trustedPeers := make([]*net.IPNet, 0)
	for peer := range strings.SplitSeq(peers, ",") {
		peer = strings.TrimSpace(peer)
		if peer == "" {
			return nil, fmt.Errorf("%s contains an empty entry", consoleForwardPeersEnv)
		}
		if ip := net.ParseIP(peer); ip != nil {
			if ip.To4() != nil {
				trustedPeers = append(trustedPeers, &net.IPNet{IP: ip.To4(), Mask: net.CIDRMask(32, 32)})
			} else {
				trustedPeers = append(trustedPeers, &net.IPNet{IP: ip, Mask: net.CIDRMask(128, 128)})
			}
			continue
		}
		_, cidr, err := net.ParseCIDR(peer)
		if err != nil {
			return nil, fmt.Errorf("invalid %s entry %q", consoleForwardPeersEnv, peer)
		}
		trustedPeers = append(trustedPeers, cidr)
	}
	if len(trustedPeers) == 0 {
		return nil, fmt.Errorf("%s has no valid peer addresses", consoleForwardPeersEnv)
	}

	trustedProxyStrings, _, err := ResolveTrustedProxies(os.Getenv("TRUSTED_PROXIES"))
	if err != nil {
		return nil, fmt.Errorf("invalid TRUSTED_PROXIES: %w", err)
	}
	ingressTrustedPeers := make([]*net.IPNet, 0, len(trustedProxyStrings))
	for _, peer := range trustedProxyStrings {
		if ip := net.ParseIP(peer); ip != nil {
			bits := 128
			if ip.To4() != nil {
				ip = ip.To4()
				bits = 32
			}
			ingressTrustedPeers = append(ingressTrustedPeers, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
			continue
		}
		_, cidr, err := net.ParseCIDR(peer)
		if err != nil {
			return nil, fmt.Errorf("invalid TRUSTED_PROXIES entry %q", peer)
		}
		ingressTrustedPeers = append(ingressTrustedPeers, cidr)
	}

	publicOrigins := make(map[string]struct{})
	for origin := range strings.SplitSeq(origins, ",") {
		origin = strings.TrimSpace(origin)
		if origin == "" {
			return nil, fmt.Errorf("%s contains an empty entry", consoleForwardOriginsEnv)
		}
		normalized, err := NormalizeOrigin(origin)
		if err != nil {
			return nil, fmt.Errorf("invalid %s entry: %w", consoleForwardOriginsEnv, err)
		}
		u, _ := url.Parse(normalized)
		originIP := net.ParseIP(u.Hostname())
		loopback := u.Hostname() == "localhost" || originIP != nil && originIP.IsLoopback()
		if !strings.HasPrefix(normalized, "https://") && !loopback {
			return nil, fmt.Errorf("%s entries must use HTTPS except for loopback origins", consoleForwardOriginsEnv)
		}
		publicOrigins[normalized] = struct{}{}
	}

	config := &ConsoleForwardConfig{
		Secret:               []byte(secret),
		TrustedPeers:         trustedPeers,
		IngressTrustedPeers:  ingressTrustedPeers,
		PublicOrigins:        publicOrigins,
		DialTimeoutMillis:    2000,
		HeaderTimeoutSecond:  120,
		RequestTimeoutSecond: 300,
	}
	if target != "" {
		parsed, err := url.Parse(target)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Opaque != "" || parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") || parsed.RawPath != "" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || parsed.RawFragment != "" || strings.Contains(target, "#") {
			return nil, fmt.Errorf("%s must be an absolute HTTP(S) URL with no credentials, path, query or fragment", consoleForwardTargetEnv)
		}
		ip := net.ParseIP(parsed.Hostname())
		if ip == nil || !ip.IsPrivate() || ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() {
			return nil, fmt.Errorf("%s must use a fixed private internal IP address", consoleForwardTargetEnv)
		}
		port, err := strconv.Atoi(parsed.Port())
		if err != nil || port < 1 || port > 65535 {
			return nil, fmt.Errorf("%s must specify a valid TCP port", consoleForwardTargetEnv)
		}
		parsed.Path = ""
		parsed.RawPath = ""
		config.MasterURL = parsed
	}
	value, err := boundedConsoleTimeout(consoleForwardDialTimeoutEnv, dialTimeout, 2000, 1, 10000)
	if err != nil {
		return nil, err
	}
	config.DialTimeoutMillis = value
	value, err = boundedConsoleTimeout(consoleForwardHeaderTimeoutEnv, headerTimeout, 120, 1, 600)
	if err != nil {
		return nil, err
	}
	config.HeaderTimeoutSecond = value
	value, err = boundedConsoleTimeout(consoleForwardRequestTimeoutEnv, requestTimeout, 300, 1, 3600)
	if err != nil {
		return nil, err
	}
	config.RequestTimeoutSecond = value
	return config, nil
}

func boundedConsoleTimeout(name, raw string, defaultValue, minimum, maximum int) (int, error) {
	if raw == "" {
		return defaultValue, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < minimum || value > maximum {
		return 0, fmt.Errorf("%s must be an integer between %d and %d", name, minimum, maximum)
	}
	return value, nil
}
