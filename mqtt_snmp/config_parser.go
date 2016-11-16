package mqtt_snmp

import (
    "github.com/alouca/gosnmp"
    "github.com/contactless/wbgo"
    "encoding/json"
    "fmt"
    "io/ioutil"
    "strconv"
    "math"
)


const (
    // Default templates directory
    TemplatesDirectory = "/usr/share/wb-mqtt-snmp/templates"

    // Environmental variable that tells which directories to search
    // for template files. List is colon-separated.
    // For example, "/tmp/templates:/home/user/templates"
    // Default directory will be attached to the end of this list
    // TemplatesEnvVar = "MQTT_SNMP_TEMPLATES_DIR"

    floatEps = 0.00001 // epsilon to compare floats

    //  Default poll interval for channels
    DefaultChannelPollInterval = 1000
)

// Device templates storage type
type deviceTemplatesStorage struct {
    templates map[string]map[string]interface{}
    Valid bool
}

// Load template files from directory
func (tpl *deviceTemplatesStorage) Load(dir string) error {
    files, err := ioutil.ReadDir(dir)

    if err != nil {
        return fmt.Errorf("failed to read templates dir %s: %s", dir, err.Error())
    }

    for _, file := range files {
        data, err := ioutil.ReadFile(file.Name())

        if err == nil {
            return fmt.Errorf("failed to read template file %s: %s", file.Name(), err.Error())
        }

        var jsonData map[string]interface{}

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

    return nil
}

// Initialize raw device entry using template
func (tpl *deviceTemplatesStorage) InitEntry(devType string, entry *map[string]interface{}) error {
    if data, ok := tpl.templates[devType]; ok {
        *entry = data;
    } else {
        return fmt.Errorf("no such template: %s", devType)
    }

    return nil
}

var (
    // Device templates cache
    deviceTpls deviceTemplatesStorage
)

// Channel value converter type
type ValueConverter func(string) string

func AsIs(s string) string { return s; }

func Scale(factor float64) ValueConverter {
    return func(s string) string {
        f, err := strconv.ParseFloat(s, 64)
        if err != nil {
            wbgo.Warn.Printf("can't convert numeric value: %s", s)
            return s
        }

        // skip conversion if scale is 1
        if math.Abs(factor - 1.0) < floatEps {
            return s
        }

        return strconv.FormatFloat(f * factor, 'f', 1, 64)
    }
}


// Final structures
type ChannelConfig struct {
    Name, Oid, ControlType string
    Conv ValueConverter
    PollInterval int
}

type DeviceConfig struct {
    Name, Id, Address, DeviceType string
    SnmpVersion gosnmp.SnmpVersion
    SnmpTimeout int
    Channels []ChannelConfig
}

// Whole daemon configuration structure
type DaemonConfig struct {
    Debug bool
    Devices []DeviceConfig
}

// JSON unmarshaller for DaemonConfig
func (c *DaemonConfig) UnmarshalJSON(raw []byte) error {
    var root struct {
        Debug bool
        Devices []map[string]interface{}
    }

    if err := json.Unmarshal(raw, &root); err != nil {
        return fmt.Errorf("can't parse config JSON file: %s", err.Error())
    }

    c.Debug = root.Debug

    // parse devices config
    return c.parseDevices(root.Devices)
}

// Parse devices list
func (c *DaemonConfig) parseDevices(devs []map[string]interface{}) error {
    // for each element in input slice - create DeviceConfig structure
    for _, value := range devs {
        if err := c.parseDeviceEntry(value); err != nil {
            return err;
        }
    }

    return nil
}

// Try to get name from channel entry
func getNameFromChannelEntry(entry *map[string]interface{}) (name string, err error) {
    err = nil

    if nameEntry, ok := *entry["name"]; ok {
        if name, valid := nameEntry.(string); !valid {
            err = fmt.Errorf("channel name must be string, %T given", nameEntry)
        }
    } else {
        err = fmt.Errorf("no channel name present")
    }
}

// Lay real data over device template
func (c *DaemonConfig) layConfigDataOverTemplate(entry *map[string]interface{}, devConfig *map[string]interface{}) error {
    // rewrite all elements except 'channels'
    for key, value := range *devConfig {
        if key != "channels" {
            *entry[key] = value
        }
    }

    // merge channels
    // check channels list from template
    var channelsList []map[string]interface{}
    if channelsListEntry, ok := *entry["channels"]; ok {
        if channelsList, valid := channelsListEntry.([]map[string]interface{}); !valid {
            return fmt.Errorf("channels list must be array of objects; %T given", channelsListEntry)
        }
    }

    // check channels list from device description
    var devChannelsList []map[string]interface{}
    if devChannelsListEntry, ok := *devConfig; ok {
        if devChannelsList, valid := devChannelsListEntry.([]map[string]interface{}); !valid {
            return fmt.Errorf("channels list must be array of objects; %T given", channelsListEntry)
        }
    }

    // create merging map
    var channelsMap map[string]map[string]interface{}

    createMap := func(l *[]map[string]interface{}, m *map[string]map[string]interface{}) error {
        for _, chanEntry := range *l {
            if name, err := getNameFromChannelEntry(&chanEntry); err == nil {
                *m[name] = chanEntry
            } else {
                return err
            }
        }

        return nil
    }

    if err := createMap(&channelsList, &channelsMap); err != nil {
        return err
    }

    // merge devChannelsMap into channelsMap
    for _, chanEntry := range devChannelsMap {
        // get name
        if name, err := getNameFromChannelEntry(&chanEntry); err == nil {
            // check if this name is present in channel map
            if _, present := channelsMap[name]; present {
                // merge entries
                for n, v := range chanEntry {
                    channelsMap[name][n] = v
                }
            } else {
                // create new entry
                channelsMap[name] = chanEntry
            }
        } else {
            return err
        }
    }


    return nil
}

// Parse single device entry
func (c *DaemonConfig) parseDeviceEntry(devConfig map[string]interface{}) error {

    // Get device type and apply template first
    if !deviceTpls.Valid {
        deviceTpls.Load(TemplatesDirectory)
    }

    // Get device type and apply template to it
    var devType string
    var devEntry map[string]interface{}

    // device_type is optional; if not present, just don't apply template
    if devTypeEntry, ok := devConfig["device_type"]; ok {
        if devType, valid := devTypeEntry.(string); valid {
            deviceTpls.InitEntry(devType, &devEntry)
        } else {
            return fmt.Errorf("device_type must be string, but %T given", devTypeEntry)
        }
    }

    // Lay config data over template
    layConfigDataOverTemplate(&devEntry, &devConfig)

    // Parse whole tree
    d := DeviceConfig{}

    // insert device name
    if devNameEntry, ok := devConfig["name"]; ok {
        if devName, valid := devNameEntry.(string); valid {
            d.Name = devName;
        } else {
            return fmt.Errorf("name must be string, but %T given", devNameEntry)
        }
    } else {
        return fmt.Errorf("name is required in device description")
    }

    return nil
}
