package mqtt_snmp

import (
	"fmt"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gosnmp/gosnmp"
	"github.com/wirenboard/wbgong"
)

const (
	// ChanBufferSize is the size of channels buffer
	ChanBufferSize = 128
)

// SnmpDevice is an SNMP device object
type SnmpDevice struct {
	ID    string
	Title string

	// device configuration right from config tree
	Config *DeviceConfig

	// Device cached values
	Cache map[*ChannelConfig]string

	// Device errors
	Error map[*ChannelConfig]string

	// SNMP connection
	snmp SnmpInterface

	// Mutex to protect SNMP connection
	mutex sync.Mutex
}

// ConvertSnmpValue tries to convert variable value into string
func ConvertSnmpValue(v gosnmp.SnmpPDU) (data string, valid bool) {
	valid = false

	switch v.Type {
	case gosnmp.Gauge32, gosnmp.Counter32:
		var d uint
		d, valid = v.Value.(uint)
		if valid {
			data = fmt.Sprintf("%d", d)
		}
	case gosnmp.Uinteger32:
		var d uint32
		d, valid = v.Value.(uint32)
		if valid {
			data = fmt.Sprintf("%d", d)
		}
	case gosnmp.Counter64:
		var d uint64
		d, valid = v.Value.(uint64)
		if !valid {
			return "", false
		}
		data = fmt.Sprintf("%d", d)
		valid = true

	case gosnmp.Integer:
		var d int
		d, valid = v.Value.(int)
		if !valid {
			return "", false
		}
		data = fmt.Sprintf("%d", d)
		valid = true

	case gosnmp.OctetString:
		var d []byte
		d, valid = v.Value.([]byte)
		if !valid {
			return "", false
		}
		data = string(d)

		// check also if value is a text string
		// TODO: implement DISPLAY-HINT to convert compound values
		valid = utf8.Valid(d)
	case gosnmp.IPAddress:
		data, valid = v.Value.(string)
	case gosnmp.TimeTicks:
		var d uint32
		d, valid = v.Value.(uint32)
		if !valid {
			return "", false
		}
		data = (time.Duration(d) * 10 * time.Millisecond).String()
		valid = true
	}

	return data, valid
}

// Create new SNMP device instance from config tree
func newSnmpDevice(snmpFactory SnmpFactory, config *DeviceConfig, debug bool) (device *SnmpDevice, err error) {
	snmp, err := snmpFactory(config, debug)
	if err != nil {
		return
	}

	device = &SnmpDevice{
		ID:     config.ID,
		Title:  config.Name,
		snmp:   snmp,
		Config: config,
		Cache:  make(map[*ChannelConfig]string),
		Error:  make(map[*ChannelConfig]string),
	}

	return
}

// Get performs SNMP GET request for a single OID
func (d *SnmpDevice) Get(oid string) (*gosnmp.SnmpPacket, error) {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	packet, err := d.snmp.Get([]string{oid})
	if err != nil {
		return nil, fmt.Errorf("SNMP GET %s failed: %w", oid, err)
	}
	if packet == nil {
		return nil, fmt.Errorf("empty SNMP response")
	}
	if packet.Error != gosnmp.NoError {
		return nil, fmt.Errorf("SNMP error: %s (index %d)", packet.Error, packet.ErrorIndex)
	}
	// Each poll must produce exactly one result or error for the poll timer.
	if packet.PDUType != gosnmp.GetResponse || len(packet.Variables) != 1 {
		return nil, fmt.Errorf("unexpected SNMP response: %s with %d variables", packet.PDUType, len(packet.Variables))
	}
	switch packet.Variables[0].Type {
	case gosnmp.NoSuchObject, gosnmp.NoSuchInstance, gosnmp.EndOfMibView:
		return nil, fmt.Errorf("SNMP exception for %s: %s", oid, packet.Variables[0].Type)
	}
	return packet, nil
}

// Close closes SNMP connection
func (d *SnmpDevice) Close() error {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	if err := d.snmp.Close(); err != nil {
		return fmt.Errorf("can't close SNMP connection: %w", err)
	}
	return nil
}

// SnmpModel is an SNMP device model
type SnmpModel struct {
	config    *DaemonConfig
	publisher SnmpPublisher

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
	pollTimer wbgong.RTimer
}

// SnmpPublisher sends model changes to the MQTT driver.
type SnmpPublisher interface {
	AddDevice(*SnmpDevice) error
	RemoveDevice(*SnmpDevice) error
	NewControl(*SnmpDevice, *ChannelConfig, string, bool) error
	UpdateValue(*SnmpDevice, *ChannelConfig, string) error
	SetError(*SnmpDevice, *ChannelConfig, bool) error
}

// SetPublisher sets MQTT publisher for model changes
func (m *SnmpModel) SetPublisher(p SnmpPublisher) {
	m.publisher = p
}

