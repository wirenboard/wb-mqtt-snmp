package mqtt_snmp

import (
	"sync"
	"testing"

	"github.com/wirenboard/wbgong"
	"github.com/wirenboard/wbgong/testutils"
)

// Exercise the adapter with the actual wbgo.so implementation and an in-memory broker.
func TestMQTTPublisher(t *testing.T) {
	broker := testutils.NewFakeMQTTBroker(t, nil)
	driver := newTestDriver(t, broker)

	publisher := &mqttPublisher{driver: driver}
	dev := &SnmpDevice{ID: "snmp_test", Title: "SNMP test"}
	ch := &ChannelConfig{Name: "voltage", ControlType: "value", Units: "V", Order: 4}
	if err := publisher.AddDevice(dev); err != nil {
		t.Fatal(err)
	}
	if err := publisher.NewControl(dev, ch, "10.0", false); err != nil {
		t.Fatal(err)
	}
	if err := publisher.UpdateValue(dev, ch, "20.0"); err != nil {
		t.Fatal(err)
	}
	if err := publisher.SetError(dev, ch, true); err != nil {
		t.Fatal(err)
	}
	failedChannel := &ChannelConfig{Name: "unreachable", ControlType: "value", Order: 5}
	if err := publisher.NewControl(dev, failedChannel, "", true); err != nil {
		t.Fatal(err)
	}
	checkPublishedControls(t, driver, dev, ch, failedChannel)
	if err := publisher.SetError(dev, ch, false); err != nil {
		t.Fatal(err)
	}
	if err := driver.Access(func(tx wbgong.DriverTx) error {
		if got := tx.GetDevice(dev.ID).GetControl(ch.Name).GetError(); got != nil {
			t.Errorf("read error was not cleared: %v", got)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Collect retained device topics and wait until broker has replayed all of them
	var mutex sync.Mutex
	topics := make(map[string]string)
	observer := broker.MakeClient("observer")
	observer.Start()
	t.Cleanup(observer.Stop)
	observer.Subscribe(func(msg wbgong.MQTTMessage) {
		mutex.Lock()
		defer mutex.Unlock()
		topics[msg.Topic] = msg.Payload
	}, "/devices/"+dev.ID+"/#")
	replayed := make(chan struct{})
	observer.WaitForRetained(func() { close(replayed) })
	<-replayed

	mutex.Lock()
	value := topics["/devices/"+dev.ID+"/controls/"+ch.Name]
	mutex.Unlock()
	if value != "20.0" {
		t.Fatalf("retained control value = %q, want 20.0", value)
	}

	if err := publisher.RemoveDevice(dev); err != nil {
		t.Fatal(err)
	}
	testutils.WaitFor(t, func() bool {
		mutex.Lock()
		defer mutex.Unlock()
		for _, payload := range topics {
			if payload != "" {
				return false
			}
		}
		return true
	})
	if err := driver.Access(func(tx wbgong.DriverTx) error {
		if tx.HasDevice(dev.ID) {
			t.Error("device was not removed from driver")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func newTestDriver(t *testing.T, broker *testutils.FakeMQTTBroker) wbgong.DeviceDriver {
	t.Helper()
	driver, err := wbgong.NewDriverBase(wbgong.NewDriverArgs().
		SetId(DriverClientID).
		SetMqtt(broker.MakeClient(DriverClientID)).
		SetUseStorage(false))
	if err != nil {
		t.Fatal(err)
	}
	if err := driver.StartLoop(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := driver.StopLoop(); err != nil {
			t.Error(err)
		}
		driver.Close()
	})
	driver.WaitForReady()
	return driver
}

func checkPublishedControls(t *testing.T, driver wbgong.DeviceDriver, dev *SnmpDevice, ch, failedChannel *ChannelConfig) {
	t.Helper()
	if err := driver.Access(func(tx wbgong.DriverTx) error {
		device := tx.GetDevice(dev.ID)
		if device == nil || device.GetTitle()["en"] != dev.Title {
			t.Errorf("device title was not published: %v", device)
			return nil
		}
		ctrl := device.GetControl(ch.Name)
		if ctrl == nil {
			t.Error("control was not published")
			return nil
		}
		if got := ctrl.GetRawValue(); got != "20.0" {
			t.Errorf("raw value = %q, want 20.0", got)
		}
		if ctrl.GetType() != "value" || ctrl.GetUnits() != "V" || !ctrl.GetReadonly() || ctrl.GetOrder() != 4 {
			t.Errorf("control metadata was not preserved: %v", ctrl.GetMetaJson())
		}
		if ctrl.GetError() == nil || ctrl.GetError().Error() != "r" {
			t.Errorf("read error was not set: %v", ctrl.GetError())
		}
		failed := device.GetControl(failedChannel.Name)
		if failed == nil || failed.GetError() == nil || failed.GetError().Error() != "r" {
			t.Errorf("initial read error was not published: %v", failed)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
