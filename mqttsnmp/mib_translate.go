package mqttsnmp

// OID translate module
// Using `snmptranslate` utility from NetSNMP

import (
	"fmt"
	"log"
	"os/exec"
	"strings"
)

// TranslateOids translates mixed OIDs/names to OIDs
// using local `snmptranslate` utility and, so,
// local installed MIBs
func TranslateOids(oids []string) (out map[string]string, err error) {
	err = nil
	var rawOut []byte

	// call snmptranslate
	log.Printf("command to run: snmptranslate %#v -On", oids)
	cmd := exec.Command("snmptranslate", append(oids, "-On")...)
	rawOut, err = cmd.Output()

	if err != nil {
		eout, _ := cmd.CombinedOutput()
		err = fmt.Errorf("error translating OIDs: %s", string(eout))
		return
	}

	// parse output of snmptranslate
	out = make(map[string]string)
	split := strings.Split(string(rawOut), "\n\n")

	for i, value := range oids {
		out[value] = strings.Trim(split[i], " \n")
	}

	return
}

// TranslateOidsInDaemonConfig translates all OIDs in given configuration
func TranslateOidsInDaemonConfig(config *DaemonConfig) error {
	// collect all unique OIDs into list
	oidsSet := make(map[string]bool)

	for _, device := range config.Devices {
		for _, channel := range device.channels {
			oidsSet[channel.Oid] = true
		}
	}

	oidsList := make([]string, len(oidsSet), len(oidsSet)+1) // +1 for TranslateOids (for not to waste time on reallocations)

	i := 0
	for key := range oidsSet {
		oidsList[i] = key
		i++
	}

	// parse list
	tmap, err := TranslateOids(oidsList)
	if err != nil {
		return err
	}

	// translate OIDs in config
	for devKey, device := range config.Devices {
		for chKey := range device.channels {
			// TODO: it's a Go bullshit' workaround
			tmp := config.Devices[devKey].channels[chKey]
			tmp.Oid = tmap[tmp.Oid]
			config.Devices[devKey].channels[chKey] = tmp
		}
	}

	return nil
}
