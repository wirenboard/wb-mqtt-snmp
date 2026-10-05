package mqtt_snmp

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/contactless/wbgo"
	"github.com/contactless/wbgo/testutils"
	"github.com/gosnmp/gosnmp"
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
			wbgo.Debug.Print("debug mismatch")
		}
		return false
	}

	// check number of workers
	if a.NumWorkers != b.NumWorkers {
		if verbose {
			wbgo.Debug.Print("num_workers mismatch")
		}
		return false
	}

	if len(a.Devices) != len(b.Devices) {
		if verbose {
			wbgo.Debug.Print("devices number mismatch")
		}
		return false
	}

	// check devices map
	for dkey, dvalue := range a.Devices {
		bDvalue, ok := b.Devices[dkey]
		if !ok {
			wbgo.Debug.Printf("device %s doesn't exist in another", dkey)
			return false
		}
		if !deviceConfigsEqualVerbose(dkey, dvalue, bDvalue, verbose) {
			return false
		}
	}

	return true
}

// Check if two DeviceConfig structures are equal including their channels
func deviceConfigsEqualVerbose(dkey string, dvalue, bDvalue *DeviceConfig, verbose bool) bool {
	if len(dvalue.Channels) != len(bDvalue.Channels) {
		wbgo.Debug.Printf("device %s number of channel mismatch", dkey)
		wbgo.Debug.Printf("%d vs %d", len(dvalue.Channels), len(bDvalue.Channels))
		return false
	}

	// check values per-key
	if dvalue.Name != bDvalue.Name ||
		dvalue.Address != bDvalue.Address ||
		dvalue.DeviceType != bDvalue.DeviceType ||
		dvalue.ID != bDvalue.ID ||
		dvalue.Community != bDvalue.Community ||
		dvalue.SnmpV3 != bDvalue.SnmpV3 ||
		dvalue.SnmpTimeout != bDvalue.SnmpTimeout ||
		dvalue.SnmpVersion != bDvalue.SnmpVersion {
		if verbose {
			wbgo.Debug.Printf("device %s configuration mismatch", dkey)
			wbgo.Debug.Printf("%+v", dvalue)
			wbgo.Debug.Print("vs.")
			wbgo.Debug.Printf("%+v", bDvalue)
		}
		return false
	}

	// check channels
	for ckey, cvalue := range dvalue.Channels {
		bCvalue, ok := bDvalue.Channels[ckey]
		if !ok {
			if verbose {
				wbgo.Debug.Printf("device %s channel %s doesn't exist in another", dkey, ckey)
			}
			return false
		}
		if !channelConfigsEqualVerbose(dkey, ckey, cvalue, bCvalue, verbose) {
			return false
		}
	}

	return true
}

// Check if two ChannelConfig structures are equal
func channelConfigsEqualVerbose(dkey, ckey string, cvalue, bCvalue *ChannelConfig, verbose bool) bool {
	// check values per-key
	if cvalue.Name != bCvalue.Name ||
		cvalue.Oid != bCvalue.Oid ||
		cvalue.ControlType != bCvalue.ControlType ||
		cvalue.PollInterval != bCvalue.PollInterval ||
		cvalue.Order != bCvalue.Order {
		if verbose {
			wbgo.Debug.Printf("device %s channel %s configuration mismatch", dkey, ckey)
			wbgo.Debug.Printf("%+v", cvalue)
			wbgo.Debug.Print("vs.")
			wbgo.Debug.Printf("%+v", bCvalue)
		}
		return false
	}

	// check function pointer
	if reflect.ValueOf(cvalue.Conv).Pointer() != reflect.ValueOf(bCvalue.Conv).Pointer() {
		if verbose {
			wbgo.Debug.Printf("device %s channel %s conversion function mismatch", dkey, ckey)
			wbgo.Debug.Printf("%v", reflect.ValueOf(cvalue.Conv))
			wbgo.Debug.Print("vs.")
			wbgo.Debug.Printf("%v", reflect.ValueOf(bCvalue.Conv))
		}
		return false
	}

	// check function param for Scale
	if reflect.ValueOf(cvalue.Conv).Pointer() == reflect.ValueOf(Scale(1)).Pointer() {
		if cvalue.Conv("1") != bCvalue.Conv("1") {
			if verbose {
				wbgo.Debug.Printf("device %s channel %s Scale() function coefficient mismatch", dkey, ckey)
				wbgo.Debug.Printf("%s", cvalue.Conv("1"))
				wbgo.Debug.Print("vs.")
				wbgo.Debug.Printf("%s", bCvalue.Conv("1"))
			}
			return false
		}
	}

	return true
}

