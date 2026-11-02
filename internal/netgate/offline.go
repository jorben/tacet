package netgate

import (
	"errors"
	"net"
	"strings"
	"testing"
)

// errTestOffline refuses a request from a test binary to anything but this
// computer. Tacet's promise is that documents never leave the machine, and
// netgate is the only way out (AR-17), so this is where the tests are kept
// off the network: a test that wants a vendor serves it on loopback
// (httptest) and allowlists 127.0.0.1. Outside tests it never applies.
var errTestOffline = errors.New("NETWORK_TEST_OFFLINE: tests stay off the network; serve the host on loopback")

// offline says the process is a test binary, whose requests stay on loopback.
var offline = testing.Testing()

// refuseInTests is errTestOffline for a host other than this computer when
// the process is a test binary, otherwise nil.
func refuseInTests(host string) error {
	if offline && !loopback(host) {
		return errTestOffline
	}
	return nil
}

// loopback says host (a name or an IP, without port) is this computer, without
// looking it up: localhost, a name under .localhost, or a loopback address.
func loopback(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(strings.Trim(host, "[]"), "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
