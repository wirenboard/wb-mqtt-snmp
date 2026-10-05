package mqtt_snmp

import (
	"fmt"
	"math"
	"net"
	"net/netip"
	"strings"
	"time"

	"github.com/gosnmp/gosnmp"
	"github.com/wirenboard/wbgong"
)

// SnmpInterface is a minimal SNMP interface
// We need it to create fake SNMP driver for testing.
// gosnmp.GoSNMP implements this interface
type SnmpInterface interface {
	Get(oids []string) (*gosnmp.SnmpPacket, error)
	Close() error
}

// SnmpFactory is an SNMP interface factory type
type SnmpFactory func(config *DeviceConfig, debug bool) (SnmpInterface, error)

// NewGoSNMP configures and connects a per-device SNMP session.
func NewGoSNMP(config *DeviceConfig, debug bool) (SnmpInterface, error) {
	client, err := newGoSNMPConfig(config, debug)
	if err != nil {
		return nil, err
	}
	if err := client.Connect(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("can't connect to %s: %w", config.Address, err)
	}
	return client, nil
}

func newGoSNMPConfig(config *DeviceConfig, debug bool) (*gosnmp.GoSNMP, error) {
	target, port, err := snmpAddress(config.Address)
	if err != nil {
		return nil, err
	}
	client := &gosnmp.GoSNMP{
		Target:    target,
		Port:      port,
		Community: config.Community,
		Version:   config.SnmpVersion,
		Timeout:   time.Duration(config.SnmpTimeout) * time.Second,
		// Polling supplies the next attempt; preserve the old single-request timeout.
		Retries: 0,
	}
	if debug {
		client.Logger = gosnmp.NewLogger(wbgong.Debug)
	}
	if config.SnmpVersion == gosnmp.Version3 {
		flags, security, err := config.SnmpV3.securityParameters()
		if err != nil {
			return nil, err
		}
		client.MsgFlags = flags
		client.SecurityModel = gosnmp.UserSecurityModel
		client.SecurityParameters = security
		client.ContextName = config.SnmpV3.ContextName
	}
	return client, nil
}

// Keep the address[:port] configuration syntax.
func snmpAddress(address string) (host string, port uint16, err error) {
	// A bare or bracketed IPv6 literal has no port.
	literal := address
	if strings.HasPrefix(address, "[") && strings.HasSuffix(address, "]") {
		literal = address[1 : len(address)-1]
	}
	if ip, parseErr := netip.ParseAddr(literal); parseErr == nil && ip.Is6() {
		return literal, 161, nil
	}
	if !strings.Contains(address, ":") {
		if address != "" && !strings.ContainsAny(address, "[]") {
			return address, 161, nil
		}
		return "", 0, fmt.Errorf("invalid SNMP address: %s", address)
	}
	host, service, err := net.SplitHostPort(address)
	if err != nil {
		return "", 0, fmt.Errorf("invalid SNMP address: %w", err)
	}
	if host == "" {
		return "", 0, fmt.Errorf("invalid SNMP address %s: empty host", address)
	}
	n, err := net.LookupPort("udp", service)
	if err != nil {
		return "", 0, fmt.Errorf("invalid SNMP address %s: %w", address, err)
	}
	if n < 1 || n > math.MaxUint16 {
		return "", 0, fmt.Errorf("invalid SNMP address %s: port must be 1-65535", address)
	}
	return host, uint16(n), nil
}
