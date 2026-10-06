package mqtt_snmp

import (
	"fmt"
	"github.com/gosnmp/gosnmp"
	"github.com/wirenboard/wbgong/testutils"
	"strings"
	"sync"
	"testing"
	"time"
)

// Timeout routine
func Timeout(d int, c chan struct{}) {
	time.Sleep(time.Duration(d) * time.Millisecond)
	c <- struct{}{}
}

// Event description for mock device observer
type MockDeviceEventType int

const (
	// Event waiting timeout
	EventTimeout = 1000

	// Wait timeout to check there's no more messages in channel
	WaitTimeout = 200

	OnValueEvent MockDeviceEventType = iota
	OnNewControlEvent
	OnErrorEvent
	OnRemoveDeviceEvent
)

type MockDeviceEvent struct {
	Type    MockDeviceEventType
	Message string
}

// Mock device observer
type MockDeviceObserver struct {
	Log   chan MockDeviceEvent
	mutex sync.Mutex

	// AddDevice call number (starting from 1) which fails, 0 means never
	FailAddDeviceCall int
	addDeviceCalls    int
}

func (o *MockDeviceObserver) AddDevice(dev *SnmpDevice) error {
	o.mutex.Lock()
	defer o.mutex.Unlock()
	o.addDeviceCalls++
	if o.addDeviceCalls == o.FailAddDeviceCall {
		return fmt.Errorf("can't add device %s", dev.ID)
	}
	return nil
}

func (o *MockDeviceObserver) RemoveDevice(dev *SnmpDevice) error {
	o.mutex.Lock()
	defer o.mutex.Unlock()
	o.Log <- MockDeviceEvent{OnRemoveDeviceEvent, fmt.Sprintf("device %s", dev.ID)}
	return nil
}

func (o *MockDeviceObserver) UpdateValue(dev *SnmpDevice, ch *ChannelConfig, value string) error {
	o.mutex.Lock()
	defer o.mutex.Unlock()
	o.Log <- MockDeviceEvent{OnValueEvent, fmt.Sprintf("device %s, name %s, value %s", dev.ID, ch.Name, value)}
	return nil
}

func (o *MockDeviceObserver) NewControl(dev *SnmpDevice, ch *ChannelConfig, value string, readError bool) error {
	o.mutex.Lock()
	defer o.mutex.Unlock()
	o.Log <- MockDeviceEvent{OnNewControlEvent, fmt.Sprintf("device %s, name %s, type %s, value %s, order %d", dev.ID, ch.Name, ch.ControlType, value, ch.Order)}
	return nil
}

func (o *MockDeviceObserver) SetError(dev *SnmpDevice, ch *ChannelConfig, readError bool) error {
	return nil
}

// CheckEvents checks if all events from list were pushed into log (maybe in another order)
func (o *MockDeviceObserver) CheckEvents(list []*MockDeviceEvent, timeout int) error {
	timeoutCh := make(chan struct{})
	go Timeout(timeout, timeoutCh)

	for range list {
		select {
		case <-timeoutCh:
			return fmt.Errorf("event timeout")
		case event := <-o.Log:
			gotEvent := false
			// try to find received event in list
			for j := range list {
				if list[j] != nil && *(list[j]) == event {
					list[j] = nil
					gotEvent = true
					break
				}
			}

			if !gotEvent {
				return fmt.Errorf("unknown event received: %v", event)
			}
		}
	}

	return nil
}

// WaitForNoMessages checks if no messages are going to be received during given interval
func (o *MockDeviceObserver) WaitForNoMessages(timeout int) error {
	timeoutCh := make(chan struct{})
	go Timeout(timeout, timeoutCh)

	select {
	case <-timeoutCh:
		return nil
	case event := <-o.Log:
		return fmt.Errorf("got unexpected message: %v", event)
	}
}

func NewMockDeviceObserver() *MockDeviceObserver {
	return &MockDeviceObserver{
		Log: make(chan MockDeviceEvent, 16),
	}
}

var (
	// Map of fake SNMP objects to be read from FakeSNMPs
	// Keys are "address@community@oid"
	fakeSNMPMessages map[string]*gosnmp.SnmpPacket
)

// Fake SNMP connection
type FakeSNMP struct {
	Address, Community string
	Version            gosnmp.SnmpVersion
	Timeout            int64
	Closed             bool
}

func (snmp *FakeSNMP) Get(oids []string) (packet *gosnmp.SnmpPacket, err error) {
	if len(oids) != 1 {
		return nil, fmt.Errorf("expected exactly one OID")
	}
	oid := oids[0]
	if pkg, ok := fakeSNMPMessages[snmp.Address+"@"+snmp.Community+"@"+oid]; ok {
		packet = pkg
		err = nil
		return
	}
	packet = nil
	err = fmt.Errorf("No such instance")
	return
}

