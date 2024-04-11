package netx

import (
	"net"
)

// HostPort returns whether addr includes a port number (i.e.,
// is of the form HOST:PORT).
func HasPort(addr string) bool {
	_, _, err := net.SplitHostPort(addr)
	return err == nil
}
