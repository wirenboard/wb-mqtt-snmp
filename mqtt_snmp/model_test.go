package mqtt_snmp

import (
	"fmt"
	"github.com/alouca/gosnmp"
	"github.com/contactless/wbgo"
	"github.com/contactless/wbgo/testutils"
	"testing"
)

// Test config

// Mock device observer
type MockDeviceObserver struct {
	log string
}

func (o *MockDeviceObserver) OnValue(dev wbgo.DeviceModel, name, value string) {
	o.log += fmt.Sprintf("OnValue: device %s, name %s, value %s\n", dev.Name(), name, value)
}

func (o *MockDeviceObserver) OnNewControl(dev wbgo.LocalDeviceModel, name, controlType, value string, readonly bool, max float64, retain bool) string {
	o.log += fmt.Sprintf("OnNewControl: device %s, name %s, type %s, value %s", dev.Name(), name, controlType, value)
	return value
}

func (o *MockDeviceObserver) GetLog() string { return o.log }

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
	m.queryChannel = make(chan PollQuery)
	m.resultChannel = make(chan PollResult)
	m.errorChannel = make(chan PollError)
	m.quitChannel = make(chan struct{})

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

// Test PublisherWorker
func (m *ModelWorkersTest) TestPublisherWorker() {
	// launch PublisherWorker

}

func TestModelWorkers(t *testing.T) {
	s := new(ModelWorkersTest)

	s.SetupTestFixture(t)
	defer s.TearDownTestFixture(t)

	testutils.RunSuites(t, s)
}
