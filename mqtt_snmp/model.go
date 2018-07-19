package mqtt_snmp

import (
	"fmt"
	"github.com/alouca/gosnmp"
	"github.com/contactless/wbgo"
	"net"
	"time"
	"unicode/utf8"
)

const (
	// Size of channels buffer
	CHAN_BUFFER_SIZE = 128
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

// ConvertSnmpValue tries to convert variable value into string
func ConvertSnmpValue(v gosnmp.SnmpPDU) (data string, valid bool) {
	valid = false

	switch v.Type {
	case gosnmp.Gauge32:
		fallthrough
	case gosnmp.Counter32:
		fallthrough
	case gosnmp.Counter64:
		var d uint64
		d, valid = v.Value.(uint64)
		if !valid {
			return
		}
		data = fmt.Sprintf("%d", d)
		valid = true

	case gosnmp.Integer:
		fallthrough
	case gosnmp.Uinteger32:
		var d int
		d, valid = v.Value.(int)
		if !valid {
			return
		}
		data = fmt.Sprintf("%d", d)
		valid = true

	case gosnmp.OctetString:
		data, valid = v.Value.(string)

		// check also if value is a text string
		// TODO: implement DISPLAY-HINT to convert compound values
		valid = utf8.Valid([]byte(data))
	case gosnmp.IpAddress:
		var d net.IP
		d, valid = v.Value.(net.IP)
		if !valid {
			return
		}
		data = fmt.Sprintf("%s", d)
		valid = true
	case gosnmp.TimeTicks:
		var d int
		d, valid = v.Value.(int)
		if !valid {
			return
		}
		data = fmt.Sprintf("%s", time.Duration(d*10)*time.Millisecond)
		valid = true
	}

	return
}

// Create new SNMP device instance from config tree
func newSnmpDevice(snmpFactory SnmpFactory, config *DeviceConfig, debug bool) (device *SnmpDevice, err error) {
	err = nil

	snmp, err := snmpFactory(config.Address, config.Community, config.SnmpVersion, int64(config.SnmpTimeout), debug)
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
	queryChannel         chan PollQuery
	resultChannel        chan PollResult
	errorChannel         chan PollError
	quitChannels         []chan struct{}
	pollDoneChannel      chan struct{}
	pubDoneChannel       chan struct{}
	pollTimerDoneChannel chan struct{}

	// Poll timer to sync poll procedures
	pollTimer wbgo.RTimer
}

// SNMP model constructor
func NewSnmpModel(snmpFactory SnmpFactory, config *DaemonConfig, start time.Time) (model *SnmpModel, err error) {
	err = nil

	model = &SnmpModel{
		config: config,
	}

	// init all devices from configuration
	model.devices = make([]*SnmpDevice, len(model.config.Devices))
	model.DeviceChannelMap = make(map[*ChannelConfig]*SnmpDevice)
	i := 0
	for dev := range model.config.Devices {
		if model.devices[i], err = newSnmpDevice(snmpFactory, model.config.Devices[dev], config.Debug); err != nil {
			wbgo.Error.Fatalf("can't create SNMP device: %s", err)
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

	// form queries from config and given start time
	model.formQueries(start)

	return
}

// Form queries from config and fill poll table
func (m *SnmpModel) formQueries(deadline time.Time) {
	// create map from intervals to queries
	queries := make(map[int][]PollQuery)

	// go through config file and fill queries map
	for _, dev := range m.config.Devices {
		for _, ch := range dev.Channels {
			if _, ok := queries[ch.PollInterval]; !ok {
				queries[ch.PollInterval] = make([]PollQuery, 0, 5)
			}

			// form query
			q := PollQuery{
				Channel:  ch,
				Deadline: deadline,
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
			wbgo.Debug.Printf("[poller %d] Receive request %v\n", id, r.Channel.Oid)
			// process query
			dev := m.DeviceChannelMap[r.Channel]
			packet, e := dev.snmp.Get(r.Channel.Oid)
			if e != nil {
				// TODO: make it Error and suppress on testing
				wbgo.Debug.Printf("failed to poll %s:%s: %s", dev.DevName, r.Channel.Name, e)
				err <- PollError{Channel: r.Channel}
			} else {
				for i := range packet.Variables {
					data, valid := ConvertSnmpValue(packet.Variables[i])
					if !valid {
						wbgo.Warn.Printf("failed to poll %s:%s: instance can't be converted to string", dev.DevName, r.Channel.Name)
						err <- PollError{Channel: r.Channel}
					} else {
						wbgo.Debug.Printf("[poller %d] Send result for request %v: %v", id, r, data)
						res <- PollResult{Channel: r.Channel, Data: data}
					}
				}
			}
			done <- struct{}{}
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
			wbgo.Debug.Printf("[publisher] Receive data %+v\n", d)

			// process received data
			// get device of given channel
			dev := m.DeviceChannelMap[d.Channel]

			if dev == nil {
				panic(fmt.Sprintf("device is not found for channel: %+v", d.Channel))
			}

			// try to get value from cache
			val, ok := dev.Cache[d.Channel]
			if !ok {
				// create value in cache and create new control in MQTT
				dev.Cache[d.Channel] = d.Data
				// TODO: read-only, max value and retain flags
				controlType := d.Channel.ControlType
				if d.Channel.Units != "" {
					controlType = controlType + ":" + d.Channel.Units
				}
				wbgo.Debug.Printf("[publisher] Create new control for channel %+v\n", *(d.Channel))
				dev.Observer.OnNewControl(dev, wbgo.Control{Name: d.Channel.Name, Type: controlType, Value: d.Data, Order: d.Channel.Order})
			} else if val != d.Data {
				dev.Cache[d.Channel] = d.Data
				// send new value only if it has been changed
				dev.Observer.OnValue(dev, d.Channel.Name, d.Data)
			}
			done <- struct{}{}
		case e := <-err:
			_ = e
			done <- struct{}{}
			// TODO: process error
		case <-quit:
			done <- struct{}{}
			break LPublisherWorker
		}
	}
}

// Timer triggers pollTable to send queries
func (m *SnmpModel) PollTimerWorker(quit <-chan struct{}, done chan struct{}) {
	var t time.Time

	for {
		// wait for timer event
		select {
		case <-quit:
			done <- struct{}{}
			return
		case t = <-m.pollTimer.GetChannel():
		}
		wbgo.Debug.Printf("[POLLTIMEREVENT] Run at %v\n", t)

		// start poll and wait until it's done
		numQueries := m.pollTable.Poll(m.queryChannel, t)
		for i := 0; i < 2*numQueries; i++ {
			select {
			case <-m.pollDoneChannel:
			case <-m.pubDoneChannel:
			}
		}

		// setup timer to next poll time
		nextPoll, err := m.pollTable.NextPollTime()
		if err != nil {
			panic("Error getting next poll time from table")
		}
		m.pollTimer.Reset(nextPoll.Sub(t))
	}
}

// Setup poll timer and timer channel
// Generally this is for testing
func (m *SnmpModel) SetPollTimer(t wbgo.RTimer) {
	m.pollTimer = t
}

// Start model
func (m *SnmpModel) Start() error {
	// create all channels
	m.queryChannel = make(chan PollQuery, CHAN_BUFFER_SIZE)
	m.resultChannel = make(chan PollResult, CHAN_BUFFER_SIZE)
	m.errorChannel = make(chan PollError, CHAN_BUFFER_SIZE)
	m.quitChannels = make([]chan struct{}, m.config.NumWorkers+2) // +2 for publisher and poll timer
	m.pollDoneChannel = make(chan struct{}, CHAN_BUFFER_SIZE)
	m.pubDoneChannel = make(chan struct{}, CHAN_BUFFER_SIZE)
	m.pollTimerDoneChannel = make(chan struct{})

	for i := range m.quitChannels {
		m.quitChannels[i] = make(chan struct{})
	}

	// observe local devices
	for i := range m.devices {
		m.Observer.OnNewDevice(m.devices[i])
	}

	// start poll timer
	// configure local timer if it was not configured yet
	if m.pollTimer == nil {
		nextPoll, err := m.pollTable.NextPollTime()
		if err != nil {
			wbgo.Error.Fatalf("unable to get next poll time: %s", err)
			m.Stop()
			return err
		}
		m.SetPollTimer(wbgo.NewRealRTimer(nextPoll.Sub(time.Now())))
	}

	// start workers and publisher
	for i := 0; i < m.config.NumWorkers; i++ {
		go m.PollWorker(i, m.queryChannel, m.resultChannel, m.errorChannel, m.quitChannels[i], m.pollDoneChannel)
	}
	go m.PublisherWorker(m.resultChannel, m.errorChannel, m.quitChannels[m.config.NumWorkers], m.pubDoneChannel)

	go m.PollTimerWorker(m.quitChannels[m.config.NumWorkers+1], m.pollTimerDoneChannel)

	return nil
}

// Built-in poll function - leave this empty, we have our own autopoll already
func (m *SnmpModel) Poll() {}

// Stop model - send signal to terminate all workers
func (m *SnmpModel) Stop() {

	// stop poller
	if m.pollTimer != nil {
		m.pollTimer.Stop()
	}

	// close all data channels
	// close(m.queryChannel)
	// close(m.resultChannel)
	// close(m.errorChannel)

	// send signals to quit to all workers
	for i := range m.quitChannels {
		m.quitChannels[i] <- struct{}{}
	}

	// wait for workers to shut down
	pollDone := 0
	pubDone := 0
	pollTimerDone := 0
	for _ = range m.quitChannels {
		select {
		case <-m.pollDoneChannel:
			pollDone++
		case <-m.pubDoneChannel:
			pubDone++
		case <-m.pollTimerDoneChannel:
			pollTimerDone++
		}
	}

	// fmt.Printf("Done: poll %d, pub %d, timer %d\n", pollDone, pubDone, pollTimerDone)
}
