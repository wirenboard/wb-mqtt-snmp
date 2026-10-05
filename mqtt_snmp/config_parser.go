// Package mqtt_snmp implements the SNMP to MQTT bridge driver.
package mqtt_snmp

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/contactless/wbgo"
	"github.com/gosnmp/gosnmp"
)

const (
	// TemplatesDirectory is the default templates directory
	// TemplatesDirectory = "/usr/share/wb-mqtt-snmp/templates"
	TemplatesDirectory = "./templates"

	// TemplatesFileMask is the template file regexp
	TemplatesFileMask = "config-.*\\.json"

	// DefaultChannelPollInterval is the default poll interval for channels (ms)
	DefaultChannelPollInterval = 1000

	// DefaultChannelControlType is the default channel control type
	DefaultChannelControlType = "value"

	// DefaultSnmpVersion is the default SNMP version
	DefaultSnmpVersion = gosnmp.Version2c

	// DefaultSnmpTimeout is the default SNMP timeout (s)
	DefaultSnmpTimeout = 5

	// DefaultNumWorkers is the default number of workers
	DefaultNumWorkers = 4

	floatEps = 0.00001 // epsilon to compare floats
)

var templatesFileRegexp = regexp.MustCompile(TemplatesFileMask)

// Device templates storage type
type deviceTemplatesStorage struct {
	templates map[string]map[string]any
	Valid     bool
}

// Load template files from directory
func (tpl *deviceTemplatesStorage) Load(dir string) error {

	if tpl.Valid {
		return nil // templates are already loaded
	}

	fsys := os.DirFS(dir)
	files, err := fs.ReadDir(fsys, ".")

	if err != nil {
		return fmt.Errorf("failed to read templates dir %s: %s", dir, err.Error())
	}

	tpl.templates = make(map[string]map[string]any)

	for _, file := range files {
		// skip files which don't match regexp
		if !templatesFileRegexp.MatchString(file.Name()) {
			continue
		}

		data, err := fs.ReadFile(fsys, file.Name())

		if err != nil {
			return fmt.Errorf("failed to read template file %s: %s", file.Name(), err.Error())
		}

		var jsonData map[string]any

		if err := json.Unmarshal(data, &jsonData); err != nil {
			return fmt.Errorf("failed to parse JSON in template file %s: %s", file.Name(), err.Error())
		}

		if devTypeEntry, ok := jsonData["device_type"]; ok {
			if devType, valid := devTypeEntry.(string); valid {
				tpl.templates[devType] = jsonData
			} else {
				return fmt.Errorf("template error: device_type must be string in %s", file.Name())
			}
		} else {
			return fmt.Errorf("template error: device_type is not present in %s", file.Name())
		}
	}

	tpl.Valid = true

	return nil
}

// Initialize raw device entry using template
func (tpl *deviceTemplatesStorage) InitEntry(devType string, entry map[string]any) error {
	if data, ok := tpl.templates[devType]; ok {
		maps.Copy(entry, data)
	} else {
		return fmt.Errorf("no such template: %s", devType)
	}

	return nil
}

// ValueConverter is a channel value converter type
type ValueConverter func(string) string

// AsIs returns value unchanged
func AsIs(s string) string { return s }

// Scale returns converter which multiplies numeric value by factor
func Scale(factor float64) ValueConverter {
	return func(s string) string {
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			wbgo.Warn.Printf("can't convert numeric value: %s", s)
			return s
		}

		// skip conversion if scale is 1
		if math.Abs(factor-1.0) < floatEps {
			return s
		}

		return strconv.FormatFloat(f*factor, 'f', 1, 64)
	}
}

// Check if control type is numeric
func isNumericControlType(ctype string) bool {
	return ctype != "text"
}

// ChannelConfig is a final channel configuration
type ChannelConfig struct {
	Name, Oid, ControlType, Units string
	Conv                          ValueConverter
	PollInterval                  int
	Order                         int
	Device                        *DeviceConfig
}

