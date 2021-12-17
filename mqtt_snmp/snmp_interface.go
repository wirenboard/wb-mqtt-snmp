package mqtt_snmp

import (
	l "github.com/alouca/gologger"
	"github.com/wirenboard/gosnmp"
)

// Minimal SNMP interface
// We need it to create fake SNMP driver for testing.
// gosnmp.GoSNMP implements this interface
type SnmpInterface interface {
	Get(oid string) (*gosnmp.SnmpPacket, error)
}

// SNMP interface factory type
type SnmpFactory func(address, community string, version gosnmp.SnmpVersion, timeout int64, debug bool) (SnmpInterface, error)

// GoSNMP NewGoSNMP wrapper
func NewGoSNMP(address, community string, version gosnmp.SnmpVersion, timeout int64, debug bool) (SnmpInterface, error) {
	i, e := gosnmp.NewGoSNMP(address, community, version, timeout)

	if debug {
		i.Log = l.CreateLogger(true, true)
	}

	return i, e
}