func DaemonConfigsEqual(a, b *DaemonConfig) bool {
	return DaemonConfigsEqualVerbose(a, b, false)
}

// Create default templates file just to check if all works fine
func (s *ConfigParserSuite) createDefaultTemplates() error {
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
				"oid": ".1.2.3.4.4",
				"control_type": "value",
				"units": "U"
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
	for name, tpl := range map[string]string{
		"config-type1.json": tpl1,
		"config-type2.json": tpl2,
		"config-type3.json": tpl3,
	} {
		if err := os.WriteFile(name, []byte(tpl), os.ModePerm); err != nil {
			return fmt.Errorf("can't write %s: %w", name, err)
		}
	}
	return nil
}

// Function to run before starting tests
// Creates templates directory and templates themselves
// Temporary templates directory is required to check
// incorrect templates
func (s *ConfigParserSuite) SetupTestFixture(t *testing.T) {
	// create temp dir
	s.tempDir, s.oldDirRm = testutils.SetupTempDir(t)

	wbgo.Debug.Printf("Created test temp dir %s", s.tempDir)

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
			},
			{
				"address": "127.0.0.2",
				"community": "test",
				"device_type": "type2",
				"snmp_version": "2c",
				"poll_interval": 1300,
				"channels": [
					{
						"name": "channel1",
						"enabled": false
					},
					{
						"name": "channel2",
						"poll_interval": 1500
					}
				]
			},
			{
				"address": "192.168.0.1",
				"community": "foo",
				"snmp_version": "2c",
				"enabled": false,
				"channels": [
					{
						"name": "test",
						"oid": ".1.2.3.4.5"
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

	expect := DaemonConfig{
		Debug:      false,
		NumWorkers: DefaultNumWorkers,
		Devices: map[string]*DeviceConfig{
			"snmp_127.0.0.1_test": &DeviceConfig{
				Name:        "SNMP 127.0.0.1_test",
				ID:          "snmp_127.0.0.1_test",
				Address:     "127.0.0.1",
				DeviceType:  "type2",
				Community:   "test",
				SnmpVersion: gosnmp.Version1,
				SnmpTimeout: DefaultSnmpTimeout,
				Channels: map[string]*ChannelConfig{
					"Temperature": &ChannelConfig{
						Name:         "Temperature",
						Oid:          ".1.2.3",
						ControlType:  "value",
						Conv:         Scale(0.1),
						PollInterval: 1000,
						Order:        3,
					},
					"channel1": &ChannelConfig{
						Name:         "channel1",
						Oid:          ".1.2.3.4.4",
						ControlType:  "value",
						Conv:         AsIs,
						PollInterval: 1000,
						Units:        "U",
						Order:        1,
					},
					"channel2": &ChannelConfig{
						Name:         "channel2",
						Oid:          ".1.2.3.4.5",
						ControlType:  "value",
						Conv:         AsIs,
						PollInterval: 500,
						Order:        2,
					},
				},
			},
			"snmp_127.0.0.2_test": &DeviceConfig{
				Name:        "SNMP 127.0.0.2_test",
				ID:          "snmp_127.0.0.2_test",
				Address:     "127.0.0.2",
				DeviceType:  "type2",
				Community:   "test",
				SnmpVersion: gosnmp.Version2c,
				SnmpTimeout: DefaultSnmpTimeout,
				Channels: map[string]*ChannelConfig{
					"channel2": &ChannelConfig{
						Name:         "channel2",
						Oid:          ".1.2.3.4.5",
						ControlType:  "value",
						Conv:         AsIs,
						PollInterval: 1500,
						Order:        1,
					},
				},
			},
		},
	}

	// compare fields
	s.True(DaemonConfigsEqualVerbose(res, &expect, true))
}

// Test OID prefix
func (s *ConfigParserSuite) TestOidPrefix() {
	var res *DaemonConfig
	var err error
	testConfig := `{
		"devices": [{
			"address": "127.0.0.1",
			"oid_prefix": "SNMPv2-MIB",
			"channels": [
				{
					"name": "channel1",
					"oid": "sysDescr.0"
				},
				{
					"name": "channel2",
					"oid": ".1.2.3.4.5"
				},
				{
					"name": "channel3",
					"oid": "DISMAN-EVENT-MIB::sysUpTimeInstance"
				}
			]
		}]
	}`

	r := strings.NewReader(testConfig)

	res, err = NewDaemonConfig(r, ".")
	s.Ck("failed to parse config", err)

	expect := DaemonConfig{
		Debug:      false,
		NumWorkers: DefaultNumWorkers,
		Devices: map[string]*DeviceConfig{
			"snmp_127.0.0.1": &DeviceConfig{
				Name:        "SNMP 127.0.0.1",
				ID:          "snmp_127.0.0.1",
				Address:     "127.0.0.1",
				DeviceType:  "",
				Community:   "",
				OidPrefix:   "SNMPv2-MIB",
				SnmpVersion: gosnmp.Version2c,
				SnmpTimeout: DefaultSnmpTimeout,
				Channels: map[string]*ChannelConfig{
					"channel1": &ChannelConfig{
						Name:         "channel1",
						Oid:          "SNMPv2-MIB::sysDescr.0",
						ControlType:  "value",
						Conv:         AsIs,
						PollInterval: 1000,
						Order:        1,
					},
					"channel2": &ChannelConfig{
						Name:         "channel2",
						Oid:          ".1.2.3.4.5",
						ControlType:  "value",
						Conv:         AsIs,
						PollInterval: 1000,
						Order:        2,
					},
					"channel3": &ChannelConfig{
						Name:         "channel3",
						Oid:          "DISMAN-EVENT-MIB::sysUpTimeInstance",
						ControlType:  "value",
						Conv:         AsIs,
						PollInterval: 1000,
						Order:        3,
					},
				},
			},
		},
	}

	// compare fields
	s.True(DaemonConfigsEqualVerbose(res, &expect, true))
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
	s.Error(err, "config parser doesn't fail on empty devices list")
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
	s.Error(err, "config parser doesn't fail on empty channels list")
}

// Fail on address collision
func (s *ConfigParserSuite) TestAddressCollision() {
	testConfig1 := `{
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

	_, err := NewDaemonConfig(strings.NewReader(testConfig1), ".")
	s.Require().Error(err, "config parser doesn't fail on device address collision")

	// different communities on one address is not an error
	testConfig2 := `{
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

	_, err = NewDaemonConfig(strings.NewReader(testConfig2), ".")
	s.Require().NoError(err, "config parser fail on no device address collision")

	// SNMPv3 devices on one address are told apart by user name, community is ignored
	testConfig3 := `{
		"devices": [
		{
			"address": "127.0.0.1",
			"device_type": "type2",
			"snmp_version": "3",
			"snmp_user": "foo"
		},
		{
			"address": "127.0.0.1",
			"device_type": "type2",
			"snmp_version": "3",
			"snmp_user": "bar"
		}
		]
	}`

	config, err := NewDaemonConfig(strings.NewReader(testConfig3), ".")
	s.Require().NoError(err, "config parser fail on SNMPv3 devices with different users")
	_, foo := config.Devices["snmp_127.0.0.1_foo"]
	_, bar := config.Devices["snmp_127.0.0.1_bar"]
	s.True(foo && bar, "SNMPv3 device IDs are not generated from user names")

	testConfig4 := `{
		"devices": [
		{
			"address": "127.0.0.1",
			"community": "foo",
			"device_type": "type2",
			"snmp_version": "3",
			"snmp_user": "monitor"
		},
		{
			"address": "127.0.0.1",
			"community": "bar",
			"device_type": "type2",
			"snmp_version": "3",
			"snmp_user": "monitor"
		}
		]
	}`

	_, err = NewDaemonConfig(strings.NewReader(testConfig4), ".")
	s.Error(err, "config parser doesn't fail on SNMPv3 devices with the same user")
}

// Fail on malformed address, so the daemon exits as not configured
func (s *ConfigParserSuite) TestInvalidAddress() {
	testConfig := `{
		"devices": [{
			"address": "%s",
			"device_type": "type2"
		}]
	}`

	_, err := NewDaemonConfig(strings.NewReader(fmt.Sprintf(testConfig, "127.0.0.1:abc")), ".")
	s.Require().Error(err, "config parser doesn't fail on malformed device address")

	_, err = NewDaemonConfig(strings.NewReader(fmt.Sprintf(testConfig, "127.0.0.1:1161")), ".")
	s.NoError(err, "config parser fail on device address with port")
}

// Test channel names collision
func (s *ConfigParserSuite) TestChannelsCollision() {
	testConfig1 := `{
		"devices": [{
			"address": "127.0.0.1",
			"community": "foo",
			"channels": [
			{
				"name": "channel1",
				"oid": ".1.2.3.4"
			},
			{
				"name": "channel1",
				"oid": ".1.2.3.4"
			}
			]
		}]
	}`

	var err error
	_, err = NewDaemonConfig(strings.NewReader(testConfig1), ".")
	s.Error(err, "config parser doesn't fail on channel names collision")
}

// Test missing parameters
func (s *ConfigParserSuite) TestMissingParams() {
	var err error

	// missing device address
	testConfigDevAddr := `{
		"devices": [{
			"community": "foo",
			"device_type": "type2"
		}]
	}`

	_, err = NewDaemonConfig(strings.NewReader(testConfigDevAddr), ".")
	s.Require().Error(err, "config parser doesn't fail on device address missing")

	// missing channel name
	testConfigChanName := `{
		"devices": [{
			"address": "127.0.0.1",
			"channels": [{
				"oid": ".1.2.3"
			}]
		}]
	}`

	_, err = NewDaemonConfig(strings.NewReader(testConfigChanName), ".")
	s.Require().Error(err, "config parser doesn't fail on channel name missing")

	// missing channel OID
	testConfigChanOid := `{
		"devices": [{
			"address": "127.0.0.1",
			"channels": [{
				"name": "foo"
			}]
		}]
	}`

	_, err = NewDaemonConfig(strings.NewReader(testConfigChanOid), ".")
	s.Error(err, "config parser doesn't fail on channel OID missing")
}

func TestConfigParser(t *testing.T) {
	s := new(ConfigParserSuite)

	s.SetupTestFixture(t)
	defer s.TearDownTestFixture(t)

	testutils.RunSuites(t, s)
}

func TestSnmpVersions(t *testing.T) {
	for _, tt := range []struct {
		name    string
		fields  string
		version gosnmp.SnmpVersion
		id      string
		err     string
	}{
		{"default", "", gosnmp.Version2c, "snmp_127.0.0.1", ""},
		{"v1", `"snmp_version":"1",`, gosnmp.Version1, "snmp_127.0.0.1", ""},
		{"v2c", `"snmp_version":"2c",`, gosnmp.Version2c, "snmp_127.0.0.1", ""},
		{"v3", `"snmp_version":"3","snmp_user":"monitor",`, gosnmp.Version3, "snmp_127.0.0.1_monitor", ""},
		{"unknown", `"snmp_version":"4",`, 0, "", "SNMP version must be"},
		{"number", `"snmp_version":3,`, 0, "", "snmp_version must be string"},
		{"null", `"snmp_version":null,`, 0, "", "snmp_version must be string"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var config DaemonConfig
			err := json.Unmarshal([]byte(`{"devices":[{`+tt.fields+`"address":"127.0.0.1","channels":[{"name":"test","oid":".1.3.6.1.2.1.1.1.0"}]}]}`), &config)
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("expected %q, got %v", tt.err, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			device, ok := config.Devices[tt.id]
			if !ok {
				t.Fatalf("device %q not found", tt.id)
			}
			if device.SnmpVersion != tt.version {
				t.Fatalf("version = %v, want %v", device.SnmpVersion, tt.version)
			}
		})
	}
}

func TestSnmpTimeout(t *testing.T) {
	for _, tt := range []struct {
		name    string
		fields  string
		timeout int
	}{
		{"default", "", DefaultSnmpTimeout},
		{"explicit", `"snmp_timeout":7,`, 7},
		{"zero", `"snmp_timeout":0,`, DefaultSnmpTimeout},
		{"negative", `"snmp_timeout":-1,`, DefaultSnmpTimeout},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var config DaemonConfig
			err := json.Unmarshal([]byte(`{"devices":[{`+tt.fields+`"address":"127.0.0.1","channels":[{"name":"test","oid":".1.3.6.1.2.1.1.1.0"}]}]}`), &config)
			if err != nil {
				t.Fatal(err)
			}
			if got := config.Devices["snmp_127.0.0.1"].SnmpTimeout; got != tt.timeout {
				t.Fatalf("timeout = %v, want %v", got, tt.timeout)
			}
		})
	}
}

func TestLegacySnmpConfigIgnoresV3Fields(t *testing.T) {
	for _, version := range []string{"", "1", "2c"} {
		t.Run("version="+version, func(t *testing.T) {
			// These fields were previously unknown and ignored. They must not
			// invalidate a community-based device, including a v3 template override.
			device := map[string]any{
				"address": "127.0.0.1", "community": "private",
				"snmp_user": nil, "snmp_security_level": false,
				"snmp_auth_protocol": 123, "snmp_auth_passphrase": nil,
				"snmp_priv_protocol": []any{}, "snmp_priv_passphrase": nil,
				"snmp_context_name": map[string]any{},
				"channels":          []any{map[string]any{"name": "test", "oid": ".1.3.6.1.2.1.1.1.0"}},
			}
			if version != "" {
				device["snmp_version"] = version
			}
			raw, err := json.Marshal(map[string]any{"devices": []any{device}})
			if err != nil {
				t.Fatal(err)
			}
			var config DaemonConfig
			if err = json.Unmarshal(raw, &config); err != nil {
				t.Fatal(err)
			}
			d := config.Devices["snmp_127.0.0.1_private"]
			if d == nil || d.Name != "SNMP 127.0.0.1_private" || d.SnmpV3 != (SnmpV3Config{}) {
				t.Fatal("legacy identity or credentials changed")
			}
			client, err := newGoSNMPConfig(d, false)
			if err != nil {
				t.Fatal(err)
			}
			wantVersion := gosnmp.Version2c
			if version == "1" {
				wantVersion = gosnmp.Version1
			}
			if client.Version != wantVersion || client.Community != "private" || client.SecurityParameters != nil {
				t.Fatal("legacy session settings changed")
			}
		})
	}
}

func TestSnmpTemplateVersionInheritance(t *testing.T) {
	for _, tt := range []struct {
		name            string
		templateVersion string
		override        string
		wantVersion     gosnmp.SnmpVersion
		wantCredential  string
	}{
		{"v1 inherited", "1", "", gosnmp.Version1, "private"},
		{"v2c inherited", "2c", "", gosnmp.Version2c, "private"},
		{"v3 inherited", "3", "", gosnmp.Version3, "monitor"},
		{"v1 overridden", "1", "2c", gosnmp.Version2c, "private"},
		{"v3 overridden", "3", "1", gosnmp.Version1, "private"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			config := DaemonConfig{
				templates: deviceTemplatesStorage{templates: map[string]map[string]any{
					"custom": {
						"snmp_version": tt.templateVersion, "community": "private",
						"snmp_user": "monitor", "snmp_security_level": "authPriv",
						"snmp_auth_protocol": "SHA256", "snmp_auth_passphrase": "auth-password",
						"snmp_priv_protocol": "AES", "snmp_priv_passphrase": "priv-password",
						"channels": []any{map[string]any{"name": "test", "oid": ".1.3.6.1.2.1.1.1.0"}},
					},
				}},
			}
			device := map[string]any{"address": "127.0.0.1", "device_type": "custom"}
			if tt.override != "" {
				device["snmp_version"] = tt.override
			}
			raw, err := json.Marshal(map[string]any{"devices": []any{device}})
			if err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(raw, &config); err != nil {
				t.Fatal(err)
			}
			d := config.Devices["snmp_127.0.0.1_"+tt.wantCredential]
			if d == nil || d.SnmpVersion != tt.wantVersion || d.Community != "private" {
				t.Fatal("incorrect template version, community or identity")
			}
			client, err := newGoSNMPConfig(d, false)
			if err != nil {
				t.Fatal(err)
			}
			if tt.wantVersion == gosnmp.Version3 {
				if client.MsgFlags != gosnmp.AuthPriv || client.SecurityParameters.(*gosnmp.UsmSecurityParameters).AuthenticationPassphrase != "auth-password" {
					t.Fatal("template security settings were overridden by defaults")
				}
			} else if client.SecurityParameters != nil {
				t.Fatal("community-based session uses template USM credentials")
			}
		})
	}
}