// DeviceConfig is a final device configuration
type DeviceConfig struct {
	Name, ID, Address, DeviceType, Community string
	OidPrefix                                string
	SnmpVersion                              gosnmp.SnmpVersion
	SnmpTimeout                              int
	SnmpV3                                   SnmpV3Config
	PollInterval                             int

	// Channels is map from channel names
	Channels map[string]*ChannelConfig
}

// GenerateID builds a device ID from address and community string (SNMPv1/v2c) or user name (SNMPv3).
func (d *DeviceConfig) GenerateID() string {
	credential := d.Community
	if d.SnmpVersion == gosnmp.Version3 {
		credential = d.SnmpV3.UserName
	}
	if credential != "" {
		return d.Address + "_" + credential
	}
	return d.Address
}

// DaemonConfig is a whole daemon configuration structure
type DaemonConfig struct {
	Debug      bool
	NumWorkers int
	templates  deviceTemplatesStorage

	// Devices storage is map from device IDs
	Devices map[string]*DeviceConfig
}

// LoadTemplates loads templates from directory into DaemonConfig storage
func (c *DaemonConfig) LoadTemplates(path string) (err error) {
	err = c.templates.Load(path)
	return
}

// NewDaemonConfig generates daemon config from input stream and directory with templates
func NewDaemonConfig(input io.Reader, templatesDir string) (config *DaemonConfig, err error) {
	config = &DaemonConfig{}
	if err = config.LoadTemplates(templatesDir); err != nil {
		return
	}

	err = json.NewDecoder(input).Decode(config)

	return
}

// NewEmptyDeviceConfig makes empty device config, fills it with
// default configuration values such as SnmpVersion and SnmpTimeout
func NewEmptyDeviceConfig() *DeviceConfig {
	return &DeviceConfig{DeviceType: "", Community: "", SnmpVersion: DefaultSnmpVersion, SnmpTimeout: DefaultSnmpTimeout, OidPrefix: "", PollInterval: DefaultChannelPollInterval}
}

// NewEmptyChannelConfig makes empty channel config
func NewEmptyChannelConfig() *ChannelConfig {
	return &ChannelConfig{ControlType: DefaultChannelControlType, Conv: AsIs, PollInterval: DefaultChannelPollInterval, Units: "", Order: 0}
}

// UnmarshalJSON is a JSON unmarshaller for DaemonConfig
func (c *DaemonConfig) UnmarshalJSON(raw []byte) error {
	var root struct {
		Debug      bool
		NumWorkers int `json:"num_workers"`
		Devices    []map[string]any
	}

	root.NumWorkers = DefaultNumWorkers

	if err := json.Unmarshal(raw, &root); err != nil {
		return fmt.Errorf("can't parse config JSON file: %s", err.Error())
	}

	c.Debug = root.Debug
	c.NumWorkers = root.NumWorkers
	c.Devices = make(map[string]*DeviceConfig)

	// parse devices config
	return c.parseDevices(root.Devices)
}

// Copy raw any data from map to string
func copyString(fromMap map[string]any, key string, to *string, required bool) error {
	if entry, ok := fromMap[key]; ok {
		if val, valid := entry.(string); valid {
			*to = val
		} else {
			return fmt.Errorf("%s must be string, but %T given", key, entry)
		}
	} else {
		if required {
			return fmt.Errorf("%s is not present", key)
		}
	}

	return nil
}

// Copy raw any data from map to int
func copyInt(fromMap map[string]any, key string, to *int, required bool) error {
	if entry, ok := fromMap[key]; ok {
		if val, valid := entry.(float64); valid {
			*to = int(val)
		} else {
			return fmt.Errorf("%s must be float64, but %T given", key, entry)
		}
	} else {
		if required {
			return fmt.Errorf("%s is not present", key)
		}
	}

	return nil
}

