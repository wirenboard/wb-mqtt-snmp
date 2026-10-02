package mqtt_snmp

import (
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/gosnmp/gosnmp"
)

func TestSnmpAddress(t *testing.T) {
	for _, tt := range []struct {
		address, target string
		port            uint16
	}{
		{"127.0.0.1", "127.0.0.1", 161},
		{"device.example", "device.example", 161},
		{"device.example:1161", "device.example", 1161},
		{"[::1]:1161", "::1", 1161},
		{"[2001:db8::1]:161", "2001:db8::1", 161},
		{"[fe80::1%eth0]:161", "fe80::1%eth0", 161},
		{"::1", "::1", 161},
		{"[::1]", "::1", 161},
		{"2001:db8::1", "2001:db8::1", 161},
		{"fe80::1%eth0", "fe80::1%eth0", 161},
	} {
		t.Run(tt.address, func(t *testing.T) {
			client, err := newGoSNMPConfig(tt.address, "private", gosnmp.Version2c, 7, false)
			if err != nil {
				t.Fatal(err)
			}
			if client.Target != tt.target || client.Port != tt.port || client.Community != "private" || client.Version != gosnmp.Version2c || client.Timeout != 7*time.Second || client.Retries != 0 {
				t.Fatal("incorrect session settings")
			}
		})
	}
	for _, address := range []string{"", "host:", "host:abc", "host:0", "host:65536", "host:-1", ":161", "[::1", "::1]", "[::1:161", "[::1]:0", "[]:161", "[]", "[host]", "[192.0.2.1]"} {
		if _, _, err := snmpAddress(address); err == nil {
			t.Errorf("accepted invalid address %q", address)
		}
	}
}

func TestSnmpGetOverUDP(t *testing.T) {
	for _, tt := range []struct {
		name    string
		version gosnmp.SnmpVersion
	}{
		{"v1", gosnmp.Version1},
		{"v2c", gosnmp.Version2c},
	} {
		t.Run(tt.name, func(t *testing.T) {
			listener, err := net.ListenPacket("udp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = listener.Close() })
			if err := listener.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
				t.Fatal(err)
			}
			config := NewEmptyDeviceConfig()
			config.Address, config.Community = listener.LocalAddr().String(), "private"
			config.SnmpVersion, config.SnmpTimeout = tt.version, 2
			const oid = ".1.3.6.1.2.1.1.1.0"
			done := make(chan error, 1)
			go func() { done <- serveSnmpGet(listener, config, oid) }()
			device, err := newSnmpDevice(NewGoSNMP, config, false)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = device.Close() })
			packet, getErr := device.Get(oid)
			if err := <-done; err != nil {
				t.Fatalf("SNMP peer: %v (client: %v)", err, getErr)
			}
			if getErr != nil {
				t.Fatal(getErr)
			}
			if value, valid := ConvertSnmpValue(packet.Variables[0]); !valid || value != "test device" {
				t.Fatalf("unexpected reading: %q, valid=%v", value, valid)
			}
			if err := device.Close(); err != nil {
				t.Fatal(err)
			}
			if device.snmp.(*gosnmp.GoSNMP).Conn != nil {
				t.Fatal("connection was not closed")
			}
		})
	}
}

// A local SNMP peer exercises GET request and response encoding on the wire.
func serveSnmpGet(conn net.PacketConn, config *DeviceConfig, oid string) error {
	peer, err := newGoSNMPConfig(config.Address, config.Community, config.SnmpVersion, int64(config.SnmpTimeout), false)
	if err != nil {
		return err
	}
	buffer := make([]byte, 65535)
	n, addr, err := conn.ReadFrom(buffer)
	if err != nil {
		return err
	}
	request, err := peer.UnmarshalTrap(buffer[:n], false)
	if err != nil {
		return err
	}
	if request.Version != config.SnmpVersion || request.PDUType != gosnmp.GetRequest || len(request.Variables) != 1 || request.Variables[0].Name != oid {
		return fmt.Errorf("unexpected GET request")
	}
	if request.Community != config.Community {
		return fmt.Errorf("incorrect community")
	}
	request.PDUType = gosnmp.GetResponse
	request.Variables[0].Type, request.Variables[0].Value = gosnmp.OctetString, []byte("test device")
	wire, err := request.MarshalMsg()
	if err != nil {
		return err
	}
	_, err = conn.WriteTo(wire, addr)
	return err
}

type responseSNMP struct {
	packet *gosnmp.SnmpPacket
	closed bool
}

func (s *responseSNMP) Get(oids []string) (*gosnmp.SnmpPacket, error) { return s.packet, nil }
func (s *responseSNMP) Close() error                                  { s.closed = true; return nil }

func TestSnmpDeviceRejectsErrorResponses(t *testing.T) {
	for _, tt := range []struct {
		name   string
		packet *gosnmp.SnmpPacket
	}{
		{"nil", nil},
		{"empty", &gosnmp.SnmpPacket{PDUType: gosnmp.GetResponse}},
		{"multiple variables", &gosnmp.SnmpPacket{PDUType: gosnmp.GetResponse, Variables: make([]gosnmp.SnmpPDU, 2)}},
		{"v1 error", &gosnmp.SnmpPacket{PDUType: gosnmp.GetResponse, Error: gosnmp.NoSuchName, ErrorIndex: 1}},
		{"unexpected PDU type", &gosnmp.SnmpPacket{PDUType: gosnmp.GetRequest, Variables: []gosnmp.SnmpPDU{{Type: gosnmp.Counter32, Value: uint(1)}}}},
		{"missing object", &gosnmp.SnmpPacket{PDUType: gosnmp.GetResponse, Variables: []gosnmp.SnmpPDU{{Type: gosnmp.NoSuchObject}}}},
		{"missing instance", &gosnmp.SnmpPacket{PDUType: gosnmp.GetResponse, Variables: []gosnmp.SnmpPDU{{Type: gosnmp.NoSuchInstance}}}},
		{"end of MIB", &gosnmp.SnmpPacket{PDUType: gosnmp.GetResponse, Variables: []gosnmp.SnmpPDU{{Type: gosnmp.EndOfMibView}}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			device := &SnmpDevice{snmp: &responseSNMP{packet: tt.packet}}
			if _, err := device.Get(".1.3.6.1.2.1.1.1.0"); err == nil {
				t.Fatal("accepted invalid response")
			}
		})
	}
}

func TestSnmpModelClosesConnections(t *testing.T) {
	for _, fail := range []bool{false, true} {
		var connections []*responseSNMP
		factory := func(address, community string, version gosnmp.SnmpVersion, timeout int64, debug bool) (SnmpInterface, error) {
			if fail && len(connections) == 2 {
				return nil, fmt.Errorf("connection failed")
			}
			connection := &responseSNMP{}
			connections = append(connections, connection)
			return connection, nil
		}
		config := &DaemonConfig{Devices: map[string]*DeviceConfig{"first": {}, "second": {}, "third": {}}}
		model, err := NewSnmpModel(factory, config, time.Now())
		if fail {
			if err == nil {
				t.Fatal("expected constructor error")
			}
		} else {
			if err != nil {
				t.Fatal(err)
			}
			model.Stop()
		}
		// The third connection fails in the error case.
		want := 3
		if fail {
			want = 2
		}
		if len(connections) != want {
			t.Fatalf("opened %d connections, want %d", len(connections), want)
		}
		for i, connection := range connections {
			if !connection.closed {
				t.Fatalf("connection %d was not closed", i)
			}
		}
	}
}
