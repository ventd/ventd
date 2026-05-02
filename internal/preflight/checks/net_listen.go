package checks

import "net"

// livePortFree attempts to bind the address with a non-blocking
// listener. Success means nothing else holds the port; failure (any
// error) means the port is held or the addr is malformed — either
// way, the operator must intervene.
func livePortFree(addr string) bool {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return false
	}
	_ = l.Close()
	return true
}
