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

// Mock device observer
type MockDeviceObserver struct {
	Log   []string
	mutex sync.Mutex
}

func (o *MockDeviceObserver) OnValue(dev wbgo.DeviceModel, name, value string) {
	o.mutex.Lock()
	defer o.mutex.Unlock()
	o.Log = append(o.Log, fmt.Sprintf("OnValue: device %s, name %s, value %s\n", dev.Name(), name, value))
}

func (o *MockDeviceObserver) OnNewControl(dev wbgo.LocalDeviceModel, name, controlType, value string, readonly bool, max float64, retain bool) string {
	o.mutex.Lock()
	defer o.mutex.Unlock()
	o.Log = append(o.Log, fmt.Sprintf("OnNewControl: device %s, name %s, type %s, value %s\n", dev.Name(), name, controlType, value))
	return value
}

func (o *MockDeviceObserver) GetLog() string { return strings.Join(o.Log, "") }

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

func NewFakeSNMP(address, community string, version gosnmp.SnmpVersion, timeout int64) (snmp SnmpInterface, err error) {
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
}

func (m *ModelWorkersTest) SetupTestFixture(t *testing.T) {
	fakeSNMPMessages = make(map[string]*gosnmp.SnmpPacket)
}

func (m *ModelWorkersTest) TearDownTestFixture(t *testing.T) {

}

func (m *ModelWorkersTest) SetupTest() {
	m.Suite.SetupTest()

	// create channels
	m.queryChannel = make(chan PollQuery, 128)
	m.resultChannel = make(chan PollResult, 128)
	m.errorChannel = make(chan PollError, 128)
	m.quitChannel = make(chan struct{}, 128)

	// create config
	m.config = &DaemonConfig{
		Debug: false,
		Devices: map[string]*DeviceConfig{
			"snmp_device1": &DeviceConfig{
				Name:        "Device 1",
				Address:     "127.0.0.1",
				Community:   "test",
				Id:          "snmp_device1",
				SnmpVersion: gosnmp.Version2c,
				SnmpTimeout: 1000,
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
						PollInterval: 1000,
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

	// create fake SNMP

	// create model
	m.model, _ = NewSnmpModel(NewFakeSNMP, m.config)
}

func (m *ModelWorkersTest) TearDownTest() {
	m.Suite.TearDownTest()
}

// Test PublisherWorker itself (outside the model)
func (m *ModelWorkersTest) TestPublisherWorker() {
	// create observer
	obs := &MockDeviceObserver{Log: make([]string, 0, 16)}
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
	m.Equal(obs.GetLog(), "OnNewControl: device snmp_device1, name channel1, type value, value foo\nOnValue: device snmp_device1, name channel1, value bar\nOnValue: device snmp_device1, name channel1, value baz\n")
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

func TestModelWorkers(t *testing.T) {
	s := new(ModelWorkersTest)

	s.SetupTestFixture(t)
	defer s.TearDownTestFixture(t)

	testutils.RunSuites(t, s)
}
