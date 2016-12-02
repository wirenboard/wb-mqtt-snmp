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

// Test config

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

// Fake SNMP connection
type FakeSNMP struct {
	Address, Community string
	Version            gosnmp.SnmpVersion
	Timeout            int64

	// map of fake messages
	Messages map[string]*gosnmp.SnmpPacket
}

func (snmp *FakeSNMP) Get(oid string) (packet *gosnmp.SnmpPacket, err error) {
	if pkg, ok := snmp.Messages[oid]; ok {
		packet = pkg
		err = nil
		return
	} else {
		packet = nil
		err = fmt.Errorf("No such instance")
		return
	}
}

func (snmp *FakeSNMP) Insert(oid string, value string) {
	snmp.Messages[oid] = &gosnmp.SnmpPacket{
		Version:        snmp.Version,
		Community:      snmp.Community,
		RequestType:    gosnmp.GetResponse,
		RequestID:      0,
		Error:          0,
		ErrorIndex:     0,
		NonRepeaters:   0,
		MaxRepetitions: 0,
		Variables: []gosnmp.SnmpPDU{
			gosnmp.SnmpPDU{
				Name:  oid,
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
		Messages:  make(map[string]*gosnmp.SnmpPacket),
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
				},
			},
		},
	}

	// setup backward pointers
	t := m.config.Devices["snmp_device1"].Channels["channel1"]
	t.Device = m.config.Devices["snmp_device1"]
	m.config.Devices["snmp_device1"].Channels["channel1"] = t

	// create model
	m.model, _ = NewSnmpModel(NewFakeSNMP, m.config)
}

func (m *ModelWorkersTest) TearDownTest() {
	m.Suite.TearDownTest()
}

// Test PublisherWorker itself (outside model)
func (m *ModelWorkersTest) TestPublisherWorker() {
	// create observer
	obs := &MockDeviceObserver{Log: make([]string, 0, 16)}
	ch := m.config.Devices["snmp_device1"].Channels["channel1"]

	// obs.OnValue(m.model.DeviceChannelMap[ch], "hello", "world")

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
	go func() {
		time.Sleep(500 * time.Millisecond)
		timeout <- struct{}{}
	}()

	select {
	case <-done:
		break
	case <-timeout:
		m.Fail("publisher worker timeout")
	}

	// compare mock logs
	m.Equal(obs.GetLog(), "OnNewControl: device snmp_device1, name channel1, type value, value foo\nOnValue: device snmp_device1, name channel1, value bar\nOnValue: device snmp_device1, name channel1, value baz\n")
}

func TestModelWorkers(t *testing.T) {
	s := new(ModelWorkersTest)

	s.SetupTestFixture(t)
	defer s.TearDownTestFixture(t)

	testutils.RunSuites(t, s)
}
