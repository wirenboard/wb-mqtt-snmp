package mqtt_snmp

import (
	"fmt"
	"github.com/alouca/gosnmp"
	"github.com/contactless/wbgo"
	"github.com/contactless/wbgo/testutils"
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
	OnValueEvent      MockDeviceEventType = 1
	OnNewControlEvent MockDeviceEventType = 2

	// Event waiting timeout
	EventTimeout = 1000

	// Wait timeout to check there's no more messages in channel
	WaitTimeout = 200
)

type MockDeviceEvent struct {
	Type    MockDeviceEventType
	Message string
}

// Mock device observer
type MockDeviceObserver struct {
	Log   chan MockDeviceEvent
	mutex sync.Mutex
}

func (o *MockDeviceObserver) OnValue(dev wbgo.DeviceModel, name, value string) {
	o.mutex.Lock()
	defer o.mutex.Unlock()
	o.Log <- MockDeviceEvent{OnValueEvent, fmt.Sprintf("device %s, name %s, value %s", dev.Name(), name, value)}
}

func (o *MockDeviceObserver) OnNewControl(dev wbgo.LocalDeviceModel, name, controlType, value string, readonly bool, max float64, retain bool) string {
	o.mutex.Lock()
	defer o.mutex.Unlock()
	o.Log <- MockDeviceEvent{OnNewControlEvent, fmt.Sprintf("device %s, name %s, type %s, value %s", dev.Name(), name, controlType, value)}
	return value
}

