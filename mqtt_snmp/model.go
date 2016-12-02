package mqtt_snmp

import (
	// "fmt"
	"github.com/contactless/wbgo"
	"time"
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
	DeviceChannelMap map[*ChannelConfig]*SnmpDevice

	// Poll schedule table
	pollTable *PollTable

	// Channels to exchange data between workers and replier
	queryChannel    chan PollQuery
	resultChannel   chan PollResult
	errorChannel    chan PollError
	quitChannels    []chan struct{}
	pollDoneChannel chan struct{}
	pubDoneChannel  chan struct{}
}

// SNMP model constructor
func NewSnmpModel(snmpFactory SnmpFactory, config *DaemonConfig) (model *SnmpModel, err error) {
	err = nil

	model = &SnmpModel{
		config: config,
	}

	// init all devices from configuration
	model.devices = make([]*SnmpDevice, len(model.config.Devices))
	model.DeviceChannelMap = make(map[*ChannelConfig]*SnmpDevice)
	i := 0
	for dev := range model.config.Devices {
		if model.devices[i], err = newSnmpDevice(snmpFactory, model.config.Devices[dev]); err != nil {
			wbgo.Error.Printf("can't create SNMP device: %s", err)
		}

		for ch := range model.config.Devices[dev].Channels {
			model.DeviceChannelMap[model.config.Devices[dev].Channels[ch]] = model.devices[i]
		}

		i += 1
	}

	if err != nil {
		return
	}

	// fill poll table
	model.pollTable = NewPollTable()

	// form queries from config
	model.formQueries()

	return
}

// Form queries from config and fill poll table
func (m *SnmpModel) formQueries() {
	// create map from intervals to queries
	queries := make(map[int][]PollQuery)
	t := time.Now()

	// go through config file and fill queries map
	for _, dev := range m.config.Devices {
		for _, ch := range dev.Channels {
			if _, ok := queries[ch.PollInterval]; !ok {
				queries[ch.PollInterval] = make([]PollQuery, 0, 5)
			}

			// form query
			q := PollQuery{
				Channel:  ch,
				Deadline: t,
			}

			queries[ch.PollInterval] = append(queries[ch.PollInterval], q)
		}
	}

	// push that queues into poll table
	for interval, lst := range queries {
		m.pollTable.AddQueue(NewPollQueue(lst), interval)
	}
}

// Reader worker
// Receives poll query, perform SNMP transaction and
// send result (or error) to publisher worker
func (m *SnmpModel) PollWorker(id int, req <-chan PollQuery, res chan PollResult, err chan PollError, quit <-chan struct{}, done chan struct{}) {
LPollWorker:
	for {
		select {
		case r := <-req:
			_ = r
			// process query
		case <-quit:
			done <- struct{}{}
			break LPollWorker
		}
	}
}

// Publisher worker
// Receives new values from Reader workers
func (m *SnmpModel) PublisherWorker(data <-chan PollResult, err <-chan PollError, quit chan struct{}, done chan struct{}) {
LPublisherWorker:
	for {
		select {
		case d := <-data:
			// process received data
			// get device of given channel
			dev := m.DeviceChannelMap[d.Channel]

			// try to get value from cache
			val, ok := dev.Cache[d.Channel]
			if !ok {
				// create value in cache and create new control in MQTT
				dev.Cache[d.Channel] = d.Data
				// TODO: read-only, max value and retain flags
				dev.Observer.OnNewControl(dev, d.Channel.Name, d.Channel.ControlType, d.Data, true, -1, true)
			} else if val != d.Data {
				dev.Cache[d.Channel] = d.Data
				// send new value only if it has been changed
				dev.Observer.OnValue(dev, d.Channel.Name, d.Data)
			}
			done <- struct{}{}
		case e := <-err:
			_ = e
			// TODO: process error
		case <-quit:
			done <- struct{}{}
			break LPublisherWorker
		}
	}
}

// Start model
func (m *SnmpModel) Start() {
	// var err error

	// create all channels
	m.queryChannel = make(chan PollQuery, CHAN_BUFFER_SIZE)
	m.resultChannel = make(chan PollResult, CHAN_BUFFER_SIZE)
	m.errorChannel = make(chan PollError, CHAN_BUFFER_SIZE)
	m.quitChannels = make([]chan struct{}, NUM_WORKERS+1) // +1 for publisher
	m.pollDoneChannel = make(chan struct{}, CHAN_BUFFER_SIZE)
	m.pubDoneChannel = make(chan struct{}, CHAN_BUFFER_SIZE)

	for i := range m.quitChannels {
		m.quitChannels[i] = make(chan struct{})
	}

	// start workers and publisher
	for i := 0; i < NUM_WORKERS; i++ {
		go m.PollWorker(i, m.queryChannel, m.resultChannel, m.errorChannel, m.quitChannels[i], m.pollDoneChannel)
	}
	go m.PublisherWorker(m.resultChannel, m.errorChannel, m.quitChannels[NUM_WORKERS], m.pubDoneChannel)
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

	// wait for workers to shut down
	for _ = range m.quitChannels {
		select {
		case <-m.pollDoneChannel:
		case <-m.pubDoneChannel:
		}
	}
}
