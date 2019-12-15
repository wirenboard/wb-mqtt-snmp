package mqttsnmp

import (
	"fmt"
	"net"
	"time"
	"unicode/utf8"

	"github.com/alouca/gosnmp"
	"github.com/contactless/wbgong"
)

const (
	// Size of channels buffer
	chanBufferSize = 128
)

// snmpDevice is SNMP device object
type snmpDevice struct {
	devName  string
	devTitle string

	driver wbgong.DeviceDriver

	// device configuration right from config tree
	config *DeviceConfig

	// Device cached values
	cache map[*ChannelConfig]interface{}

	// SNMP connection
	snmp snmpInterface
}

// convertSnmpValue tries to convert variable value into string
func convertSnmpValue(v gosnmp.SnmpPDU) (data interface{}, valid bool) {
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
		data = float64(d)
		valid = true

	case gosnmp.Integer:
		fallthrough
	case gosnmp.Uinteger32:
		var d int
		d, valid = v.Value.(int)
		if !valid {
			return
		}
		data = float64(d)
		valid = true

	case gosnmp.OctetString:
		var str string
		str, valid = v.Value.(string)

		// check also if value is a text string
		// TODO: implement DISPLAY-HINT to convert compound values
		valid = utf8.Valid([]byte(str))
		data = str
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
func newSnmpDevice(factory snmpFactory, config *DeviceConfig, debug bool, driver wbgong.DeviceDriver) (device *snmpDevice, err error) {
	err = nil

	snmp, err := factory(config.Address, config.Community, config.SnmpVersion, int64(config.SnmpTimeout), debug)
	if err != nil {
		return
	}

	device = &snmpDevice{
		devName:  config.ID,
		devTitle: config.Name,
		snmp:     snmp,
		config:   config,
		cache:    make(map[*ChannelConfig]interface{}),
		driver:   driver,
	}

	return
}

// SnmpModel is SNMP device model
type SnmpModel struct {
	driver wbgong.DeviceDriver
	config *DaemonConfig

	// devices list
	devices []*snmpDevice

	// devices associated with their channels
	deviceChannelMap map[*ChannelConfig]*snmpDevice

	// Poll schedule table
	pollTable *pollTable

	// Channels to exchange data between workers and replier
	queryChannel         chan pollQuery
	resultChannel        chan pollResult
	errorChannel         chan pollError
	quitChannels         []chan struct{}
	pollDoneChannel      chan struct{}
	pubDoneChannel       chan struct{}
	pollTimerDoneChannel chan struct{}

	// Poll timer to sync poll procedures
	pollTimer wbgong.RTimer
}

// newSnmpModel constructs new SnmpModel
func newSnmpModel(factory snmpFactory, config *DaemonConfig, start time.Time, driver wbgong.DeviceDriver) (model *SnmpModel, err error) {
	err = nil

	model = &SnmpModel{
		config: config,
		driver: driver,
	}

	// init all devices from configuration
	model.devices = make([]*snmpDevice, len(model.config.Devices))
	model.deviceChannelMap = make(map[*ChannelConfig]*snmpDevice)
	i := 0
	for dev := range model.config.Devices {
		if model.devices[i], err = newSnmpDevice(factory, model.config.Devices[dev], config.Debug, driver); err != nil {
			wbgong.Error.Fatalf("can't create SNMP device: %s", err)
		}

		for ch := range model.config.Devices[dev].channels {
			model.deviceChannelMap[model.config.Devices[dev].channels[ch]] = model.devices[i]
		}

		i++
	}

	if err != nil {
		return
	}

	// fill poll table
	model.pollTable = newPollTable()

	// form queries from config and given start time
	model.formQueries(start)

	return
}

// Form queries from config and fill poll table
func (m *SnmpModel) formQueries(deadline time.Time) {
	// create map from intervals to queries
	queries := make(map[int][]pollQuery)

	// go through config file and fill queries map
	for _, dev := range m.config.Devices {
		for _, ch := range dev.channels {
			if _, ok := queries[ch.PollInterval]; !ok {
				queries[ch.PollInterval] = make([]pollQuery, 0, 5)
			}

			// form query
			q := pollQuery{
				channel:  ch,
				deadline: deadline,
			}

			queries[ch.PollInterval] = append(queries[ch.PollInterval], q)
		}
	}

	// push that queues into poll table
	for interval, lst := range queries {
		m.pollTable.addQueue(newPollQueue(lst), interval)
	}
}

// pollWorker receives poll query, perform SNMP transaction and
// send result (or error) to publisher worker
func (m *SnmpModel) pollWorker(id int, req <-chan pollQuery, res chan pollResult, err chan pollError, quit <-chan struct{}, done chan struct{}) {
LPollWorker:
	for {
		select {
		case r := <-req:
			wbgong.Debug.Printf("[poller %d] Receive request %v\n", id, r.channel.Oid)
			// process query
			dev := m.deviceChannelMap[r.channel]
			packet, e := dev.snmp.Get(r.channel.Oid)
			if e != nil {
				// TODO: make it Error and suppress on testing
				wbgong.Debug.Printf("failed to poll %s:%s: %s", dev.devName, r.channel.Name, e)
				err <- pollError{channel: r.channel}
			} else {
				for i := range packet.Variables {
					data, valid := convertSnmpValue(packet.Variables[i])
					if !valid {
						wbgong.Warn.Printf("failed to poll %s:%s: instance can't be converted to string", dev.devName, r.channel.Name)
						err <- pollError{channel: r.channel}
					} else {
						wbgong.Debug.Printf("[poller %d] Send result for request %v: %v", id, r, data)
						res <- pollResult{channel: r.channel, data: data}
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

// publisherWorker receives new values from Reader workers
func (m *SnmpModel) publisherWorker(data <-chan pollResult, err <-chan pollError, quit chan struct{}, done chan struct{}) {
LPublisherWorker:
	for {
		select {
		case d := <-data:
			wbgong.Debug.Printf("[publisher] Receive data %+v\n", d)

			// process received data
			// get device of given channel
			dev := m.deviceChannelMap[d.channel]

			if dev == nil {
				panic(fmt.Sprintf("device is not found for channel: %+v", d.channel))
			}

			// try to get value from cache
			val, ok := dev.cache[d.channel]
			if !ok {
				// create value in cache and create new control in MQTT
				dev.cache[d.channel] = d.data
				// TODO: read-only, max value and retain flags
				controlType := d.channel.ControlType
				if d.channel.Units != "" {
					controlType = controlType + ":" + d.channel.Units
				}
				wbgong.Debug.Printf("[publisher] Create new control for channel %#v\n", *(d.channel))

				err := dev.driver.Access(func(tx wbgong.DriverTx) (err error) {
					currentDevice := tx.GetDevice(dev.devName)
					// create control
					args := wbgong.NewControlArgs().
						SetId(d.channel.Name).
						SetDevice(currentDevice).
						SetType(d.channel.ControlType).
						SetOrder(d.channel.Order).
						SetValue(d.data).
						SetReadonly(true).
						SetValue(d.data)
					_, err = currentDevice.(wbgong.LocalDevice).CreateControl(args)()
					return
				})

				if err != nil {
					wbgong.Error.Printf("[publisher] Error in creating control: %s\n", err)
				}

				// dev.Observer.OnNewControl(dev, wbgo.Control{Name: d.Channel.Name, Type: controlType, Value: d.Data, Order: d.Channel.Order})
			} else if val != d.data {
				dev.cache[d.channel] = d.data
				// send new value only if it has been changed
				err := dev.driver.Access(func(tx wbgong.DriverTx) (err error) {
					wbDev := tx.ToDeviceDriverTx().GetDevice(dev.devName)
					wbControl := wbDev.GetControl(d.channel.Name)
					err = wbControl.UpdateValue(d.data)()
					return
					// return tx.GetDevice(dev.devName).GetControl(d.Channel.Name).SetValue(d.Data)()
				})

				if err != nil {
					wbgong.Error.Printf("[publisher] Error in setting control value: %s\n", err)
				}
				// dev.Observer.OnValue(dev, d.Channel.Name, d.Data)
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

// pollTimerWorker is a timer triggers pollTable to send queries
func (m *SnmpModel) pollTimerWorker(quit <-chan struct{}, done chan struct{}) {
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
		numQueries := m.pollTable.poll(m.queryChannel, t)
		for i := 0; i < 2*numQueries; i++ {
			select {
			case <-m.pollDoneChannel:
			case <-m.pubDoneChannel:
			}
		}

		// setup timer to next poll time
		nextPoll, err := m.pollTable.nextPollTime()
		if err != nil {
			panic("Error getting next poll time from table")
		}
		m.pollTimer.Reset(nextPoll.Sub(t))
	}
}

// setPollTimer setup poll timer and timer channel
// Generally this is for testing
func (m *SnmpModel) setPollTimer(t wbgong.RTimer) {
	m.pollTimer = t
}

// Start model
func (m *SnmpModel) Start() error {
	// create all channels
	m.queryChannel = make(chan pollQuery, chanBufferSize)
	m.resultChannel = make(chan pollResult, chanBufferSize)
	m.errorChannel = make(chan pollError, chanBufferSize)
	m.quitChannels = make([]chan struct{}, m.config.NumWorkers+2) // +2 for publisher and poll timer
	m.pollDoneChannel = make(chan struct{}, chanBufferSize)
	m.pubDoneChannel = make(chan struct{}, chanBufferSize)
	m.pollTimerDoneChannel = make(chan struct{})

	for i := range m.quitChannels {
		m.quitChannels[i] = make(chan struct{})
	}

	// observe local devices
	for i := range m.devices {
		iDev := m.devices[i]
		devArgs := wbgong.NewLocalDeviceArgs().SetId(iDev.devName).SetTitle(iDev.devTitle)
		err := m.devices[i].driver.Access(func(tx wbgong.DriverTx) (err error) {
			// create device by device description
			_, err = tx.CreateDevice(devArgs)()
			return
		})
		if err != nil {
			return err
		}
	}

	// start poll timer
	// configure local timer if it was not configured yet
	if m.pollTimer == nil {
		nextPoll, err := m.pollTable.nextPollTime()
		if err != nil {
			wbgong.Error.Fatalf("unable to get next poll time: %s", err)
			m.Stop()
			return err
		}
		m.setPollTimer(wbgong.NewRealRTimer(nextPoll.Sub(time.Now())))
	}

	// start workers and publisher
	for i := 0; i < m.config.NumWorkers; i++ {
		go m.pollWorker(i, m.queryChannel, m.resultChannel, m.errorChannel, m.quitChannels[i], m.pollDoneChannel)
	}
	go m.publisherWorker(m.resultChannel, m.errorChannel, m.quitChannels[m.config.NumWorkers], m.pubDoneChannel)

	go m.pollTimerWorker(m.quitChannels[m.config.NumWorkers+1], m.pollTimerDoneChannel)

	return nil
}

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

	// fmt.Printf("Done: poll %d, pub %d, timer %d\n", pollDone, pubDone, pollTimerDone)
}