// CheckEvents checks if all events from list were pushed into log (maybe in another order)
func (o *MockDeviceObserver) CheckEvents(list []*MockDeviceEvent, timeout int) error {
	timeout_ch := make(chan struct{})
	go Timeout(timeout, timeout_ch)

	for _ = range list {
		select {
		case <-timeout_ch:
			return fmt.Errorf("event timeout")
		case event := <-o.Log:
			gotEvent := false
			// try to find received event in list
			for j := range list {
				if list[j] != nil && *(list[j]) == event {
					list[j] = nil
					gotEvent = true
					// fmt.Printf("Got event %v\n", event)
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
	timeout_ch := make(chan struct{})
	go Timeout(timeout, timeout_ch)

	select {
	case <-timeout_ch:
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
}

func (snmp *FakeSNMP) Get(oid string) (packet *gosnmp.SnmpPacket, err error) {
	if pkg, ok := fakeSNMPMessages[snmp.Address+"@"+snmp.Community+"@"+oid]; ok {
		packet = pkg
		err = nil
		return
	} else {
		packet = nil
		err = fmt.Errorf("No such instance")
		return
	}
}

func InsertFakeSNMPMessage(key string, value string) {
	fakeSNMPMessages[key] = &gosnmp.SnmpPacket{
		Version:        gosnmp.Version2c,
		Community:      "",
		RequestType:    gosnmp.GetResponse,
		RequestID:      0,
		Error:          0,
		ErrorIndex:     0,
		NonRepeaters:   0,
		MaxRepetitions: 0,
		Variables: []gosnmp.SnmpPDU{
			gosnmp.SnmpPDU{
				Name:  strings.Split(key, "@")[2],
				Type:  gosnmp.OctetString,
				Value: value,
			},
		},
	}
}

func NewFakeSNMP(address, community string, version gosnmp.SnmpVersion, timeout int64, debug bool) (snmp SnmpInterface, err error) {
	err = nil
	s := &FakeSNMP{
		Address:   address,
		Community: community,
		Version:   version,
		Timeout:   timeout,
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
	// fmt.Printf("[FAKETIMER] Updated time: %v\n", t.currentTime)
}

// Tick sends a new message to the output channel
func (t *FakeRTimer) Tick() {
	// wait for sync on Reset()
	<-t.sync

	// fmt.Printf("[FAKETIMER] Tick %v\n", t.currentTime)
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

// Fake model observer
type FakeModelObserver struct {
	// Device observer registered
	DevObserver *MockDeviceObserver
}

func (f *FakeModelObserver) CallSync(thunk func())             {}
func (f *FakeModelObserver) WhenReady(thunk func())            {}
func (f *FakeModelObserver) RemoveDevice(dev wbgo.DeviceModel) {}
func (f *FakeModelObserver) OnNewDevice(dev wbgo.DeviceModel) {
	dev.Observe(f.DevObserver)
}

func NewFakeModelObserver(devObserver *MockDeviceObserver) *FakeModelObserver {
	return &FakeModelObserver{devObserver}
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
	ModelObserver *FakeModelObserver
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

	m.ModelObserver = NewFakeModelObserver(NewMockDeviceObserver())

	// create channels
	m.queryChannel = make(chan PollQuery, 128)
	m.resultChannel = make(chan PollResult, 128)
	m.errorChannel = make(chan PollError, 128)
	m.quitChannel = make(chan struct{}, 128)

	// create config
	m.config = &DaemonConfig{
		Debug: true,
		Devices: map[string]*DeviceConfig{
			"snmp_device1": &DeviceConfig{
				Name:        "Device 1",
				Address:     "127.0.0.1",
				Community:   "test",
				Id:          "snmp_device1",
				SnmpVersion: gosnmp.Version2c,
				SnmpTimeout: 1,
				Channels: map[string]*ChannelConfig{
					"channel1": &ChannelConfig{
						Name:         "channel1",
						Oid:          ".1.2.3.4",
						ControlType:  "value",
						Conv:         AsIs,
						PollInterval: 1000,
					},
					"channel2": &ChannelConfig{
						Name:         "channel2",
						Oid:          ".1.2.3.5",
						ControlType:  "value",
						Conv:         AsIs,
						PollInterval: 2000,
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

	m.model.Observe(m.ModelObserver)
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
	m.model.DeviceChannelMap[ch].Observe(obs)

	done := make(chan struct{}, 128)

	// launch PublisherWorker
	go m.model.PublisherWorker(m.resultChannel, m.errorChannel, m.quitChannel, done)

	// send some results
	m.resultChannel <- PollResult{Channel: ch, Data: "foo"}
	m.resultChannel <- PollResult{Channel: ch, Data: "bar"}
	m.resultChannel <- PollResult{Channel: ch, Data: "baz"}
	m.resultChannel <- PollResult{Channel: ch, Data: "baz"}

	// wait for them to be processed
	for i := 0; i < 4; i++ {
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
	m.Equal(<-obs.Log, MockDeviceEvent{OnNewControlEvent, "device snmp_device1, name channel1, type value, value foo"})
	m.Equal(<-obs.Log, MockDeviceEvent{OnValueEvent, "device snmp_device1, name channel1, value bar"})
	m.Equal(<-obs.Log, MockDeviceEvent{OnValueEvent, "device snmp_device1, name channel1, value baz"})
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
	m.Equal(res, PollResult{Channel: ch1, Data: "HelloWorld"})

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
	m.Equal(res, PollResult{ch1, "GoAway"})

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
	m.Equal(er, PollError{Channel: ch2})

	// close worker
	m.quitChannel <- struct{}{}

	timeout4 := make(chan struct{})
	go Timeout(500, timeout4)
	select {
	case <-done:
	case <-timeout4:
		m.Fail("poll worker timeout on quit")
	}
}

// Test whole model
func (m *ModelWorkersTest) TestModel() {
	// Create a fake timer to make poll shots
	timer := NewFakeRTimer(m.StartTime, 1*time.Millisecond)
	m.model.SetPollTimer(timer)

	// Create fake device observer
	obs := m.ModelObserver.DevObserver

	// Set some SNMP values
	InsertFakeSNMPMessage("127.0.0.1@test@.1.2.3.4", "foo")
	InsertFakeSNMPMessage("127.0.0.1@test@.1.2.3.5", "bar")

	// Start model
	m.model.Start()
	defer m.model.Stop()

	// Send a tick to model
	timer.Tick()

	// Receive new events
	events1 := []*MockDeviceEvent{
		&MockDeviceEvent{OnNewControlEvent, "device snmp_device1, name channel1, type value, value foo"},
		&MockDeviceEvent{OnNewControlEvent, "device snmp_device1, name channel2, type value, value bar"},
	}

	m.NoError(obs.CheckEvents(events1, EventTimeout))

	// Change SNMP value for channel 1 and channel 2
	InsertFakeSNMPMessage("127.0.0.1@test@.1.2.3.4", "baz")
	InsertFakeSNMPMessage("127.0.0.1@test@.1.2.3.5", "moo")

	// After first tick we must get first message only
	timer.Tick()

	events2 := []*MockDeviceEvent{
		&MockDeviceEvent{OnValueEvent, "device snmp_device1, name channel1, value baz"},
	}

	m.NoError(obs.CheckEvents(events2, EventTimeout))

	timer.Tick()
	events3 := []*MockDeviceEvent{
		&MockDeviceEvent{OnValueEvent, "device snmp_device1, name channel2, value moo"},
	}

	m.NoError(obs.CheckEvents(events3, EventTimeout))

	timer.Tick()

	// wait for observer to flush and get no more events
	m.NoError(obs.WaitForNoMessages(WaitTimeout))
}

func TestModelWorkers(t *testing.T) {
	s := new(ModelWorkersTest)

	s.SetupTestFixture(t)
	defer s.TearDownTestFixture(t)

	testutils.RunSuites(t, s)
}
