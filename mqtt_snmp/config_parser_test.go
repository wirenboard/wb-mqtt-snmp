package mqtt_snmp

import (
	"github.com/alouca/gosnmp"
	"github.com/contactless/wbgo/testutils"
	"io/ioutil"
	"log"
	"os"
	"reflect"
	"strings"
	"testing"
)

const (
	// Testing templates directory
	templatesDirectory = "./test-templates"
)

type ConfigParserSuite struct {
	testutils.Suite

	tempDir  string
	oldDirRm func()
}

// Check if two DaemonConfig structures are equal (verbose version)
func DaemonConfigsEqualVerbose(a, b *DaemonConfig, verbose bool) bool {
	// check debug field
	if a.Debug != b.Debug {
		if verbose {
			log.Print("debug mismatch")
		}
		return false
	}

	if len(a.Devices) != len(b.Devices) {
		if verbose {
			log.Print("devices number mismatch")
		}
		return false
	}

	// check devices map
	for dkey, dvalue := range a.Devices {
		var b_dvalue DeviceConfig
		var ok bool

		if b_dvalue, ok = b.Devices[dkey]; !ok {
			log.Printf("device %s doesn't exist in another", dkey)
			return false
		}

		if len(a.Devices[dkey].Channels) != len(b.Devices[dkey].Channels) {
			log.Printf("device %s number of channel mismatch", dkey)
			return false
		}

		// check values per-key
		if dvalue.Name != b_dvalue.Name ||
			dvalue.Address != b_dvalue.Address ||
			dvalue.DeviceType != b_dvalue.DeviceType ||
			dvalue.Id != b_dvalue.Id ||
			dvalue.Community != b_dvalue.Community ||
			dvalue.SnmpTimeout != b_dvalue.SnmpTimeout ||
			dvalue.SnmpVersion != b_dvalue.SnmpVersion {
			if verbose {
				log.Printf("device %s configuration mismatch", dkey)
				log.Printf("%+v", dvalue)
				log.Print("vs.")
				log.Printf("%+v", b_dvalue)
			}
			return false
		}

		// check channels
		for ckey, cvalue := range a.Devices[dkey].Channels {
			var b_cvalue ChannelConfig

			if b_cvalue, ok = b.Devices[dkey].Channels[ckey]; !ok {
				if verbose {
					log.Printf("device %s channel %s doesn't exist in another", dkey, ckey)
				}
				return false
			}

			// check values per-key
			if cvalue.Name != b_cvalue.Name ||
				cvalue.Oid != b_cvalue.Oid ||
				cvalue.ControlType != b_cvalue.ControlType ||
				cvalue.PollInterval != b_cvalue.PollInterval {
				if verbose {
					log.Printf("device %s channel %s configuration mismatch", dkey, ckey)
					log.Printf("%+v", cvalue)
					log.Print("vs.")
					log.Printf("%+v", b_cvalue)
				}
				return false
			}

			// check function pointer
			if reflect.ValueOf(cvalue.Conv).Pointer() != reflect.ValueOf(b_cvalue.Conv).Pointer() {
				if verbose {
					log.Printf("device %s channel %s convertion function mismatch", dkey, ckey)
					log.Printf("%v", reflect.ValueOf(cvalue.Conv))
					log.Print("vs.")
					log.Printf("%v", reflect.ValueOf(b_cvalue.Conv))
				}
				return false
			}

			// check function param for Scale
			if reflect.ValueOf(cvalue.Conv).Pointer() == reflect.ValueOf(Scale(1)).Pointer() {
				if cvalue.Conv("1") != b_cvalue.Conv("1") {
					if verbose {
						log.Printf("device %s channel %s Scale() function coefficient mismatch", dkey, ckey)
						log.Printf("%s", cvalue.Conv("1"))
						log.Print("vs.")
						log.Printf("%s", b_cvalue.Conv("1"))
					}
					return false
				}
			}
		}
	}

	return true
}

func DaemonConfigsEqual(a, b *DaemonConfig) bool {
	return DaemonConfigsEqualVerbose(a, b, false)
}

// Create default templates file just to check if all works fine
func (s *ConfigParserSuite) createDefaultTemplates() (err error) {

	// let us start from 3 basic templates
	tpl1 := `{
		"device_type": "type1",
		"snmp_version": "1"
	}`

	tpl2 := `{
		"device_type": "type2",
		"community": "test",
		"snmp_version": "1",
		"channels": [
			{
				"name": "channel1",
				"oid": ".1.2.3.4.4"
			},
			{
				"name": "channel2",
				"oid": ".1.2.3.4.5"
			}
		]
	}`

	tpl3 := `{
		"device_type": "type3",
		"community": "demo",
		"channels": [
			{
				"name": "channel1",
				"oid": ".2.3.4.5",
				"poll_interval": 1234
			}
		]
	}`

	// write these templates into separate files in current dir (which is
	// temp dir already)
	if err = ioutil.WriteFile("config-type1.json", []byte(tpl1), os.ModePerm); err != nil {
		return
	}
	if err = ioutil.WriteFile("config-type2.json", []byte(tpl2), os.ModePerm); err != nil {
		return
	}
	if err = ioutil.WriteFile("config-type3.json", []byte(tpl3), os.ModePerm); err != nil {
		return
	}

	return
}

