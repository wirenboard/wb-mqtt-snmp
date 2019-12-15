package mqttsnmp

import (
	l "github.com/alouca/gologger"
	"github.com/alouca/gosnmp"
)

// snmpInterface is a minimal SNMP interface
// We need it to create fake SNMP driver for testing.
// gosnmp.GoSNMP implements this interface
type snmpInterface interface {
	Get(oid string) (*gosnmp.SnmpPacket, error)
}

// snmpFactory SNMP interface factory type
type snmpFactory func(address, community string, version gosnmp.SnmpVersion, timeout int64, debug bool) (snmpInterface, error)

// newGoSNMP is a GoSNMP wrapper
func newGoSNMP(address, community string, version gosnmp.SnmpVersion, timeout int64, debug bool) (snmpInterface, error) {
	i, e := gosnmp.NewGoSNMP(address, community, version, timeout)

	if debug {
		i.Log = l.CreateLogger(true, true)
	}

	return i, e
}
