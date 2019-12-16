package mqtt_snmp

import (
	"time"

	"github.com/contactless/wbgong"
)

const (
	DRIVER_CLIENT_ID = "snmp"
)

// NewSnmpDriver creates snmp driver
func NewSnmpDriver(config *DaemonConfig, driver wbgong.DeviceDriver) (*SnmpModel, error) {
	return NewSnmpModel(NewGoSNMP, config, time.Now(), driver)
}
