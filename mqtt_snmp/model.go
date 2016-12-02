package mqtt_snmp

import (
	"github.com/contactless/wbgo"
)

const (
	// Size of channels buffer
	CHAN_BUFFER_SIZE = 128

	// Number of connections per time (TODO: move to config)
	NUM_WORKERS = 4
)

// SNMP device object
type SnmpDevice struct {
	wbgo.DeviceBase

	// device configuration right from config tree
	Config *DeviceConfig

	// Device cached values
	Cache map[*ChannelConfig]string

	// SNMP connection
	snmp SnmpInterface
}

// Create new SNMP device instance from config tree
func newSnmpDevice(snmpFactory SnmpFactory, config *DeviceConfig) (device *SnmpDevice, err error) {
	err = nil

	snmp, err := snmpFactory(config.Address, config.Community, config.SnmpVersion, int64(config.SnmpTimeout))
	if err != nil {
		return
	}

	device = &SnmpDevice{
		DeviceBase: wbgo.DeviceBase{DevName: config.Id, DevTitle: config.Name},
		snmp:       snmp,
		Config:     config,
		Cache:      make(map[*ChannelConfig]string),
	}

	return
}

// TODO: receive values from MQTT and send it to SNMP?
func (d *SnmpDevice) AcceptValue(name, value string)        {}
func (d *SnmpDevice) AcceptOnValue(name, value string) bool { return false }
func (d *SnmpDevice) IsVirtual() bool                       { return false }

// SNMP device model
type SnmpModel struct {
	wbgo.ModelBase
	config *DaemonConfig

	// devices list
	devices []*SnmpDevice

	// devices associated with their channels
	deviceChannelMap map[*ChannelConfig]*SnmpDevice

	// Channels to exchange data between workers and replier
	queryChannel  chan PollQuery
	resultChannel chan PollResult
	errorChannel  chan PollError
	quitChannels  []chan struct{}
}

// SNMP model constructor
func NewSnmpModel(snmpFactory SnmpFactory, config *DaemonConfig) (model *SnmpModel, err error) {
	model = &SnmpModel{
		config: config,
	}

	// init all devices from configuration
	model.devices = make([]*SnmpDevice, len(model.config.Devices))
	model.deviceChannelMap = make(map[*ChannelConfig]*SnmpDevice)
	i := 0
	for dev := range model.config.Devices {
		if model.devices[i], err = newSnmpDevice(snmpFactory, model.config.Devices[dev]); err != nil {
			wbgo.Error.Printf("can't create SNMP device: %s", err)
		}

		for ch := range model.config.Devices[dev].Channels {
			model.deviceChannelMap[model.config.Devices[dev].Channels[ch]] = model.devices[i]
		}

		i += 1
	}

	return
}

// Reader worker
// Receives poll query, perform SNMP transaction and
// send result (or error) to publisher worker
func (m *SnmpModel) PollWorker(id int, req <-chan PollQuery, res chan PollResult, err chan PollError, quit chan struct{}) {
	for {
		select {
		case r := <-req:
			_ = r
			// process query
		case <-quit:
			break
		}
	}

	close(quit)
}

// Publisher worker
// Receives new values from Reader workers
func (m *SnmpModel) PublisherWorker(data <-chan PollResult, err <-chan PollError, quit chan struct{}) {
	for {
		select {
		case d := <-data:
			// process received data
			// get device of given channel
			dev := m.deviceChannelMap[d.Channel]

			// try to get value from cache
			val, ok := dev.Cache[d.Channel]
			if !ok {
				// create value in cache and create new control in MQTT
				dev.Cache[d.Channel] = d.Data
				// TODO: read-only, max value and retain flags
				dev.Observer.OnNewControl(dev, d.Channel.Name, d.Channel.ControlType, d.Data, true, -1, true)
			} else if val != d.Data {
				// send new value only if it has been changed
				dev.Observer.OnValue(dev, d.Channel.Name, d.Data)
			}
		case e := <-err:
			_ = e
			// TODO: process error
		case <-quit:
			break
		}
	}

	close(quit)
}

// Start model
func (m *SnmpModel) Start() {
	// var err error

	// create all channels
	m.queryChannel = make(chan PollQuery, CHAN_BUFFER_SIZE)
	m.resultChannel = make(chan PollResult, CHAN_BUFFER_SIZE)
	m.errorChannel = make(chan PollError, CHAN_BUFFER_SIZE)
	m.quitChannels = make([]chan struct{}, NUM_WORKERS+1) // +1 for publisher

	for i := range m.quitChannels {
		m.quitChannels[i] = make(chan struct{})
	}

	// start workers and publisher
	for i := 0; i < NUM_WORKERS; i++ {
		go m.PollWorker(i, m.queryChannel, m.resultChannel, m.errorChannel, m.quitChannels[i])
	}
	go m.PublisherWorker(m.resultChannel, m.errorChannel, m.quitChannels[NUM_WORKERS])
}

// Poll values - means run workers and poll queue
func (m *SnmpModel) Poll() {
	// TODO: dummy!
	// Test publisher node
	m.resultChannel <- PollResult{}
}

// Stop model - send signal to terminate all workers
func (m *SnmpModel) Stop() {
	// close all data channels
	close(m.queryChannel)
	close(m.resultChannel)
	close(m.errorChannel)

	// send signals to quit to all workers
	for i := range m.quitChannels {
		m.quitChannels[i] <- struct{}{}
	}

	// wait for quit channels to close
	for i := range m.quitChannels {
		<-m.quitChannels[i]
	}
}
