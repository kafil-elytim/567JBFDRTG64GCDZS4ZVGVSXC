package main

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

const registryURL = "https://ip-registry.lor-lord20.workers.dev/register"

// Ultra-fast reverse proxy for LiteRouter API
// Zero external dependencies — pure Go stdlib
// Target overhead: <1ms per request

const targetHost = "api.literouter.com"
const targetScheme = "https"

var transport = &http.Transport{
	// Connection pooling — reuse connections aggressively
	MaxIdleConns:        200,
	MaxIdleConnsPerHost: 200,
	MaxConnsPerHost:     0, // unlimited
	IdleConnTimeout:     120 * time.Second,

	// Fast DNS + TLS
	TLSClientConfig: &tls.Config{
		InsecureSkipVerify: false,
		MinVersion:         tls.VersionTLS12,
	},
	TLSHandshakeTimeout: 5 * time.Second,

	// Fast dialer
	DialContext: (&net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 60 * time.Second,
	}).DialContext,

	// Compression
	DisableCompression: false,

	// Buffers
	WriteBufferSize:    64 * 1024,
	ReadBufferSize:     64 * 1024,
	ForceAttemptHTTP2:  true,
	ResponseHeaderTimeout: 55 * time.Second,
}

var client = &http.Client{
	Transport: transport,
	Timeout:   60 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

func proxyHandler(w http.ResponseWriter, r *http.Request) {
	// Build target URL
	targetURL := targetScheme + "://" + targetHost + r.URL.RequestURI()

	// Create proxied request
	proxyReq, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL, r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte(`{"error":"proxy_request_failed"}`))
		return
	}

	// Copy all headers from original request
	for key, values := range r.Header {
		for _, v := range values {
			proxyReq.Header.Add(key, v)
		}
	}

	// Override host header
	proxyReq.Host = targetHost
	proxyReq.Header.Set("Host", targetHost)

	// Remove headers that reveal proxy
	proxyReq.Header.Del("X-Forwarded-For")
	proxyReq.Header.Del("X-Real-IP")
	proxyReq.Header.Del("CF-Connecting-IP")
	proxyReq.Header.Del("X-Forwarded-Proto")
	proxyReq.Header.Del("X-Forwarded-Host")

	// Send request to target
	resp, err := client.Do(proxyReq)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte(`{"error":"upstream_failed"}`))
		return
	}
	defer resp.Body.Close()

	// Copy response headers
	for key, values := range resp.Header {
		for _, v := range values {
			w.Header().Add(key, v)
		}
	}

	// Send status code + body
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	w.Write([]byte(`{"status":"ok"}`))
}

func ipHandler(w http.ResponseWriter, r *http.Request) {
	resp, err := http.Get("https://api.ipify.org")
	if err != nil {
		w.Write([]byte(`{"error":"` + err.Error() + `"}`))
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	renderURL := os.Getenv("RENDER_EXTERNAL_URL")
	if renderURL == "" {
		renderURL = os.Getenv("RENDER_SERVICE_URL")
	}
	if renderURL == "" {
		host := r.Host
		renderURL = "https://" + host
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"egress_ip":"` + string(body) + `","render_url":"` + renderURL + `"}`))
}

func selfRegister() {
	// Get egress IP
	resp, err := http.Get("https://api.ipify.org")
	if err != nil {
		fmt.Println("[register] failed to get IP:", err)
		return
	}
	defer resp.Body.Close()
	ipBytes, _ := io.ReadAll(resp.Body)
	ip := strings.TrimSpace(string(ipBytes))

	// Get render URL
	renderURL := os.Getenv("RENDER_EXTERNAL_URL")
	if renderURL == "" {
		renderURL = os.Getenv("RENDER_SERVICE_URL")
	}

	// Send to registry
	payload := fmt.Sprintf(`{"ip":"%s","render_url":"%s"}`, ip, renderURL)
	r, err := http.Post(registryURL, "application/json", bytes.NewBufferString(payload))
	if err != nil {
		fmt.Println("[register] failed to register:", err)
		return
	}
	defer r.Body.Close()
	body, _ := io.ReadAll(r.Body)
	fmt.Printf("[register] IP=%s URL=%s Response=%s\n", ip, renderURL, string(body))
}

func main() {
	// Auto-register on startup
	go selfRegister()

	port := os.Getenv("PORT")
	if port == "" {
		port = "10000"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", healthHandler)
	mux.HandleFunc("/ip", ipHandler)
	mux.HandleFunc("/", proxyHandler)

	server := &http.Server{
		Addr:         ":" + port,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	fmt.Println("[proxy] starting on port", port)
	server.ListenAndServe()
}
