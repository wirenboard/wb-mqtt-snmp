package mqtt_snmp

import (
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"

	"github.com/contactless/wbgo"
	"github.com/gosnmp/gosnmp"
)

// Minimal SNMP interface
// We need it to create fake SNMP driver for testing.
// gosnmp.GoSNMP implements this interface
type SnmpInterface interface {
	Get(oids []string) (*gosnmp.SnmpPacket, error)
	Close() error
}

// SNMP interface factory type
type SnmpFactory func(address, community string, version gosnmp.SnmpVersion, timeout int64, debug bool) (SnmpInterface, error)

// NewGoSNMP configures and connects a per-device SNMP session.
func NewGoSNMP(address, community string, version gosnmp.SnmpVersion, timeout int64, debug bool) (SnmpInterface, error) {
	client, err := newGoSNMPConfig(address, community, version, timeout, debug)
	if err != nil {
		return nil, err
	}
	if err := client.Connect(); err != nil {
		_ = client.Close()
		return nil, err
	}
	return client, nil
}

func newGoSNMPConfig(address, community string, version gosnmp.SnmpVersion, timeout int64, debug bool) (*gosnmp.GoSNMP, error) {
	target, port, err := snmpAddress(address)
	if err != nil {
		return nil, err
	}
	client := &gosnmp.GoSNMP{
		Target:    target,
		Port:      port,
		Community: community,
		Version:   version,
		Timeout:   time.Duration(timeout) * time.Second,
		// Polling supplies the next attempt; preserve the old single-request timeout.
		Retries: 0,
	}
	if debug {
		client.Logger = gosnmp.NewLogger(wbgo.Debug)
	}
	return client, nil
}

// Keep the address[:port] configuration syntax.
func snmpAddress(address string) (string, uint16, error) {
	// A bare or bracketed IPv6 literal has no port.
	literal := address
	if strings.HasPrefix(address, "[") && strings.HasSuffix(address, "]") {
		literal = address[1 : len(address)-1]
	}
	if ip, err := netip.ParseAddr(literal); err == nil && ip.Is6() {
		return literal, 161, nil
	}
	if !strings.Contains(address, ":") {
		if address != "" && !strings.ContainsAny(address, "[]") {
			return address, 161, nil
		}
		return "", 0, fmt.Errorf("invalid SNMP address: %s", address)
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return "", 0, fmt.Errorf("invalid SNMP address: %w", err)
	}
	if host == "" {
		return "", 0, fmt.Errorf("invalid SNMP address %s: empty host", address)
	}
	n, err := net.LookupPort("udp", port)
	if err != nil {
		return "", 0, fmt.Errorf("invalid SNMP address %s: %w", address, err)
	}
	if n == 0 {
		return "", 0, fmt.Errorf("invalid SNMP address %s: port must be 1-65535", address)
	}
	return host, uint16(n), nil
}