// NewSnmpModel is an SNMP model constructor
func NewSnmpModel(snmpFactory SnmpFactory, config *DaemonConfig, start time.Time) (model *SnmpModel, err error) {
	model = &SnmpModel{
		config: config,
	}

	// init all devices from configuration
	model.devices = make([]*SnmpDevice, len(model.config.Devices))
	model.DeviceChannelMap = make(map[*ChannelConfig]*SnmpDevice)
	i := 0
	for dev := range model.config.Devices {
		if model.devices[i], err = newSnmpDevice(snmpFactory, model.config.Devices[dev], config.Debug); err != nil {
			for _, device := range model.devices[:i] {
				_ = device.Close()
			}
			return nil, fmt.Errorf("can't create SNMP device %s: %w", dev, err)
		}

		for ch := range model.config.Devices[dev].Channels {
			model.DeviceChannelMap[model.config.Devices[dev].Channels[ch]] = model.devices[i]
		}

		i++
	}

	// fill poll table
	model.pollTable = NewPollTable()

	// form queries from config and given start time
	model.formQueries(start)

	return model, nil
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
		_ = m.pollTable.AddQueue(NewPollQueue(lst), interval)
	}
}

// PollWorker is a reader worker
// Receives poll query, perform SNMP transaction and
// send result (or error) to publisher worker
func (m *SnmpModel) PollWorker(id int, req <-chan PollQuery, res chan PollResult, err chan PollError, quit <-chan struct{}, done chan struct{}) {
LPollWorker:
	for {
		select {
		case r := <-req:
			wbgong.Debug.Printf("[poller %d] Receive request %v\n", id, r.Channel.Oid)
			// process query
			dev := m.DeviceChannelMap[r.Channel]
			packet, e := dev.Get(r.Channel.Oid)
			if e != nil {
				wbgong.Error.Printf("failed to poll %s:%s: %s", dev.ID, r.Channel.Name, e)
				err <- PollError{Channel: r.Channel, Error: e.Error()}
			} else if data, valid := ConvertSnmpValue(packet.Variables[0]); !valid {
				// Get guarantees exactly one variable: one result or error per query.
				errorMessage := fmt.Sprintf("failed to poll %s:%s: instance can't be converted to string", dev.ID, r.Channel.Name)
				wbgong.Error.Print(errorMessage)
				err <- PollError{Channel: r.Channel, Error: errorMessage}
			} else {
				wbgong.Debug.Printf("[poller %d] Send result for request %v: %v", id, r, data)
				res <- PollResult{Channel: r.Channel, Data: r.Channel.Conv(data)}
			}
			done <- struct{}{}
		case <-quit:
			done <- struct{}{}
			break LPollWorker
		}
	}
}

// PublisherWorker is a publisher worker
// Receives new values from Reader workers
func (m *SnmpModel) PublisherWorker(data <-chan PollResult, err <-chan PollError, quit, done chan struct{}) {
LPublisherWorker:
	for {
		select {
		case d := <-data:
			wbgong.Debug.Printf("[publisher] Receive data %+v\n", d)
			m.publishData(d)
			done <- struct{}{}
		case e := <-err:
			m.publishError(e)
			done <- struct{}{}
		case <-quit:
			done <- struct{}{}
			break LPublisherWorker
		}
	}
}

// getChannelDevice returns device of given channel
func (m *SnmpModel) getChannelDevice(channel *ChannelConfig) *SnmpDevice {
	dev := m.DeviceChannelMap[channel]
	if dev == nil {
		panic(fmt.Sprintf("device is not found for channel: %+v", channel))
	}
	return dev
}

// publishData creates or updates a control with the polled value
func (m *SnmpModel) publishData(d PollResult) {
	dev := m.getChannelDevice(d.Channel)

	// try to get value from cache
	val, ok := dev.Cache[d.Channel]
	if !ok {
		wbgong.Debug.Printf("[publisher] Create new control for channel %+v\n", *(d.Channel))
		if pubErr := m.publisher.NewControl(dev, d.Channel, d.Data, false); pubErr != nil {
			wbgong.Error.Printf("can't create control %s/%s: %s", dev.ID, d.Channel.Name, pubErr)
			return
		}
		dev.Cache[d.Channel] = d.Data
		dev.Error[d.Channel] = ""
		return
	}

	if val != d.Data {
		if pubErr := m.publisher.UpdateValue(dev, d.Channel, d.Data); pubErr != nil {
			wbgong.Error.Printf("can't update control %s/%s: %s", dev.ID, d.Channel.Name, pubErr)
			return
		}
		dev.Cache[d.Channel] = d.Data
	}

	if dev.Error[d.Channel] == "" {
		return
	}
	if pubErr := m.publisher.SetError(dev, d.Channel, false); pubErr != nil {
		wbgong.Error.Printf("can't clear control error %s/%s: %s", dev.ID, d.Channel.Name, pubErr)
		return
	}
	dev.Error[d.Channel] = ""
}

