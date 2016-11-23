package main

import (
	"flag"
	m "github.com/contactless/wb-mqtt-snmp/mqtt_snmp"
	"io"
	"log"
	"os"
)

func main() {

	flag.Parse()

	if flag.NArg() != 1 {
		log.Fatalf("Usage: _____ json_file")
	}

	config_file := flag.Arg(0)

	var err error
	var r io.Reader
	if r, err = os.Open(config_file); err != nil {
		log.Fatalf("Can't read file %s: %s", config_file, err.Error())
	}

	var cfg *m.DaemonConfig
	if cfg, err = m.NewDaemonConfig(r, "../templates/"); err != nil {
		log.Fatalf("Error parsing JSON file %s: %s", config_file, err)
	}

	log.Printf("Config: %+v", *cfg)
}
