package httpclient

import (
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"labnana2api/internal/config"
	"labnana2api/internal/logging"
)

func New(cfg config.Config, logger *logging.Logger) *http.Client {
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}

	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   8 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		MaxIdleConnsPerHost:   10,
	}

	if proxyURL := strings.TrimSpace(cfg.Proxy); proxyURL != "" {
		parsed, err := url.Parse(proxyURL)
		if err == nil {
			transport.Proxy = http.ProxyURL(parsed)
			logger.Info("http client proxy enabled: %s", proxyURL)
		} else {
			logger.Warn("invalid proxy url %q: %v", proxyURL, err)
		}
	}

	noProxyHosts := config.NormalizeNoProxyHosts(cfg.NoProxy)
	baseProxy := transport.Proxy
	transport.Proxy = func(req *http.Request) (*url.URL, error) {
		if shouldBypassProxy(req, noProxyHosts) {
			return nil, nil
		}
		if baseProxy == nil {
			return nil, nil
		}
		return baseProxy(req)
	}

	return &http.Client{
		Transport: transport,
		Timeout:   time.Duration(cfg.Labnana.Timeout) * time.Second,
	}
}

func NewDirect(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}

	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   8 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		MaxIdleConnsPerHost:   10,
	}

	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}
}

func shouldBypassProxy(req *http.Request, hosts []string) bool {
	if req == nil || req.URL == nil {
		return false
	}
	host := strings.ToLower(strings.TrimSpace(req.URL.Hostname()))
	if host == "" {
		return false
	}
	for _, candidate := range hosts {
		candidate = strings.ToLower(strings.TrimSpace(candidate))
		if candidate == "" {
			continue
		}
		if host == candidate || strings.HasSuffix(host, "."+candidate) {
			return true
		}
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