func TestSnmpV3Validation(t *testing.T) {
	for _, tt := range []struct {
		name   string
		fields map[string]any
		err    string
	}{
		{"missing user", map[string]any{"snmp_user": ""}, "snmp_user"},
		{"numeric user", map[string]any{"snmp_user": 123}, "snmp_user must be string"},
		{"unknown level", map[string]any{"snmp_security_level": "invalid"}, "snmp_security_level"},
		{"unknown auth", map[string]any{"snmp_auth_protocol": "invalid"}, "snmp_auth_protocol"},
		{"unknown privacy", map[string]any{"snmp_priv_protocol": "invalid"}, "snmp_priv_protocol"},
		{"missing auth", map[string]any{"snmp_security_level": "authNoPriv"}, "snmp_auth_protocol"},
		{"missing auth password", map[string]any{"snmp_security_level": "authNoPriv", "snmp_auth_protocol": "SHA"}, "snmp_auth_passphrase"},
		{"short auth password", map[string]any{"snmp_security_level": "authNoPriv", "snmp_auth_protocol": "SHA", "snmp_auth_passphrase": "short"}, "snmp_auth_passphrase"},
		{"missing privacy", map[string]any{"snmp_security_level": "authPriv", "snmp_auth_protocol": "SHA", "snmp_auth_passphrase": "auth-password"}, "snmp_priv_protocol"},
		{"missing privacy password", map[string]any{"snmp_security_level": "authPriv", "snmp_auth_protocol": "SHA", "snmp_auth_passphrase": "auth-password", "snmp_priv_protocol": "AES"}, "snmp_priv_passphrase"},
		{"auth without level", map[string]any{"snmp_auth_protocol": "SHA", "snmp_auth_passphrase": "auth-password"}, "require authNoPriv or authPriv"},
		{"privacy without auth", map[string]any{"snmp_priv_protocol": "AES"}, "require authPriv"},
		{"privacy with authNoPriv", map[string]any{"snmp_security_level": "authNoPriv", "snmp_auth_protocol": "SHA", "snmp_auth_passphrase": "auth-password", "snmp_priv_protocol": "AES"}, "require authPriv"},
		{"bad context type", map[string]any{"snmp_context_name": true}, "snmp_context_name must be string"},
		{"noAuthNoPriv", map[string]any{}, ""},
		{"explicit noAuthNoPriv", map[string]any{"snmp_security_level": "noAuthNoPriv", "snmp_auth_protocol": "NoAuth", "snmp_priv_protocol": "NoPriv"}, ""},
		{"authNoPriv", map[string]any{"snmp_security_level": "authNoPriv", "snmp_auth_protocol": "MD5", "snmp_auth_passphrase": "auth-password"}, ""},
		{"authPriv", map[string]any{"snmp_security_level": "authPriv", "snmp_auth_protocol": "SHA256", "snmp_auth_passphrase": "auth-password", "snmp_priv_protocol": "AES", "snmp_priv_passphrase": "priv-password"}, ""},
		{"authPriv UTF-8", map[string]any{"snmp_security_level": "authPriv", "snmp_auth_protocol": "SHA256", "snmp_auth_passphrase": "тест", "snmp_priv_protocol": "AES", "snmp_priv_passphrase": "ключ"}, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			device := map[string]any{
				"address": "127.0.0.1", "snmp_version": "3", "snmp_user": "monitor",
				"channels": []map[string]any{{"name": "test", "oid": ".1.3.6.1.2.1.1.1.0"}},
			}
			maps.Copy(device, tt.fields)
			raw, err := json.Marshal(map[string]any{"devices": []any{device}})
			if err != nil {
				t.Fatal(err)
			}
			var config DaemonConfig
			err = json.Unmarshal(raw, &config)
			if tt.err == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tt.err) {
				t.Fatalf("expected %q, got %v", tt.err, err)
			}
			if err != nil && (strings.Contains(err.Error(), "auth-password") || strings.Contains(err.Error(), "priv-password")) {
				t.Fatal("error includes a passphrase")
			}
		})
	}
}

