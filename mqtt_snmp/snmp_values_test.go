package mqtt_snmp

import (
	"testing"

	"github.com/gosnmp/gosnmp"
)

// Use the library's actual wire decoder to catch changes in Go value types.
func TestConvertDecodedSnmpValues(t *testing.T) {
	for _, tt := range []struct {
		name  string
		typ   gosnmp.Asn1BER
		value any
		want  string
		valid bool
	}{
		{"integer", gosnmp.Integer, -123, "-123", true},
		{"gauge", gosnmp.Gauge32, uint32(4294967295), "4294967295", true},
		{"counter32", gosnmp.Counter32, uint32(4294967295), "4294967295", true},
		{"counter64", gosnmp.Counter64, uint64(18446744073709551615), "18446744073709551615", true},
		{"unsigned", gosnmp.Uinteger32, uint32(2147483647), "2147483647", true},
		{"text", gosnmp.OctetString, []byte("Текст"), "Текст", true},
		{"empty text", gosnmp.OctetString, []byte{}, "", true},
		{"binary", gosnmp.OctetString, []byte{0xff}, "\xff", false},
		{"ip", gosnmp.IPAddress, "192.0.2.1", "192.0.2.1", true},
		{"oid", gosnmp.ObjectIdentifier, ".1.3.6.1.2.1", "", false},
		{"uptime", gosnmp.TimeTicks, uint32(12345), "2m3.45s", true},
		{"max uptime", gosnmp.TimeTicks, uint32(4294967295), "11930h27m52.95s", true},
		{"missing instance", gosnmp.NoSuchInstance, nil, "", false},
		{"missing object", gosnmp.NoSuchObject, nil, "", false},
		{"end of MIB", gosnmp.EndOfMibView, nil, "", false},
		{"null", gosnmp.Null, nil, "", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			packet := &gosnmp.SnmpPacket{
				Version: gosnmp.Version2c, PDUType: gosnmp.GetResponse,
				Variables: []gosnmp.SnmpPDU{{Name: ".1.3.6.1.2.1.1.1.0", Type: tt.typ, Value: tt.value}},
			}
			wire, err := packet.MarshalMsg()
			if err != nil {
				t.Fatal(err)
			}
			decoder := &gosnmp.GoSNMP{}
			decoded, err := decoder.SnmpDecodePacket(wire)
			if err != nil {
				t.Fatal(err)
			}
			got, valid := ConvertSnmpValue(decoded.Variables[0])
			if got != tt.want || valid != tt.valid {
				t.Fatalf("decoded %T: got (%q, %v), want (%q, %v)", decoded.Variables[0].Value, got, valid, tt.want, tt.valid)
			}
		})
	}
}

func TestConvertSnmpValueRejectsWrongTypes(t *testing.T) {
	for _, typ := range []gosnmp.Asn1BER{gosnmp.Integer, gosnmp.Gauge32, gosnmp.Counter32, gosnmp.Counter64, gosnmp.Uinteger32, gosnmp.IPAddress, gosnmp.TimeTicks} {
		if _, valid := ConvertSnmpValue(gosnmp.SnmpPDU{Type: typ, Value: struct{}{}}); valid {
			t.Errorf("accepted incorrect value type for %s", typ)
		}
	}
}

func TestConvertSnmpUnsigned32Max(t *testing.T) {
	value, valid := ConvertSnmpValue(gosnmp.SnmpPDU{Type: gosnmp.Uinteger32, Value: uint32(4294967295)})
	if !valid || value != "4294967295" {
		t.Fatalf("unexpected unsigned value: %q, valid=%v", value, valid)
	}
}
