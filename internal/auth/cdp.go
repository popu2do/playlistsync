package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

var (
	cdpPort           = DefaultCDPPort
	cdpStartupTimeout = 10 * time.Second

	// Direct loopback HTTP client that never routes localhost requests through any proxy
	directCDPClient = &http.Client{
		Transport: &http.Transport{
			Proxy: nil,
		},
		Timeout: 2 * time.Second,
	}

	cdpDialer = websocket.Dialer{
		Proxy:            nil,
		HandshakeTimeout: 2 * time.Second,
	}
)

// SetCDPStartupTimeoutForTesting overrides startup deadline for unit tests
func SetCDPStartupTimeoutForTesting(d time.Duration) func() {
	orig := cdpStartupTimeout
	cdpStartupTimeout = d
	return func() {
		cdpStartupTimeout = orig
	}
}

type cdpTarget struct {
	ID                   string `json:"id"`
	URL                  string `json:"url"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

type cdpCookie struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type cdpResponse[T any] struct {
	ID     int `json:"id"`
	Result T   `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type cdpCookiesResult struct {
	Cookies []cdpCookie `json:"cookies"`
}

// getAvailablePort checks if preferredPort is free; if not, finds an ephemeral free port
func getAvailablePort(preferredPort int) int {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", preferredPort))
	if err == nil {
		_ = ln.Close()
		return preferredPort
	}
	ln, err = net.Listen("tcp", "127.0.0.1:0")
	if err == nil {
		defer ln.Close()
		return ln.Addr().(*net.TCPAddr).Port
	}
	return preferredPort
}

// getCDPPort returns the configured CDP port, allocating an available port if using default
func getCDPPort() int {
	if cdpPort != DefaultCDPPort {
		return cdpPort
	}
	return getAvailablePort(DefaultCDPPort)
}

func fetchCDPTargets(port int) ([]cdpTarget, error) {
	resp, err := directCDPClient.Get(fmt.Sprintf("http://127.0.0.1:%d/json/list", port))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var targets []cdpTarget
	if err := json.NewDecoder(resp.Body).Decode(&targets); err != nil {
		return nil, err
	}
	return targets, nil
}

func extractPageID(wsURL string) string {
	if wsURL == "" {
		return ""
	}
	idx := strings.LastIndex(wsURL, "/")
	if idx >= 0 && idx+1 < len(wsURL) {
		return wsURL[idx+1:]
	}
	return ""
}

// cdpCall provides unified websocket JSON-RPC execution for CDP commands
func cdpCall[T any](ctx context.Context, port int, pageID string, id int, method string, params any) (T, error) {
	var zero T
	wsURL := fmt.Sprintf("ws://127.0.0.1:%d/devtools/page/%s", port, pageID)
	conn, _, err := cdpDialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return zero, fmt.Errorf("failed to dial CDP websocket: %w", err)
	}
	defer conn.Close()

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetWriteDeadline(deadline)
		_ = conn.SetReadDeadline(deadline)
	} else {
		_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	}

	req := map[string]any{
		"id":     id,
		"method": method,
	}
	if params != nil {
		req["params"] = params
	}

	if err := conn.WriteJSON(req); err != nil {
		return zero, fmt.Errorf("failed to send %s: %w", method, err)
	}

	for {
		select {
		case <-ctx.Done():
			return zero, ctx.Err()
		default:
		}

		var resp cdpResponse[T]
		if err := conn.ReadJSON(&resp); err != nil {
			return zero, fmt.Errorf("failed to read CDP response: %w", err)
		}

		if resp.ID == id {
			if resp.Error != nil {
				return zero, fmt.Errorf("CDP %s error (code %d): %s", method, resp.Error.Code, resp.Error.Message)
			}
			return resp.Result, nil
		}
	}
}

// QueryCDPCookie checks active targets for a specific cookie name
func QueryCDPCookie(port int, targetCookie string) (string, error) {
	targets, err := fetchCDPTargets(port)
	if err != nil {
		return "", err
	}

	for _, t := range targets {
		if pageID := extractPageID(t.WebSocketDebuggerURL); pageID != "" {
			val, err := sendRawCDPGetCookies(port, pageID, targetCookie)
			if err == nil && val != "" {
				return val, nil
			}
		}
	}
	return "", nil
}

func getCDPTarget(port int, hostMatch string) (string, string) {
	targets, err := fetchCDPTargets(port)
	if err != nil {
		return "", ""
	}

	for _, t := range targets {
		if strings.Contains(t.URL, hostMatch) {
			if pageID := extractPageID(t.WebSocketDebuggerURL); pageID != "" {
				return t.URL, pageID
			}
		}
	}
	return "", ""
}

func sendRawCDPGetCookies(port int, pageID string, targetCookie string) (string, error) {
	cookies, err := fetchCDPCookies(port, pageID)
	if err != nil {
		return "", err
	}
	for _, c := range cookies {
		if c.Name == targetCookie && c.Value != "" {
			return c.Value, nil
		}
	}
	return "", nil
}

func sendRawCDPGetAllCookies(port int, pageID string) (string, error) {
	cookies, err := fetchCDPCookies(port, pageID)
	if err != nil {
		return "", err
	}
	var pairs []string
	for _, c := range cookies {
		pairs = append(pairs, fmt.Sprintf("%s=%s", c.Name, c.Value))
	}
	return strings.Join(pairs, "; "), nil
}

func fetchCDPCookies(port int, pageID string) ([]cdpCookie, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return fetchCDPCookiesWithContext(ctx, port, pageID)
}

func fetchCDPCookiesWithContext(ctx context.Context, port int, pageID string) ([]cdpCookie, error) {
	res, err := cdpCall[cdpCookiesResult](ctx, port, pageID, 1, "Network.getCookies", nil)
	if err != nil {
		return nil, err
	}
	return res.Cookies, nil
}

// closeCDPBrowser attempts to gracefully close the browser via CDP Browser.close command
func closeCDPBrowser(port int) error {
	resp, err := directCDPClient.Get(fmt.Sprintf("http://127.0.0.1:%d/json/version", port))
	if err != nil || resp == nil {
		return err
	}
	defer resp.Body.Close()

	var ver struct {
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ver); err != nil || ver.WebSocketDebuggerURL == "" {
		return err
	}

	conn, _, err := cdpDialer.Dial(ver.WebSocketDebuggerURL, nil)
	if err != nil {
		return err
	}
	defer conn.Close()

	req := map[string]any{
		"id":     999,
		"method": "Browser.close",
	}
	_ = conn.SetWriteDeadline(time.Now().Add(1 * time.Second))
	return conn.WriteJSON(req)
}