func TestSnmpV3TemplateOverride(t *testing.T) {
	config := DaemonConfig{
		templates: deviceTemplatesStorage{templates: map[string]map[string]any{
			"secure": {
				"snmp_version": "3", "snmp_user": "template-user",
				"snmp_security_level": "authPriv", "snmp_auth_protocol": "SHA256",
				"snmp_auth_passphrase": "template-auth", "snmp_priv_protocol": "AES",
				"snmp_priv_passphrase": "template-priv", "snmp_context_name": "template-context",
				"channels": []any{map[string]any{"name": "test", "oid": ".1.3.6.1.2.1.1.1.0"}},
			},
		}},
	}
	err := json.Unmarshal([]byte(`{"devices":[{"address":"127.0.0.1","device_type":"secure","snmp_user":"monitor","snmp_auth_passphrase":"device-auth","snmp_context_name":"device-context"}]}`), &config)
	if err != nil {
		t.Fatal(err)
	}
	d := config.Devices["snmp_127.0.0.1_monitor"]
	client, err := newGoSNMPConfig(d, false)
	if err != nil {
		t.Fatal(err)
	}
	security := client.SecurityParameters.(*gosnmp.UsmSecurityParameters)
	if client.Version != gosnmp.Version3 || client.SecurityModel != gosnmp.UserSecurityModel || client.MsgFlags != gosnmp.AuthPriv || client.ContextName != "device-context" {
		t.Fatal("incorrect SNMPv3 session settings")
	}
	if security.UserName != "monitor" || security.AuthenticationProtocol != gosnmp.SHA256 || security.AuthenticationPassphrase != "device-auth" || security.PrivacyProtocol != gosnmp.AES || security.PrivacyPassphrase != "template-priv" {
		t.Fatal("incorrect SNMPv3 credentials after template override")
	}
	other, err := newGoSNMPConfig(d, false)
	if err != nil {
		t.Fatal(err)
	}
	if other.SecurityParameters == client.SecurityParameters {
		t.Fatal("SNMPv3 sessions share mutable security parameters")
	}
}