// Copy raw any data from map to SnmpVersion
func copySnmpVersion(fromMap map[string]any, key string, to *gosnmp.SnmpVersion, required bool) error {
	if entry, ok := fromMap[key]; ok {
		if val, valid := entry.(string); valid {
			switch val {
			case "1":
				*to = gosnmp.Version1
			case "2c":
				*to = gosnmp.Version2c
			case "3":
				*to = gosnmp.Version3
			default:
				return fmt.Errorf("SNMP version must be 1, 2c or 3, %s given", val)
			}
		} else {
			return fmt.Errorf("%s must be string, but %T given", key, entry)
		}
	} else {
		if required {
			return fmt.Errorf("%s is not present", key)
		}
	}

	return nil
}

// Copy raw any data from map to float64
func copyFloat64(fromMap map[string]any, key string, to *float64, required bool) error {
	if entry, ok := fromMap[key]; ok {
		if val, valid := entry.(float64); valid {
			*to = val
		} else {
			return fmt.Errorf("%s must be number, but %T given", key, entry)
		}
	} else {
		if required {
			return fmt.Errorf("%s is not present", key)
		}
	}

	return nil
}

func (c *DaemonConfig) checkEnabled(channel map[string]any) (bool, error) {
	enabled := true

	// check if channel is enabled
	if enableEntry, ok := channel["enabled"]; ok {
		if enableValue, valid := enableEntry.(bool); valid {
			if !enableValue {
				enabled = false
			}
		} else {
			return false, fmt.Errorf("'enabled' must be bool, %T given", enableEntry)
		}
	} // if 'enable' is not presented, think that device is enabled by default

	return enabled, nil
}

// Parse devices list
func (c *DaemonConfig) parseDevices(devs []map[string]any) error {
	if len(devs) == 0 {
		return fmt.Errorf("devices list is empty")
	}

	// for each element in input slice - create DeviceConfig structure
	for _, value := range devs {
		if err := c.parseDeviceEntry(value); err != nil {
			return err
		}
	}

	return nil
}

// Try to get name from channel entry
func getNameFromEntry(entry map[string]any) (name string, err error) {
	err = nil

	var valid bool

	if nameEntry, ok := entry["name"]; ok {
		if name, valid = nameEntry.(string); !valid {
			err = fmt.Errorf("channel name must be string, %T given", nameEntry)
		}
	} else {
		err = fmt.Errorf("no channel name present")
	}

	return
}

