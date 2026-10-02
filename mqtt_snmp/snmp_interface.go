package mqtt_snmp

import (
	"fmt"
	"net"
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
		Transport: "udp",
		Community: community,
		Version:   version,
		Timeout:   time.Duration(timeout) * time.Second,
		// Polling supplies the next attempt; preserve the old single-request timeout.
		Retries: 0,
		MaxOids: gosnmp.MaxOids,
	}
	if debug {
		client.Logger = gosnmp.NewLogger(wbgo.Debug)
	}
	return client, nil
}

// Keep the address[:port] configuration syntax.
func snmpAddress(address string) (string, uint16, error) {
	if !strings.Contains(address, ":") {
		if address != "" && !strings.ContainsAny(address, "[]") {
			return address, 161, nil
		}
		return "", 0, fmt.Errorf("invalid SNMP address: %s", address)
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return "", 0, fmt.Errorf("invalid SNMP address: %s", address)
	}
	n, err := net.LookupPort("udp", port)
	if err != nil || n == 0 || host == "" {
		return "", 0, fmt.Errorf("invalid SNMP address: %s", address)
	}
	return host, uint16(n), nil
}
