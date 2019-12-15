package mqttsnmp

import (
	"time"

	"github.com/contactless/wbgong"
)

// NewSnmpDriver creates snmp driver
func NewSnmpDriver(config *DaemonConfig, driver wbgong.DeviceDriver) (*SnmpModel, error) {
	return newSnmpModel(newGoSNMP, config, time.Now(), driver)
}
