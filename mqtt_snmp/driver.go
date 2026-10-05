package mqtt_snmp

import (
	"github.com/contactless/wbgo"
	"time"
)

// MQTT driver identifiers
const (
	DriverClientID = "snmp"
)

// NewSnmpDriver creates SNMP driver connected to MQTT broker
func NewSnmpDriver(config *DaemonConfig, broker string) (*wbgo.Driver, error) {
	model, err := NewSnmpModel(NewGoSNMP, config, time.Now())
	if err != nil {
		return nil, err
	}

	driver := wbgo.NewDriver(model, wbgo.NewPahoMQTTClient(broker, DriverClientID, false))
	return driver, nil
}