// Lay real data over device template
func (c *DaemonConfig) layConfigDataOverTemplate(tplEntry, devEntry map[string]any) error {
	// rewrite all elements except 'channels'
	for key, value := range devEntry {
		if key != "channels" {
			tplEntry[key] = value
		}
	}

	// merge channels
	// check channels list from template
	var tplChannelsList []any
	var ok, valid bool
	var tplChannelsListEntry, devChannelsListEntry any

	if tplChannelsListEntry, ok = tplEntry["channels"]; ok {
		if tplChannelsList, valid = tplChannelsListEntry.([]any); !valid {
			return fmt.Errorf("channels list must be array of objects; %T given", tplChannelsListEntry)
		}
	}

	// check channels list from device description
	var devChannelsList []any
	if devChannelsListEntry, ok = devEntry["channels"]; ok {
		if devChannelsList, valid = devChannelsListEntry.([]any); !valid {
			return fmt.Errorf("channels list must be array of objects; %T given", devChannelsListEntry)
		}
	}

	// create merging map
	tplChannelsMap := make(map[string]map[string]any)
	devChannelsMap := make(map[string]map[string]any)

	// this also returns list of channel names in original order (or error)
	createMap := func(l []any, m map[string]map[string]any) ([]string, error) {
		names := make([]string, 0, 10)

		for _, chanEntry := range l {
			channel, valid := chanEntry.(map[string]any)
			if !valid {
				return nil, fmt.Errorf("channel config must be object, %T given", chanEntry)
			}
			name, err := getNameFromEntry(channel)
			if err != nil {
				return nil, err
			}

			// check name collision first
			if _, ok := m[name]; ok {
				return nil, fmt.Errorf("channel name collision: %s", name)
			}

			m[name] = channel
			names = append(names, name)
		}

		return names, nil
	}

	var tplChannelNames, devChannelNames []string
	var err error

	if tplChannelNames, err = createMap(tplChannelsList, tplChannelsMap); err != nil {
		return fmt.Errorf("template error: %s", err)
	}

	if devChannelNames, err = createMap(devChannelsList, devChannelsMap); err != nil {
		return fmt.Errorf("config error: %s", err)
	}

	// merge devChannelsMap into tplChannelsMap
	for name, channel := range devChannelsMap {
		// check if this name is present in channel map
		if _, present := tplChannelsMap[name]; present {
			// merge entries
			maps.Copy(tplChannelsMap[name], channel)
		} else {
			// create new entry
			tplChannelsMap[name] = channel
		}
	}

	// remove disabled channels
	for name := range tplChannelsMap {
		if enabled, err := c.checkEnabled(tplChannelsMap[name]); err != nil {
			return err
		} else if !enabled {
			delete(tplChannelsMap, name)
		}
	}

	// create order map
	order := make(map[string]float64)
	currentOrder := 1

	// channels from template are going first
	for _, name := range tplChannelNames {
		if _, ok := tplChannelsMap[name]; ok {
			order[name] = float64(currentOrder) // XXX: this is dirty, don't try this at home
			currentOrder++
		}
	}

	// then append names from config if not present yet
	for _, name := range devChannelNames {
		if _, ok := order[name]; !ok {
			if _, ok := tplChannelsMap[name]; ok {
				order[name] = float64(currentOrder) // XXX: this is dirty, don't try this at home
				currentOrder++
			}
		}
	}

	// add order entries to all channels
	for name := range tplChannelsMap {
		tplChannelsMap[name]["order"] = order[name]
	}

	// expose channelsMap to entry
	chanList := make([]map[string]any, len(tplChannelsMap))
	i := 0
	for _, value := range tplChannelsMap {
		chanList[i] = value
		i++
	}

	tplEntry["channels"] = chanList

	return nil
}

// Parse single device entry
func (c *DaemonConfig) parseDeviceEntry(devConfig map[string]any) error {

	// Check if device is enabled and skip if not
	if enableEntry, ok := devConfig["enabled"]; ok {
		if enableValue, valid := enableEntry.(bool); valid {
			if !enableValue {
				return nil // device is disabled, nothing to do here
			}
		} else {
			return fmt.Errorf("'enable' must be bool, %T given", enableEntry)
		}
	} // if 'enable' is not presented, think that device is enabled by default

	// Get device type and apply template to it
	var devType string
	devEntry := make(map[string]any)
	var valid bool

	// device_type is optional; if not present, just don't apply template
	if devTypeEntry, ok := devConfig["device_type"]; ok {
		if devType, valid = devTypeEntry.(string); valid {
			if err := c.templates.InitEntry(devType, devEntry); err != nil {
				return err
			}
		} else {
			return fmt.Errorf("device_type must be string, but %T given", devTypeEntry)
		}
	}

	// Lay config data over template
	if err := c.layConfigDataOverTemplate(devEntry, devConfig); err != nil {
		return err
	}

	// Parse whole tree
	d := NewEmptyDeviceConfig()

	// insert entries in a hard way
	// address field is required
	if err := copyString(devEntry, "address", &(d.Address), true); err != nil {
		return err
	}
	if _, _, err := snmpAddress(d.Address); err != nil {
		return err
	}

	if err := copyString(devEntry, "community", &(d.Community), false); err != nil {
		return err
	}
	if err := copySnmpVersion(devEntry, "snmp_version", &(d.SnmpVersion), false); err != nil {
		return err
	}
	if d.SnmpVersion == gosnmp.Version3 {
		if err := d.SnmpV3.parse(devEntry); err != nil {
			return err
		}
	}

	// fill default values
	d.Name = "SNMP " + d.GenerateID()
	d.ID = "snmp_" + d.GenerateID()

	// check address collision
	if _, ok := c.Devices[d.ID]; ok {
		return fmt.Errorf("device address collision on %s", d.ID)
	}

	if err := copyString(devEntry, "name", &(d.Name), false); err != nil {
		return err
	}
	if err := copyString(devEntry, "id", &(d.ID), false); err != nil {
		return err
	}
	if err := copyString(devEntry, "device_type", &(d.DeviceType), false); err != nil {
		return err
	}
	if d.SnmpVersion == gosnmp.Version3 {
		if _, _, err := d.SnmpV3.securityParameters(); err != nil {
			return fmt.Errorf("invalid SNMPv3 configuration for %s: %w", d.ID, err)
		}
	}
	if err := copyInt(devEntry, "snmp_timeout", &(d.SnmpTimeout), false); err != nil {
		return err
	}
	// gosnmp fails every request at once with a zero timeout
	if d.SnmpTimeout <= 0 {
		d.SnmpTimeout = DefaultSnmpTimeout
	}
	if err := copyString(devEntry, "oid_prefix", &(d.OidPrefix), false); err != nil {
		return err
	}
	if err := copyInt(devEntry, "poll_interval", &(d.PollInterval), false); err != nil {
		return err
	}

	d.Channels = make(map[string]*ChannelConfig)

	// parse channels
	channelsEntry, ok := devEntry["channels"]
	if !ok {
		return fmt.Errorf("channels list is not present for %s", d.ID)
	}
	channels, valid := channelsEntry.([]map[string]any)
	if !valid {
		return fmt.Errorf("channels list in %s must be array of objects, %T given", d.ID, channelsEntry)
	}
	if err := d.parseChannels(channels); err != nil {
		return fmt.Errorf("channel parse error in %s: %s", d.ID, err)
	}

	// append device to storage
	c.Devices[d.ID] = d

	return nil
}

