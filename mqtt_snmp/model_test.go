package mqtt_snmp

import (
	"fmt"
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

// Test model workers - goroutines to process requests
type ModelWorkersTest struct {
	testutils.Suite

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

	// create model
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
