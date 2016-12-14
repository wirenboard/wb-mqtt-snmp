package mqtt_snmp

import (
	"github.com/alouca/gosnmp"
)

// Minimal SNMP interface
// We need it to create fake SNMP driver for testing.
// gosnmp.GoSNMP implements this interface
type SnmpInterface interface {
	Get(oid string) (*gosnmp.SnmpPacket, error)
}

// SNMP interface factory type
// gosnmp.NewGoSNMP implements this
type SnmpFactory func(address, community string, version gosnmp.SnmpVersion, timeout int64) (SnmpInterface, error)

// GoSNMP NewGoSNMP wrapper
func NewGoSNMP(address, community string, version gosnmp.SnmpVersion, timeout int64) (SnmpInterface, error) {
	i, e := gosnmp.NewGoSNMP(address, community, version, timeout)
	return i, e
}