func (snmp *FakeSNMP) Close() error {
	snmp.Closed = true
	return nil
}

func InsertFakeSNMPMessage(key, value string) {
	fakeSNMPMessages[key] = &gosnmp.SnmpPacket{
		Version:        gosnmp.Version2c,
		Community:      "",
		PDUType:        gosnmp.GetResponse,
		RequestID:      0,
		Error:          0,
		ErrorIndex:     0,
		NonRepeaters:   0,
		MaxRepetitions: 0,
		Variables: []gosnmp.SnmpPDU{
			gosnmp.SnmpPDU{
				Name:  strings.Split(key, "@")[2],
				Type:  gosnmp.OctetString,
				Value: []byte(value),
			},
		},
	}
}

func NewFakeSNMP(config *DeviceConfig, debug bool) (snmp SnmpInterface, err error) {
	err = nil
	s := &FakeSNMP{
		Address:   config.Address,
		Community: config.Community,
		Version:   config.SnmpVersion,
		Timeout:   int64(config.SnmpTimeout),
	}
	snmp = s

	return
}

// Very simple fake timer for model testing
type FakeRTimer struct {
	c           chan time.Time
	currentTime time.Time
	sync        chan struct{}
}

func (t *FakeRTimer) GetChannel() <-chan time.Time {
	return t.c
}

func (t *FakeRTimer) Stop() {}

// Reset adds duration value to local time value and sends a new time message immediately
func (t *FakeRTimer) Reset(d time.Duration) {
	t.currentTime = t.currentTime.Add(d)
	// send sync
	t.sync <- struct{}{}
}

// Tick sends a new message to the output channel
func (t *FakeRTimer) Tick() {
	// wait for sync on Reset()
	<-t.sync

	t.c <- t.currentTime
}

// NewFakeRTimer creates a new fake RTimer starting from localTimer
// numShots is a number of messages to generate on Reset() calls
// d is a first shot duration
func NewFakeRTimer(localTime time.Time, d time.Duration) *FakeRTimer {
	t := &FakeRTimer{
		c:           make(chan time.Time, 16),
		currentTime: localTime.Add(d),
		sync:        make(chan struct{}, 1),
	}

	t.sync <- struct{}{}

	return t
}

// Test model workers - goroutines to process requests
type ModelWorkersTest struct {
	testutils.Suite

	// Test config
	config *DaemonConfig

	// Test model
	model *SnmpModel

	// Workers channels
	queryChannel  chan PollQuery
	resultChannel chan PollResult
	errorChannel  chan PollError
	quitChannel   chan struct{}

	// Default start time
	StartTime time.Time

	// Model observer
	ModelObserver *MockDeviceObserver
}

func (m *ModelWorkersTest) SetupTestFixture(t *testing.T) {
	// default start time
	m.StartTime = time.Date(2016, time.December, 1, 0, 0, 0, 0, time.UTC)
}

func (m *ModelWorkersTest) TearDownTestFixture(t *testing.T) {

}

func (m *ModelWorkersTest) SetupTest() {
	fakeSNMPMessages = make(map[string]*gosnmp.SnmpPacket)

	m.Suite.SetupTest()

	m.ModelObserver = NewMockDeviceObserver()

	// create channels
	m.queryChannel = make(chan PollQuery, 128)
	m.resultChannel = make(chan PollResult, 128)
	m.errorChannel = make(chan PollError, 128)
	m.quitChannel = make(chan struct{}, 128)

	// create config
	m.config = &DaemonConfig{
		Debug:      true,
		NumWorkers: 4,
		Devices: map[string]*DeviceConfig{
			"snmp_device1": &DeviceConfig{
				Name:        "Device 1",
				Address:     "127.0.0.1",
				Community:   "test",
				ID:          "snmp_device1",
				SnmpVersion: gosnmp.Version2c,
				SnmpTimeout: 1,
				Channels: map[string]*ChannelConfig{
					"channel1": &ChannelConfig{
						Name:         "channel1",
						Oid:          ".1.2.3.4",
						ControlType:  "value",
						Conv:         AsIs,
						PollInterval: 1000,
						Units:        "U",
						Order:        1,
					},
					"channel2": &ChannelConfig{
						Name:         "channel2",
						Oid:          ".1.2.3.5",
						ControlType:  "value",
						Conv:         AsIs,
						PollInterval: 2000,
						Order:        2,
					},
					"channel3": &ChannelConfig{
						Name:         "channel3",
						Oid:          ".1.2.3.6",
						ControlType:  "value",
						Conv:         Scale(0.1),
						PollInterval: 2000,
						Order:        3,
					},
				},
			},
		},
	}

	// setup backward pointers
	for dev := range m.config.Devices {
		for ch := range m.config.Devices[dev].Channels {
			t := m.config.Devices[dev].Channels[ch]
			t.Device = m.config.Devices[dev]
			m.config.Devices[dev].Channels[ch] = t
		}
	}

	// create model
	m.model, _ = NewSnmpModel(NewFakeSNMP, m.config, m.StartTime)

	m.model.SetPublisher(m.ModelObserver)
}

