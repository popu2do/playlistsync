package server

import (
	"fmt"
	"net"
	"testing"
)

// TestListenWithRetryAutoPort verifies port 0 asks the kernel for a free port.
func TestListenWithRetryAutoPort(t *testing.T) {
	ln, port, err := ListenWithRetry(0)
	if err != nil {
		t.Fatalf("ListenWithRetry(0) error: %v", err)
	}
	defer ln.Close()
	if port <= 0 || port > 65535 {
		t.Fatalf("auto port = %d, want a valid port in 1..65535", port)
	}
	if addr, ok := ln.Addr().(*net.TCPAddr); !ok || !addr.IP.IsLoopback() {
		t.Fatalf("listener addr = %v, want loopback", ln.Addr())
	}
}

// TestListenWithRetryOccupiedPort verifies an occupied preferred port falls
// back within the 3080..3089 retry range (spec §3.3).
func TestListenWithRetryOccupiedPort(t *testing.T) {
	blocker, err := net.Listen("tcp", "127.0.0.1:3080")
	if err != nil {
		t.Skipf("cannot occupy 127.0.0.1:3080 for the test: %v", err)
	}
	defer blocker.Close()

	ln, port, err := ListenWithRetry(3080)
	if err != nil {
		t.Fatalf("ListenWithRetry(3080) with 3080 occupied: %v", err)
	}
	defer ln.Close()

	if port < 3080 || port > 3089 {
		t.Fatalf("retry port = %d, want within 3080..3089", port)
	}
	if port == 3080 {
		t.Fatalf("retry port = 3080, but it was occupied")
	}
	if got := ln.Addr().(*net.TCPAddr).Port; got != port {
		t.Fatalf("listener reports port %d, want %d", got, port)
	}
}

// TestListenWithRetryCustomRange verifies the retry works from a non-default
// preferred port too (spec §4.2: preferred..preferred+9).
func TestListenWithRetryCustomRange(t *testing.T) {
	preferred := 31770
	blocker, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", preferred))
	if err != nil {
		t.Skipf("cannot occupy port %d: %v", preferred, err)
	}
	defer blocker.Close()

	ln, port, err := ListenWithRetry(preferred)
	if err != nil {
		t.Fatalf("ListenWithRetry(%d): %v", preferred, err)
	}
	defer ln.Close()
	if port <= preferred || port > preferred+9 {
		t.Fatalf("retry port = %d, want within %d..%d", port, preferred, preferred+9)
	}
}
