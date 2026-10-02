package mqtt_snmp

import (
	"github.com/contactless/wbgo"
	"time"
)

const (
	DRIVER_CLIENT_ID = "snmp"
)

func NewSnmpDriver(config *DaemonConfig, broker string) (*wbgo.Driver, error) {
	model, err := NewSnmpModel(NewGoSNMP, config, time.Now())
	if err != nil {
		return nil, err
	}

	driver := wbgo.NewDriver(model, wbgo.NewPahoMQTTClient(broker, DRIVER_CLIENT_ID, false))
	return driver, nil
}