func (m *ModelWorkersTest) TearDownTest() {
	m.Suite.TearDownTest()
}

// Test PublisherWorker itself (outside the model)
func (m *ModelWorkersTest) TestPublisherWorker() {
	// create observer
	obs := NewMockDeviceObserver()
	ch := m.config.Devices["snmp_device1"].Channels["channel1"]

	// observe test device
	m.model.SetPublisher(obs)

	done := make(chan struct{}, 128)

	// launch PublisherWorker
	go m.model.PublisherWorker(m.resultChannel, m.errorChannel, m.quitChannel, done)

	// send some results
	m.resultChannel <- PollResult{Channel: ch, Data: "foo"}
	m.resultChannel <- PollResult{Channel: ch, Data: "bar"}
	m.resultChannel <- PollResult{Channel: ch, Data: "baz"}
	m.resultChannel <- PollResult{Channel: ch, Data: "baz"}

	// wait for them to be processed
	for range 4 {
		<-done
	}

	// quit worker
	m.quitChannel <- struct{}{}

	// wait for quit or timeout
	timeout := make(chan struct{})
	go Timeout(500, timeout)

	select {
	case <-done:
		break
	case <-timeout:
		m.Fail("publisher worker timeout")
	}

	// compare mock logs
	m.Equal(MockDeviceEvent{OnNewControlEvent, "device snmp_device1, name channel1, type value, value foo, order 1"}, <-obs.Log)
	m.Equal(MockDeviceEvent{OnValueEvent, "device snmp_device1, name channel1, value bar"}, <-obs.Log)
	m.Equal(MockDeviceEvent{OnValueEvent, "device snmp_device1, name channel1, value baz"}, <-obs.Log)
}

// Test poll worker itself (outside the model)
func (m *ModelWorkersTest) TestPollWorker() {
	// Insert some fake SNMP messages for channel1 (channel2 left unreachable)
	InsertFakeSNMPMessage("127.0.0.1@test@.1.2.3.4", "HelloWorld")

	// Create service channels
	done := make(chan struct{}, 128)
	t := time.Now()
	ch1 := m.config.Devices["snmp_device1"].Channels["channel1"]
	ch2 := m.config.Devices["snmp_device1"].Channels["channel2"]
	ch3 := m.config.Devices["snmp_device1"].Channels["channel3"]

	// Run poll worker
	go m.model.PollWorker(0, m.queryChannel, m.resultChannel, m.errorChannel, m.quitChannel, done)

	// Push some requests to model
	m.queryChannel <- PollQuery{ch1, t}
	// wait for this to be done
	timeout1 := make(chan struct{})
	go Timeout(500, timeout1)

	select {
	case <-done:
	case <-timeout1:
		m.Fail("poll worker timeout")
	}

	// get result
	var res PollResult
	select {
	case res = <-m.resultChannel:
	default:
		m.Fail("no result from poller")
	}
	m.Equal(PollResult{Channel: ch1, Data: "HelloWorld"}, res)

	//
	// Poll new value
	InsertFakeSNMPMessage("127.0.0.1@test@.1.2.3.4", "GoAway")
	m.queryChannel <- PollQuery{ch1, t}
	// wait
	timeout2 := make(chan struct{})
	go Timeout(500, timeout2)

	select {
	case <-done:
	case <-timeout2:
		m.Fail("poll worker timeout")
	}

	// get result
	select {
	case res = <-m.resultChannel:
	default:
		m.Fail("no result from poller")
	}
	m.Equal(PollResult{ch1, "GoAway"}, res)

	//
	// Poll no value and so get error
	m.queryChannel <- PollQuery{ch2, t}
	// wait
	timeout3 := make(chan struct{})
	go Timeout(500, timeout3)
	select {
	case <-done:
	case <-timeout3:
		m.Fail("poll worker timeout on no entry")
	}

	// get error
	var er PollError
	select {
	case er = <-m.errorChannel:
	default:
		m.Fail("no error from poller")
	}
	m.Equal(PollError{Channel: ch2, Error: "SNMP GET " + ch2.Oid + " failed: No such instance"}, er)

	//
	// Poll new value with scale
	InsertFakeSNMPMessage("127.0.0.1@test@.1.2.3.6", "100")
	m.queryChannel <- PollQuery{ch3, t}
	// wait
	timeout5 := make(chan struct{})
	go Timeout(500, timeout5)

	select {
	case <-done:
	case <-timeout5:
		m.Fail("poll worker timeout")
	}

	// get result
	select {
	case res = <-m.resultChannel:
	default:
		m.Fail("no result from poller")
	}
	m.Equal(PollResult{ch3, "10.0"}, res)

	// close worker
	m.quitChannel <- struct{}{}

	timeout4 := make(chan struct{})
	go Timeout(500, timeout4)
	select {
	case <-done:
	case <-timeout4:
		m.Fail("poll worker timeout on quit")
	}

	m.EnsureGotErrors()
}

