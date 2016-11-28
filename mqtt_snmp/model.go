package mqtt_snmp

import (
	"github.com/alouca/gosnmp"
	"github.com/contactless/wbgo"
)

// SNMP device object
type snmpDevice struct {
	wbgo.DeviceBase

	// device configuration right from config tree
	Config *DeviceConfig

	// SNMP connection
	snmp *gosnmp.GoSNMP
}

// Create new SNMP device instance from config tree
func newSnmpDevice(config *DeviceConfig) (device *snmpDevice, err error) {
	err = nil

	snmp, err := gosnmp.NewGoSNMP(config.Address, config.Community, config.SnmpVersion, int64(config.SnmpTimeout))
	if err != nil {
		return
	}

	device = &snmpDevice{
		DeviceBase: wbgo.DeviceBase{DevName: config.Id, DevTitle: config.Name},
		snmp:       snmp,
	}

	return
}

// TODO: receive values from MQTT and send it to SNMP?
func (d *snmpDevice) AcceptValue(name, value string)         {}
func (d *snmpDevice) AcceptOnValues(name, value string) bool { return false }
func (d *snmpDevice) IsVirtual() bool                        { return false }

// Reader worker
// Receives poll query, perform SNMP transaction and
// send result (or error) to publisher worker
func PollWorker(id int, req <-chan PollQuery, res chan PollResult, err chan PollError, exit <-chan struct{}) {
	for {
		select {
		case r := <-req:
			_ = r
			// process query
		case <-exit:
			break
		}
	}
}

// Publisher worker
// Receives new values from Reader workers

// SNMP device model
type SnmpModel struct {
	wbgo.ModelBase
	config *DaemonConfig
}

// Start model
func (m *SnmpModel) Start() {

}

func (m *SnmpModel) Poll() {

}
