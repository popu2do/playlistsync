package server

import (
	"fmt"
	"net"
)

// ListenWithRetry binds a loopback-only (127.0.0.1) TCP listener (spec 02
// §4.2 verbatim). preferredPort == 0 asks the kernel for a free port;
// preferredPort > 0 tries that port first and, on failure (EADDRINUSE), retries
// the next nine ports (preferredPort..preferredPort+9). With the default
// DefaultPort of 3080 the retry range is 3080..3089 per spec §3.3. It returns
// the bound listener and the actual port.
func ListenWithRetry(preferredPort int) (net.Listener, int, error) {
	if preferredPort == 0 {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, 0, err
		}
		return ln, ln.Addr().(*net.TCPAddr).Port, nil
	}

	for port := preferredPort; port < preferredPort+10; port++ {
		addr := fmt.Sprintf("127.0.0.1:%d", port)
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			return ln, port, nil
		}
	}
	return nil, 0, fmt.Errorf("failed to bind port in range %d..%d", preferredPort, preferredPort+9)
}
