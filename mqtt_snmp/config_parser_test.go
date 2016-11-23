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

// Check if two DaemonConfig structures are equal
func DaemonConfigsEqual(a, b *DaemonConfig) bool {
	// check debug field
	if a.Debug != b.Debug {
		log.Print("debug mismatch")
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
			log.Printf("device %s configuration mismatch", dkey)
			log.Printf("%+v", dvalue)
			log.Print("vs.")
			log.Printf("%+v", b_dvalue)
			return false
		}

		// check channels
		for ckey, cvalue := range a.Devices[dkey].Channels {
			var b_cvalue ChannelConfig

			if b_cvalue, ok = b.Devices[dkey].Channels[ckey]; !ok {
				log.Printf("device %s channel %s doesn't exist in another", dkey, ckey)
				return false
			}

			// check values per-key
			if cvalue.Name != b_cvalue.Name ||
				cvalue.Oid != b_cvalue.Oid ||
				cvalue.ControlType != b_cvalue.ControlType ||
				cvalue.PollInterval != b_cvalue.PollInterval ||
				reflect.ValueOf(cvalue.Conv) != reflect.ValueOf(b_cvalue.Conv) {
				log.Printf("device %s channel %s configuration mismatch", dkey, ckey)
				log.Printf("%+v", cvalue)
				log.Print("vs.")
				log.Printf("%+v", b_cvalue)
				return false
			}
		}
	}

	if len(a.Devices) != len(b.Devices) {
		log.Print("devices number mismatch")
		return false
	}

	return true
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
						"oid": ".1.2.3"
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
			"snmp_127.0.0.1": DeviceConfig{
				Name:        "SNMP 127.0.0.1",
				Id:          "snmp_127.0.0.1",
				Address:     "127.0.0.1",
				DeviceType:  "type2",
				Community:   "test",
				SnmpVersion: gosnmp.Version2c,
				SnmpTimeout: 1000,
				Channels: map[string]ChannelConfig{
					"Temperature": ChannelConfig{
						Name:         "Temperature",
						Oid:          ".1.2.3",
						ControlType:  "value",
						Conv:         AsIs,
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
						PollInterval: 1000,
					},
				},
			},
		},
	}

	// compare fields
	s.Equal(true, DaemonConfigsEqual(res, &expect))
}

func (s *ConfigParserSuite) TestFeatureTwo() {

}

func TestConfigParser(t *testing.T) {
	s := new(ConfigParserSuite)

	s.SetupTestFixture(t)
	defer s.TearDownTestFixture(t)

	testutils.RunSuites(t, s)
}
