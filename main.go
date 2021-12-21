package main

import (
	"flag"
	m "github.com/wirenboard/wb-mqtt-snmp/mqtt_snmp"
	"github.com/contactless/wbgo"
	"io"
	"os"
	"os/signal"
)

func main() {

	broker := flag.String("broker", "tcp://localhost:1883", "MQTT broker URL")
	configFile := flag.String("config", "/etc/wb-mqtt-snmp.conf", "Config file location")
	templatesDir := flag.String("templates", "/usr/share/wb-mqtt-snmp/templates/", "Templates directory")
	debug := flag.Bool("debug", false, "Enable debugging")

	flag.Parse()

	// open config file
	var err error
	var r io.Reader
	if r, err = os.Open(*configFile); err != nil {
		wbgo.Error.Fatalf("can't open config file %s: %s", *configFile, err)
	}

	// read config
	var cfg *m.DaemonConfig
	if cfg, err = m.NewDaemonConfig(r, *templatesDir); err != nil {
		wbgo.Error.Fatalf("error parsing config file %s: %s", *configFile, err)
	}

	// update debug flag
	cfg.Debug = cfg.Debug || *debug
	wbgo.SetDebuggingEnabled(cfg.Debug)

	// translate OIDs
	if err = m.TranslateOidsInDaemonConfig(cfg); err != nil {
		wbgo.Error.Fatalf("error translating OIDs: %s", err)
	}

	// wbgo.Debug.Printf("Config structure: %#v\n", *(cfg.Devices["snmp_test.net-snmp.org"]))

	// create driver object and start daemon
	if driver, err := m.NewSnmpDriver(cfg, *broker); err != nil {
		wbgo.Error.Fatalf("can't create driver object: %s", err)
	} else {
		if err := driver.Start(); err != nil {
			wbgo.Error.Fatalf("can't start driver: %s", err)
		} else {
			wbgo.Debug.Println("Work in process")
			// handle SIGINT
			c := make(chan os.Signal, 1)
			signal.Notify(c, os.Interrupt)

			// block until SIGINT received
			<-c
			wbgo.Debug.Println("Termination signal caught, shutting down...")

			// stop driver and exit gracefully
			driver.Stop()
			return
		}
	}
}
