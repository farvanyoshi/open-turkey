// This file implements a tiny DNS client used by the firewall layer.
//
// WHY NOT net.LookupHost?
// -----------------------
// The firewall needs the REAL IP addresses of a blocked domain so it can drop
// packets to them. But by the time the firewall runs, the /etc/hosts layer has
// already mapped the domain to 0.0.0.0 — so the ordinary system resolver
// (net.LookupHost) and even a local stub resolver like systemd-resolved would
// hand back 0.0.0.0, and the firewall would "block" a useless address.
//
// To get the true addresses we query PUBLIC recursive resolvers directly over
// UDP port 53, parsing the raw DNS response ourselves. This bypasses
// /etc/hosts and any local stub entirely. Port 53 to these resolvers stays
// open (we only drop their port 443 to stop DNS-over-HTTPS), so the queries
// succeed while blocking remains effective.
//
// If every resolver is unreachable (e.g. the machine is offline) we simply
// return no addresses: the firewall's IP rules are skipped, and the /etc/hosts
// and browser-policy layers continue to do their job.
package blocker

import (
	"encoding/binary"
	"errors"
	"math/rand"
	"net"
	"strings"
	"time"
)

// errInvalidLabel is returned when a domain label is empty or too long to
// encode in a DNS query.
var errInvalidLabel = errors.New("invalid DNS label")

// publicResolvers are queried, in order, for the real IPs of a domain. We stop
// at the first one that answers. These are well-known recursive resolvers that
// have no knowledge of the local /etc/hosts file.
var publicResolvers = []string{
	"1.1.1.1:53", // Cloudflare
	"8.8.8.8:53", // Google
	"9.9.9.9:53", // Quad9
}

// DNS record type constants.
const (
	dnsTypeA    = 1  // IPv4 address
	dnsTypeAAAA = 28 // IPv6 address
)

// resolveIPs returns the real IPv4 and IPv6 addresses of a domain, bypassing
// /etc/hosts. It returns the first resolver's answer; if none respond, both
// slices are empty.
func resolveIPs(domain string) (ipv4 []string, ipv6 []string) {
	for _, server := range publicResolvers {
		v4 := queryDNS(server, domain, dnsTypeA)
		v6 := queryDNS(server, domain, dnsTypeAAAA)
		if len(v4) > 0 || len(v6) > 0 {
			return v4, v6
		}
	}
	return nil, nil
}

// queryDNS sends a single UDP DNS query for one record type to one server and
// returns the addresses found. Any error (timeout, malformed response, etc.)
// yields an empty slice — resolution is best-effort.
func queryDNS(server, domain string, qtype uint16) []string {
	query, err := buildQuery(domain, qtype)
	if err != nil {
		return nil
	}

	conn, err := net.DialTimeout("udp", server, 3*time.Second)
	if err != nil {
		return nil
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))

	if _, err := conn.Write(query); err != nil {
		return nil
	}

	resp := make([]byte, 1500)
	n, err := conn.Read(resp)
	if err != nil {
		return nil
	}

	return parseResponse(resp[:n], qtype)
}

// buildQuery assembles a minimal DNS query packet: a 12-byte header with the
// recursion-desired flag, followed by a single question section.
func buildQuery(domain string, qtype uint16) ([]byte, error) {
	var b []byte

	// Header: random ID, flags 0x0100 (standard query, recursion desired),
	// QDCOUNT=1, ANCOUNT=NSCOUNT=ARCOUNT=0.
	id := uint16(rand.Intn(0xffff))
	header := make([]byte, 12)
	binary.BigEndian.PutUint16(header[0:2], id)
	binary.BigEndian.PutUint16(header[2:4], 0x0100)
	binary.BigEndian.PutUint16(header[4:6], 1)
	b = append(b, header...)

	// Question: QNAME as length-prefixed labels, then QTYPE and QCLASS (IN=1).
	for _, label := range strings.Split(domain, ".") {
		if len(label) == 0 || len(label) > 63 {
			return nil, errInvalidLabel
		}
		b = append(b, byte(len(label)))
		b = append(b, label...)
	}
	b = append(b, 0) // root label terminator

	qt := make([]byte, 4)
	binary.BigEndian.PutUint16(qt[0:2], qtype)
	binary.BigEndian.PutUint16(qt[2:4], 1) // class IN
	b = append(b, qt...)

	return b, nil
}

// parseResponse walks a DNS response and extracts the addresses of the
// requested record type. It performs strict bounds checking so a malformed or
// hostile packet can never cause an out-of-range panic.
func parseResponse(msg []byte, qtype uint16) []string {
	if len(msg) < 12 {
		return nil
	}

	qdCount := int(binary.BigEndian.Uint16(msg[4:6]))
	anCount := int(binary.BigEndian.Uint16(msg[6:8]))

	off := 12

	// Skip the question section.
	for i := 0; i < qdCount; i++ {
		off = skipName(msg, off)
		if off < 0 || off+4 > len(msg) {
			return nil
		}
		off += 4 // QTYPE + QCLASS
	}

	var ips []string
	for i := 0; i < anCount; i++ {
		off = skipName(msg, off)
		if off < 0 || off+10 > len(msg) {
			return nil
		}
		recType := binary.BigEndian.Uint16(msg[off : off+2])
		off += 8 // TYPE(2) + CLASS(2) + TTL(4)
		rdLen := int(binary.BigEndian.Uint16(msg[off : off+2]))
		off += 2
		if off+rdLen > len(msg) {
			return nil
		}

		if recType == qtype {
			if qtype == dnsTypeA && rdLen == 4 {
				ips = append(ips, net.IP(msg[off:off+4]).String())
			} else if qtype == dnsTypeAAAA && rdLen == 16 {
				ips = append(ips, net.IP(msg[off:off+16]).String())
			}
		}
		off += rdLen
	}

	return ips
}

// skipName advances past a DNS name at offset off, handling both the
// length-prefixed label form and compression pointers (0xC0). It returns the
// offset just after the name, or -1 if the name is malformed.
func skipName(msg []byte, off int) int {
	for {
		if off >= len(msg) {
			return -1
		}
		length := int(msg[off])
		if length == 0 {
			// Root label: name ends here.
			return off + 1
		}
		if length&0xC0 == 0xC0 {
			// Compression pointer: two bytes, and the name ends after them.
			if off+2 > len(msg) {
				return -1
			}
			return off + 2
		}
		// Ordinary label: skip the length byte plus the label bytes.
		off += 1 + length
	}
}
