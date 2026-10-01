package ats

import (
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"
)

// RefusePrivate is a dialer Control that refuses loopback, private and
// link-local addresses. Links come from job feeds and users, so anything that
// fetches one must not be usable to reach the server's own network. It runs on
// the address being dialled, after DNS, so a public name that resolves inward
// is refused too.
func RefusePrivate(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return fmt.Errorf("refusing to connect to the private address %s", host)
	}
	return nil
}

// publicClient fetches from hosts named only by a job link, such as an
// employer's own careers domain, and refuses private addresses.
var publicClient = &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{
	Proxy:                 nil,
	DialContext:           (&net.Dialer{Timeout: 10 * time.Second, Control: RefusePrivate}).DialContext,
	TLSHandshakeTimeout:   10 * time.Second,
	ResponseHeaderTimeout: 15 * time.Second,
}}
