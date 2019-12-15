package main

import (
	"flag"
	"io"
	"log"
	"os"
	"os/signal"

	m "github.com/contactless/wb-mqtt-snmp/mqttsnmp"
	"github.com/contactless/wbgong"
)

const (
	driverClientID = "snmp"
	driverConvID   = "wb-mqtt-snmp"
)

func main() {

	broker := flag.String("broker", "tcp://localhost:1883", "MQTT broker URL")
	configFile := flag.String("config", "/etc/wb-mqtt-snmp.conf", "Config file location")
	templatesDir := flag.String("templates", "/usr/share/wb-mqtt-snmp/templates/", "Templates directory")
	debug := flag.Bool("debug", false, "Enable debugging")
	wbgoso := flag.String("wbgo", "/usr/share/wb-mqtt-snmp/wbgo.so", "Location to wbgo.so file")

	flag.Parse()

	errInit := wbgong.Init(*wbgoso)
	if errInit != nil {
		log.Fatalf("ERROR in init wbgo.so: '%s'", errInit)
	}

	// open config file
	var err error
	var r io.Reader
	if r, err = os.Open(*configFile); err != nil {
		wbgong.Error.Fatalf("can't open config file %s: %s", *configFile, err)
	}

	// read config
	var cfg *m.DaemonConfig
	if cfg, err = m.NewDaemonConfig(r, *templatesDir); err != nil {
		wbgong.Error.Fatalf("error parsing config file %s: %s", *configFile, err)
	}

	// update debug flag
	cfg.Debug = cfg.Debug || *debug
	wbgong.SetDebuggingEnabled(cfg.Debug)

	// translate OIDs
	if err = m.TranslateOidsInDaemonConfig(cfg); err != nil {
		wbgong.Error.Fatalf("error translating OIDs: %s", err)
	}

	// wbgo.Debug.Printf("Config structure: %#v\n", *(cfg.Devices["snmp_test.net-snmp.org"]))

	driverMqttClient := wbgong.NewPahoMQTTClient(*broker, driverClientID)
	driverArgs := wbgong.NewDriverArgs().
		SetId(driverConvID).
		SetMqtt(driverMqttClient).
		SetUseStorage(false)
	driver, err := wbgong.NewDriverBase(driverArgs)
	if err != nil {
		wbgong.Error.Fatalf("error creating driver: %s", err)
	}
	wbgong.Info.Println("driver is created")
	if err := driver.StartLoop(); err != nil {
		wbgong.Error.Fatalf("error starting the driver: %s", err)
	}
	driver.WaitForReady()
	wbgong.Info.Println("driver is ready")

	// create driver object and start daemon
	if snmpDriver, err := m.NewSnmpDriver(cfg, driver); err != nil {
		wbgong.Error.Fatalf("can't create driver object: %s", err)
	} else {
		if err := snmpDriver.Start(); err != nil {
			wbgong.Error.Fatalf("can't start driver: %s", err)
		} else {
			wbgong.Debug.Println("Work in process")
			// handle SIGINT
			c := make(chan os.Signal, 1)
			signal.Notify(c, os.Interrupt)

			// block until SIGINT received
			<-c
			wbgong.Debug.Println("Termination signal caught, shutting down...")

			// stop driver and exit gracefully
			snmpDriver.Stop()
			driver.StopLoop()
			driver.Close()
			return
		}
	}
}
