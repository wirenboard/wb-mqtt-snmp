// Package main is the wb-mqtt-snmp daemon entry point.
package main

import (
	"flag"
	"io"
	"log"
	"net/http"
	_ "net/http/pprof" //nolint:gosec // pprof is served only when -profile is specified
	"os"
	"os/signal"
	"syscall"
	"time"

	m "github.com/wirenboard/wb-mqtt-snmp/mqtt_snmp"
	"github.com/wirenboard/wbgong"
)

const (
	wbgoFile                 = "/usr/lib/wb-mqtt-snmp/wbgo.so"
	profileReadHeaderTimeout = 10 * time.Second
)

func main() {
	broker := flag.String("broker", "unix:///var/run/mosquitto/mosquitto.sock", "MQTT broker URL")
	configFile := flag.String("config", "/etc/wb-mqtt-snmp.conf", "Config file location")
	templatesDir := flag.String("templates", "/usr/share/wb-mqtt-snmp/templates/", "Templates directory")
	debugFlag := flag.Bool("debug", false, "Enable debugging")
	useSyslog := flag.Bool("syslog", false, "Use syslog for logging")
	profile := flag.String("profile", "", "Run pprof server")
	wbgoso := flag.String("wbgo", wbgoFile, "Location of wbgo.so plugin")

	flag.Parse()
	if err := wbgong.Init(*wbgoso); err != nil {
		log.Fatalf("can't initialize wbgo.so: %s", err)
	}

	if *profile != "" {
		go func() {
			server := &http.Server{
				Addr:              *profile,
				ReadHeaderTimeout: profileReadHeaderTimeout,
			}
			wbgong.Debug.Println(server.ListenAndServe())
		}()
	}

	// open config file
	var err error
	var r io.Reader
	if r, err = os.Open(*configFile); err != nil {
		wbgong.Error.Printf("can't open config file %s: %s", *configFile, err)
		os.Exit(6) // EXIT_NOTCONFIGURED, see https://www.freedesktop.org/software/systemd/man/latest/systemd.exec.html#Process_Exit_Codes
	}

	// read config
	var cfg *m.DaemonConfig
	if cfg, err = m.NewDaemonConfig(r, *templatesDir); err != nil {
		wbgong.Error.Printf("error parsing config file %s: %s", *configFile, err)
		os.Exit(6) // EXIT_NOTCONFIGURED, see https://www.freedesktop.org/software/systemd/man/latest/systemd.exec.html#Process_Exit_Codes
	}

	if *useSyslog {
		wbgong.UseSyslog()
	}

	// update debug flag
	cfg.Debug = cfg.Debug || *debugFlag
	wbgong.SetDebuggingEnabled(cfg.Debug)

	// translate OIDs
	if err = m.TranslateOidsInDaemonConfig(cfg); err != nil {
		wbgong.Error.Printf("error translating OIDs: %s", err)
		os.Exit(6) // EXIT_NOTCONFIGURED, see https://www.freedesktop.org/software/systemd/man/latest/systemd.exec.html#Process_Exit_Codes
	}

	// create driver object and start daemon
	if driver, err := m.NewSnmpDriver(cfg, *broker); err != nil {
		wbgong.Error.Fatalf("can't create driver object: %s", err)
	} else {
		if err := driver.Start(); err != nil {
			wbgong.Error.Fatalf("can't start driver: %s", err)
		} else {
			wbgong.Debug.Println("Work in process")
			// handle SIGINT
			c := make(chan os.Signal, 1)
			signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)

			// block until SIGINT received
			<-c
			wbgong.Debug.Println("Termination signal caught, shutting down...")

			// stop driver and exit gracefully
			driver.Stop()
			return
		}
	}
}