// publishError creates a control if needed and sets the read error on it
func (m *SnmpModel) publishError(e PollError) {
	dev := m.getChannelDevice(e.Channel)

	if _, ok := dev.Cache[e.Channel]; !ok {
		wbgong.Debug.Printf("[publisher] Create new control for channel %+v\n", *(e.Channel))
		if pubErr := m.publisher.NewControl(dev, e.Channel, "", true); pubErr != nil {
			wbgong.Error.Printf("can't create control %s/%s: %s", dev.ID, e.Channel.Name, pubErr)
		} else {
			dev.Cache[e.Channel] = ""
			dev.Error[e.Channel] = "r"
		}
	}

	if err, ok := dev.Error[e.Channel]; ok && err != "r" {
		if pubErr := m.publisher.SetError(dev, e.Channel, true); pubErr != nil {
			wbgong.Error.Printf("can't set control error %s/%s: %s", dev.ID, e.Channel.Name, pubErr)
		} else {
			dev.Error[e.Channel] = "r"
		}
	}
}

// PollTimerWorker triggers pollTable to send queries
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
		wbgong.Debug.Printf("[POLLTIMEREVENT] Run at %v\n", t)

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

// SetPollTimer sets up poll timer and timer channel
// Generally this is for testing
func (m *SnmpModel) SetPollTimer(t wbgong.RTimer) {
	m.pollTimer = t
}

// Start model
func (m *SnmpModel) Start() error {
	if m.publisher == nil {
		return fmt.Errorf("SNMP publisher is not configured")
	}

	// create all channels
	m.queryChannel = make(chan PollQuery, ChanBufferSize)
	m.resultChannel = make(chan PollResult, ChanBufferSize)
	m.errorChannel = make(chan PollError, ChanBufferSize)
	m.quitChannels = make([]chan struct{}, m.config.NumWorkers+2) // +2 for publisher and poll timer
	m.pollDoneChannel = make(chan struct{}, ChanBufferSize)
	m.pubDoneChannel = make(chan struct{}, ChanBufferSize)
	m.pollTimerDoneChannel = make(chan struct{})

	for i := range m.quitChannels {
		m.quitChannels[i] = make(chan struct{})
	}

	// create devices in MQTT driver
	for i := range m.devices {
		if err := m.publisher.AddDevice(m.devices[i]); err != nil {
			m.removeDevices(m.devices[:i])
			return fmt.Errorf("can't add device %s: %w", m.devices[i].ID, err)
		}
	}

	// start poll timer
	// configure local timer if it was not configured yet
	if m.pollTimer == nil {
		nextPoll, err := m.pollTable.NextPollTime()
		if err != nil {
			m.removeDevices(m.devices)
			return fmt.Errorf("unable to get next poll time: %w", err)
		}
		m.SetPollTimer(wbgong.NewRealRTimer(time.Until(nextPoll)))
	}

	// start workers and publisher
	for i := 0; i < m.config.NumWorkers; i++ {
		go m.PollWorker(i, m.queryChannel, m.resultChannel, m.errorChannel, m.quitChannels[i], m.pollDoneChannel)
	}
	go m.PublisherWorker(m.resultChannel, m.errorChannel, m.quitChannels[m.config.NumWorkers], m.pubDoneChannel)

	go m.PollTimerWorker(m.quitChannels[m.config.NumWorkers+1], m.pollTimerDoneChannel)

	return nil
}

// Stop model - terminate all workers and remove devices from MQTT
func (m *SnmpModel) Stop() {

	// stop poller
	if m.pollTimer != nil {
		m.pollTimer.Stop()
	}

	// send signals to quit to all workers
	for i := range m.quitChannels {
		m.quitChannels[i] <- struct{}{}
	}

	// wait for workers to shut down
	pollDone := 0
	pubDone := 0
	pollTimerDone := 0
	for range m.quitChannels {
		select {
		case <-m.pollDoneChannel:
			pollDone++
		case <-m.pubDoneChannel:
			pubDone++
		case <-m.pollTimerDoneChannel:
			pollTimerDone++
		}
	}

	m.CloseDevices()

	// workers are stopped, so nothing can recreate controls after this
	m.removeDevices(m.devices)
}

// CloseDevices closes SNMP connections of all devices
func (m *SnmpModel) CloseDevices() {
	for _, device := range m.devices {
		if err := device.Close(); err != nil {
			wbgong.Error.Printf("can't close SNMP device %s: %s", device.ID, err)
		}
	}
}

// Remove devices from MQTT driver, so stale values don't stay in retained topics
func (m *SnmpModel) removeDevices(devices []*SnmpDevice) {
	if m.publisher == nil {
		return
	}
	for _, dev := range devices {
		if err := m.publisher.RemoveDevice(dev); err != nil {
			wbgong.Error.Printf("can't remove device %s: %s", dev.ID, err)
		}
	}
}