// Parse channels list
func (d *DeviceConfig) parseChannels(chans []map[string]any) error {
	// for each element in input slice - create ChannelConfig structure and append to DeviceConfig
	if len(chans) == 0 {
		return fmt.Errorf("channels list is empty for %s", d.ID)
	}

	for _, value := range chans {
		if err := d.parseChannelEntry(value); err != nil {
			return err
		}
	}

	return nil
}

// Parse single channel entry
func (d *DeviceConfig) parseChannelEntry(channel map[string]any) error {

	// create channel config struct
	c := NewEmptyChannelConfig()

	// fill channel config
	//
	// name is required
	if err := copyString(channel, "name", &(c.Name), true); err != nil {
		return err
	}

	// oid is required
	if err := copyString(channel, "oid", &(c.Oid), true); err != nil {
		return err
	}

	// process OID prefix
	// only if it is defined, name is from MIB and there's no prefix in name
	if d.OidPrefix != "" && c.Oid[0] != '.' && !strings.Contains(c.Oid, "::") {
		c.Oid = d.OidPrefix + "::" + c.Oid
	}

	// control type is optional
	if err := copyString(channel, "control_type", &(c.ControlType), false); err != nil {
		return err
	}

	// converter is an optional function depends on control type
	// now scale function is presented only
	if _, ok := channel["scale"]; ok {
		if isNumericControlType(c.ControlType) {
			var scale float64
			if err := copyFloat64(channel, "scale", &scale, false); err != nil {
				return err
			}
			c.Conv = Scale(scale)
		} else {
			wbgo.Warn.Println("scale could be applied only to numeric control type")
		}
	}

	// poll interval is optional
	c.PollInterval = d.PollInterval
	if err := copyInt(channel, "poll_interval", &(c.PollInterval), false); err != nil {
		return err
	}

	// units is optional and works only for control_type == value
	if err := copyString(channel, "units", &(c.Units), false); err != nil {
		return err
	}

	if c.Units != "" && c.ControlType != "value" {
		wbgo.Warn.Println("units given for non-'value' channel ", c.Name, ", skipping it")
		c.Units = ""
	}

	// add order
	if err := copyInt(channel, "order", &(c.Order), true); err != nil {
		return err
	}

	// append channel config to device
	c.Device = d
	d.Channels[c.Name] = c

	return nil
}
