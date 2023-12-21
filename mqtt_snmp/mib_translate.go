package mqtt_snmp

// OID translate module
// Using `snmptranslate` utility from NetSNMP

import (
	"fmt"
	"log"
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
	log.Printf("command to run: snmptranslate %s -On", strings.Join(oids, " "))
	cmd := exec.Command("snmptranslate", append(oids, "-On")...)
	raw_out, err = cmd.Output()

	if err != nil {
		eout, _ := cmd.CombinedOutput()
		err = fmt.Errorf("error translating OIDs: %s", string(eout))
		return
	}

	// parse output of snmptranslate
	out = make(map[string]string)
	split := strings.Split(string(raw_out), "\n\n")

	for i, value := range oids {
		out[value] = strings.Trim(split[i], " \n")
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

	oids_list := make([]string, len(oids_set), len(oids_set)+1) // +1 for TranslateOids (for not to waste time on reallocations)

	i := 0
	for key, _ := range oids_set {
		oids_list[i] = key
		i += 1
	}

	// parse list
	tmap, err := TranslateOids(oids_list)
	if err != nil {
		return err
	}

	// translate OIDs in config
	for dev_key, device := range config.Devices {
		for ch_key, _ := range device.Channels {
			// TODO: it's a Go bullshit' workaround
			tmp := config.Devices[dev_key].Channels[ch_key]
			tmp.Oid = tmap[tmp.Oid]
			config.Devices[dev_key].Channels[ch_key] = tmp
		}
	}

	return nil
}
