package mqtt_snmp

import (
	"errors"
	"fmt"
	"time"

	"github.com/wirenboard/wbgong"
)

// MQTT driver identifiers
const (
	DriverClientID = "snmp"
	DriverConvID   = "wb-mqtt-snmp"
)

// SnmpDriver binds SNMP model to MQTT driver
type SnmpDriver struct {
	model  *SnmpModel
	driver wbgong.DeviceDriver
}

// NewSnmpDriver creates SNMP driver connected to MQTT broker
func NewSnmpDriver(config *DaemonConfig, broker string) (*SnmpDriver, error) {
	model, err := NewSnmpModel(NewGoSNMP, config, time.Now())
	if err != nil {
		return nil, err
	}

	args := wbgong.NewDriverArgs().
		SetId(DriverConvID).
		SetMqtt(wbgong.NewPahoMQTTClient(broker, DriverClientID)).
		SetUseStorage(false)
	driver, err := wbgong.NewDriverBase(args)
	if err != nil {
		return nil, fmt.Errorf("can't create MQTT driver: %w", err)
	}

	model.SetPublisher(&mqttPublisher{driver: driver})
	return &SnmpDriver{model: model, driver: driver}, nil
}

// Start starts MQTT driver loop and SNMP model
func (d *SnmpDriver) Start() error {
	if err := d.driver.StartLoop(); err != nil {
		return fmt.Errorf("can't start MQTT driver loop: %w", err)
	}
	d.driver.WaitForReady()
	if err := d.model.Start(); err != nil {
		_ = d.driver.StopLoop()
		d.driver.Close()
		return err
	}
	return nil
}

// Stop stops SNMP model and MQTT driver
func (d *SnmpDriver) Stop() {
	d.model.Stop()
	if err := d.driver.StopLoop(); err != nil {
		wbgong.Error.Printf("can't stop MQTT driver: %s", err)
	}
	d.driver.Close()
}

// mqttPublisher keeps all wbgong device and control operations in driver transactions.
// The SNMP model can therefore poll concurrently without touching driver state.
type mqttPublisher struct {
	driver wbgong.DeviceDriver
}

func (p *mqttPublisher) AddDevice(dev *SnmpDevice) error {
	return p.driver.Access(func(tx wbgong.DriverTx) error { //nolint:wrapcheck // the error comes from our own Access() callback
		_, err := tx.CreateDevice(wbgong.NewLocalDeviceArgs().
			SetId(dev.ID).
			SetTitle(wbgong.Title{"en": dev.Title}).
			SetVirtual(false))()
		return err
	})
}

// RemoveDevice clears all retained topics of the device and its controls.
func (p *mqttPublisher) RemoveDevice(dev *SnmpDevice) error {
	return p.driver.Access(func(tx wbgong.DriverTx) error { //nolint:wrapcheck // the error comes from our own Access() callback
		return tx.RemoveDeviceById(dev.ID)()
	})
}

func (p *mqttPublisher) NewControl(dev *SnmpDevice, ch *ChannelConfig, value string, readError bool) error {
	return p.driver.Access(func(tx wbgong.DriverTx) error { //nolint:wrapcheck // the error comes from our own Access() callback
		localDev, err := p.localDevice(tx, dev)
		if err != nil {
			return err
		}
		args := wbgong.NewControlArgs().
			SetId(ch.Name).
			SetType(ch.ControlType).
			SetUnits(ch.Units).
			SetReadonly(true).
			SetOrder(ch.Order).
			SetRawValue(value)
		if readError {
			args.SetError(errors.New("r"))
		}
		_, err = localDev.CreateControl(args)()
		return err
	})
}

func (p *mqttPublisher) UpdateValue(dev *SnmpDevice, ch *ChannelConfig, value string) error {
	return p.driver.Access(func(tx wbgong.DriverTx) error { //nolint:wrapcheck // the error comes from our own Access() callback
		ctrl, err := p.control(tx, dev, ch)
		if err != nil {
			return err
		}
		previous := ctrl.GetRawValue()
		if err := ctrl.SetRawValue(value); err != nil {
			return fmt.Errorf("can't set value of %s/%s: %w", dev.ID, ch.Name, err)
		}
		return tx.ToDeviceDriverTx().UpdateControlValue(ctrl, value, previous, true)()
	})
}

func (p *mqttPublisher) SetError(dev *SnmpDevice, ch *ChannelConfig, readError bool) error {
	return p.driver.Access(func(tx wbgong.DriverTx) error { //nolint:wrapcheck // the error comes from our own Access() callback
		ctrl, err := p.control(tx, dev, ch)
		if err != nil {
			return err
		}
		if readError {
			return ctrl.SetError(errors.New("r"))()
		}
		return ctrl.SetError(nil)()
	})
}

func (p *mqttPublisher) localDevice(tx wbgong.DriverTx, dev *SnmpDevice) (wbgong.LocalDevice, error) {
	localDev, ok := tx.GetDevice(dev.ID).(wbgong.LocalDevice)
	if !ok {
		return nil, fmt.Errorf("local device %s is missing", dev.ID)
	}
	return localDev, nil
}

func (p *mqttPublisher) control(tx wbgong.DriverTx, dev *SnmpDevice, ch *ChannelConfig) (wbgong.Control, error) {
	localDev, err := p.localDevice(tx, dev)
	if err != nil {
		return nil, err
	}
	ctrl := localDev.GetControl(ch.Name)
	if ctrl == nil {
		return nil, fmt.Errorf("control %s/%s is missing", dev.ID, ch.Name)
	}
	return ctrl, nil
}