// Function to run before starting tests
// Creates templates directory and templates themselves
// Temporary templates directory is required to check
// incorrect templates
func (s *ConfigParserSuite) SetupTestFixture(t *testing.T) {
	// create temp dir
	s.tempDir, s.oldDirRm = testutils.SetupTempDir(t)

	log.Printf("Created test temp dir %s", s.tempDir)

	s.Ck("can't create default templates", s.createDefaultTemplates())
}

// Function to run after tests
func (s *ConfigParserSuite) TearDownTestFixture(t *testing.T) {
	s.oldDirRm()
}

func (s *ConfigParserSuite) SetupTest() {
	s.Suite.SetupTest()
}

func (s *ConfigParserSuite) TearDownTest() {
	s.Suite.TearDownTest()
}

// Check correct configuration file
func (s *ConfigParserSuite) TestSimpleFile() {
	testConfig := `{
		"debug": false,
		"devices": [
			{
				"address": "127.0.0.1",
				"community": "test",
				"device_type": "type2",
				"channels": [
					{
						"name": "Temperature",
						"oid": ".1.2.3",
						"control_type": "value",
						"scale": 0.1
					},
					{
						"name": "channel2",
						"poll_interval": 500
					}
				]
			}
		]
	}`

	r := strings.NewReader(testConfig)

	var res *DaemonConfig
	var err error
	res, err = NewDaemonConfig(r, "./")
	s.Ck("failed to parse config", err)

	// log.Printf("%+v", res)
	expect := DaemonConfig{
		Debug: false,
		Devices: map[string]DeviceConfig{
			"snmp_127.0.0.1_test": DeviceConfig{
				Name:        "SNMP 127.0.0.1_test",
				Id:          "snmp_127.0.0.1_test",
				Address:     "127.0.0.1",
				DeviceType:  "type2",
				Community:   "test",
				SnmpVersion: gosnmp.Version1,
				SnmpTimeout: 1000,
				Channels: map[string]ChannelConfig{
					"Temperature": ChannelConfig{
						Name:         "Temperature",
						Oid:          ".1.2.3",
						ControlType:  "value",
						Conv:         Scale(0.1),
						PollInterval: 1000,
					},
					"channel1": ChannelConfig{
						Name:         "channel1",
						Oid:          ".1.2.3.4.4",
						ControlType:  "value",
						Conv:         AsIs,
						PollInterval: 1000,
					},
					"channel2": ChannelConfig{
						Name:         "channel2",
						Oid:          ".1.2.3.4.5",
						ControlType:  "value",
						Conv:         AsIs,
						PollInterval: 500,
					},
				},
			},
		},
	}

	// compare fields
	s.Equal(true, DaemonConfigsEqualVerbose(res, &expect, true))
}

//
// Test skipped parameters

// Fail on empty devices list
func (s *ConfigParserSuite) TestNoDevices() {
	// skip `devices` section
	testConfig := `{
		"debug": true
	}`

	_, err := NewDaemonConfig(strings.NewReader(testConfig), ".")
	s.Error(err, "config parser don't fail on empty devices list")
}

// Fail on empty channels list
func (s *ConfigParserSuite) TestNoChannels() {
	testConfig := `{
		"debug": false,
		"devices": [{
			"address": "127.0.0.1"
		}]
	}`

	_, err := NewDaemonConfig(strings.NewReader(testConfig), ".")
	s.Error(err, "config parser don't fail on empty channels list")
}

// Fail on address collision
func (s *ConfigParserSuite) TestAddressCollision() {
	testConfig_1 := `{
		"devices": [
		{
			"address": "127.0.0.1",
			"device_type": "type2"
		},
		{
			"address": "127.0.0.1",
			"device_type": "type2"
		}
		]
	}`

	_, err := NewDaemonConfig(strings.NewReader(testConfig_1), ".")
	s.Error(err, "config parser don't fail on device address collision")

	// different communities on one address is not an error
	testConfig_2 := `{
		"devices": [
		{
			"address": "127.0.0.1",
			"community": "foo",
			"device_type": "type2"
		},
		{
			"address": "127.0.0.1",
			"community": "bar",
			"device_type": "type2"
		}
		]
	}`

	_, err = NewDaemonConfig(strings.NewReader(testConfig_2), ".")
	s.NoError(err, "config parser fail on no device address collision")
}

func TestConfigParser(t *testing.T) {
	s := new(ConfigParserSuite)

	s.SetupTestFixture(t)
	defer s.TearDownTestFixture(t)

	testutils.RunSuites(t, s)
}
