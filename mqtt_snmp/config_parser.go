package wb-mqtt-snmp

import (
    "github.com/alouca/gosnmp"
    "encoding/json"
    "strconv"
    // "log"
    "math"
)

const (
    floatEps = 0.00001
)

// Channel value converter type
type ValueConverter func(string) string

func AsIs(s string) string { return s; }

func Scale(factor float64) ValueConverter {
    return func(s string) string {
        f, err = strconv.ParseFloat(s, 64)
        if err != nil {
            wbgo.Warn.Printf("can't convert numeric value: %s", s)
            return s
        }

        // skip conversion if scale is 1
        if math.Abs(factor - 1.0) < floatEps {
            return s
        }

        return sntconv.FormatFloat(f * factor, 'f', 1, 64)
    }
}

// Per-channel configuration
// Implements json.Unmarshaler
type ChannelConfig struct {
    Name, Oid, ControlType string
    Conv ValueConverter
    PollInterval int
}

func (ch *ChannelConfig) UnmarshalJSON(data []byte) error {
    var rec struct {
        Name string
        ObjName string `json:"object_name"`
        ControlType string `json:"control_type"`
        Scale float64
        PollInterval int `json:"poll_interval"`
    } = { Scale: 1.0 }

    if err := json.Unmarshal(data, &rec); err != nil {
        return err
    }

    // name is copied 'as is'
    ch.Name = rec.Name

    // OID is copied 'as is' and will be translated later in common order
    ch.Oid = rec.ObjName

    // Control type is also just a copy, but it does matter when Conv is selected
    ch.ControlType = rec.ControlType

    // PollInterval is also copied unchanged
    ch.PollInterval = rec.PollInterval

    if isNumericControlType(ch.ControlType) {
        ch.Conv = Scale(rec.Scale)
    } else {
        ch.Conv = AsIs
    }

    return nil
}

// Per-device configuration
// Implements json.Unmarshaler
type DeviceConfig struct {
    Name, Id, Address string
    SnmpVersion gosnmp.SnmpVersion
    Channels []ChannelConfig
}

// Root object of config file
type ConfigFile struct {
    Debug bool
    Devices []DeviceConfig
    MaxUnchangedInterval int `json:"max_unchanged_interval"`
}
