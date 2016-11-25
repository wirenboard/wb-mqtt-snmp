package mqtt_snmp

// OID translate module
// Using `snmptranslate` utility from NetSNMP

import (
	"fmt"
	"os/exec"
	"strings"
)

// Translates mixed OIDs/names to OIDs
// using local `snmptranslate` utility and, so,
// local installed MIBs
func TranslateOids(oids []string) (out map[string]string, err error) {
	err = nil
	var raw_out []byte

	// call snmptranslate
	raw_out, err = exec.Command("snmptranslate", append(oids, "-On")...).Output()

	if err != nil {
		err = fmt.Errorf("error translating OIDs: %s", err)
		return
	}

	// parse output of snmptranslate
	out = make(map[string]string)
	split := strings.Split(string(raw_out), "\n\n")

	for i, value := range oids {
		out[value] = split[i]
	}

	return
}

// Translate all OIDs in given configuration
func TranslateOidsInDaemonConfig(config *DaemonConfig) error {
	// collect all unique OIDs into list
	oids_set := make(map[string]bool)

	for _, device := range config.Devices {
		for _, channel := range device.Channels {
			oids_set[channel.Oid] = true
		}
	}

	oids_list := make([]string, len(oids_set)+1) // +1 for TranslateOids (for not to waste time on reallocations)

	for key, _ := range oids_set {
		oids_list = append(oids_list, key)
	}

	// parse list
	tmap, err := TranslateOids(oids_list)
	if err != nil {
		return err
	}

	// translate OIDs in config
	for _, device := range config.Devices {
		for _, channel := range device.Channels {
			channel.Oid = tmap[channel.Oid]
		}
	}

	return nil
}