// Test whole model
func (m *ModelWorkersTest) TestModel() {
	// Create a fake timer to make poll shots
	timer := NewFakeRTimer(m.StartTime, 1*time.Millisecond)
	m.model.SetPollTimer(timer)

	// Create fake device observer
	obs := m.ModelObserver

	// Set some SNMP values
	InsertFakeSNMPMessage("127.0.0.1@test@.1.2.3.4", "foo")
	InsertFakeSNMPMessage("127.0.0.1@test@.1.2.3.5", "bar")
	InsertFakeSNMPMessage("127.0.0.1@test@.1.2.3.6", "200")

	// Start model
	m.model.Start()
	defer func() {
		// Stop closes connections once the workers have quit
		m.model.Stop()
		for _, dev := range m.model.devices {
			m.True(dev.snmp.(*FakeSNMP).Closed, "connection of %s was not closed", dev.ID)
		}
		// Stop must remove devices from MQTT
		m.NoError(obs.CheckEvents([]*MockDeviceEvent{
			&MockDeviceEvent{OnRemoveDeviceEvent, "device snmp_device1"},
		}, EventTimeout))
	}()

	// Send a tick to model
	timer.Tick()

	// Receive new events
	events1 := []*MockDeviceEvent{
		&MockDeviceEvent{OnNewControlEvent, "device snmp_device1, name channel1, type value, value foo, order 1"},
		&MockDeviceEvent{OnNewControlEvent, "device snmp_device1, name channel2, type value, value bar, order 2"},
		&MockDeviceEvent{OnNewControlEvent, "device snmp_device1, name channel3, type value, value 20.0, order 3"},
	}

	m.Require().NoError(obs.CheckEvents(events1, EventTimeout))

	// Change SNMP value for channel 1 and channel 2
	InsertFakeSNMPMessage("127.0.0.1@test@.1.2.3.4", "baz")
	InsertFakeSNMPMessage("127.0.0.1@test@.1.2.3.5", "moo")

	// After first tick we must get first message only
	timer.Tick()

	events2 := []*MockDeviceEvent{
		&MockDeviceEvent{OnValueEvent, "device snmp_device1, name channel1, value baz"},
	}

	m.Require().NoError(obs.CheckEvents(events2, EventTimeout))

	timer.Tick()
	events3 := []*MockDeviceEvent{
		&MockDeviceEvent{OnValueEvent, "device snmp_device1, name channel2, value moo"},
	}

	m.Require().NoError(obs.CheckEvents(events3, EventTimeout))

	timer.Tick()

	// wait for observer to flush and get no more events
	m.Require().NoError(obs.WaitForNoMessages(WaitTimeout))
}

// Test that failed Start removes devices it has already created
func (m *ModelWorkersTest) TestStartRollback() {
	m.config.Devices["snmp_device2"] = &DeviceConfig{
		Name:        "Device 2",
		Address:     "127.0.0.2",
		Community:   "test",
		ID:          "snmp_device2",
		SnmpVersion: gosnmp.Version2c,
		SnmpTimeout: 1,
		Channels:    map[string]*ChannelConfig{},
	}
	model, err := NewSnmpModel(NewFakeSNMP, m.config, m.StartTime)
	m.Require().NoError(err)
	model.SetPollTimer(NewFakeRTimer(m.StartTime, 1*time.Millisecond))

	obs := NewMockDeviceObserver()
	obs.FailAddDeviceCall = 2
	model.SetPublisher(obs)

	m.Require().Error(model.Start())

	// only the first device was created, so only it must be removed
	m.NoError(obs.CheckEvents([]*MockDeviceEvent{
		&MockDeviceEvent{OnRemoveDeviceEvent, "device " + model.devices[0].ID},
	}, EventTimeout))
	m.NoError(obs.WaitForNoMessages(WaitTimeout))
}

func TestModelWorkers(t *testing.T) {
	s := new(ModelWorkersTest)

	s.SetupTestFixture(t)
	defer s.TearDownTestFixture(t)

	testutils.RunSuites(t, s)
}
