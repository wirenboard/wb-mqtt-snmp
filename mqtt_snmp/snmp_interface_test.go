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
			config := NewEmptyDeviceConfig()
			config.Address, config.Community, config.SnmpTimeout = tt.address, "private", 7
			client, err := newGoSNMPConfig(config, false)
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
		v3      SnmpV3Config
	}{
		{"v1", gosnmp.Version1, SnmpV3Config{}},
		{"v2c", gosnmp.Version2c, SnmpV3Config{}},
		{"v3 noAuthNoPriv", gosnmp.Version3, SnmpV3Config{UserName: "monitor"}},
		{"v3 authNoPriv", gosnmp.Version3, SnmpV3Config{UserName: "monitor", SecurityLevel: "authNoPriv", AuthProtocol: "MD5", AuthPassphrase: "auth-password"}},
		{"v3 authPriv AES", gosnmp.Version3, SnmpV3Config{UserName: "monitor", SecurityLevel: "authPriv", AuthProtocol: "SHA256", AuthPassphrase: "auth-password", PrivProtocol: "AES", PrivPassphrase: "priv-password", ContextName: "test-context"}},
		{"v3 authPriv DES", gosnmp.Version3, SnmpV3Config{UserName: "monitor", SecurityLevel: "authPriv", AuthProtocol: "SHA", AuthPassphrase: "auth-password", PrivProtocol: "DES", PrivPassphrase: "priv-password"}},
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
			config.SnmpVersion, config.SnmpV3, config.SnmpTimeout = tt.version, tt.v3, 2
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

// A local SNMP peer exercises GET encoding, v3 engine discovery and USM on the wire.
func serveSnmpGet(conn net.PacketConn, config *DeviceConfig, oid string) error {
	peer, err := newGoSNMPConfig(config, false)
	if err != nil {
		return err
	}
	if config.SnmpVersion == gosnmp.Version3 {
		if discoveryErr := serveSnmpDiscovery(conn, peer.SecurityParameters.(*gosnmp.UsmSecurityParameters)); discoveryErr != nil {
			return discoveryErr
		}
	}
	buffer := make([]byte, 65535)
	n, addr, err := conn.ReadFrom(buffer)
	if err != nil {
		return err
	}
	// UnmarshalTrap also verifies authentication, unlike SnmpDecodePacket.
	request, err := peer.UnmarshalTrap(buffer[:n], false)
	if err != nil {
		return err
	}
	if request.Version != config.SnmpVersion || request.PDUType != gosnmp.GetRequest || len(request.Variables) != 1 || request.Variables[0].Name != oid {
		return fmt.Errorf("unexpected GET request")
	}
	if config.SnmpVersion == gosnmp.Version3 {
		if request.ContextName != config.SnmpV3.ContextName || request.SecurityParameters.(*gosnmp.UsmSecurityParameters).UserName != config.SnmpV3.UserName || request.MsgFlags&gosnmp.AuthPriv != peer.MsgFlags {
			return fmt.Errorf("incorrect USM or context settings")
		}
	} else if request.Community != config.Community {
		return fmt.Errorf("incorrect community")
	}
	request.PDUType = gosnmp.GetResponse
	request.MsgFlags &^= gosnmp.Reportable
	request.Variables[0].Type, request.Variables[0].Value = gosnmp.OctetString, []byte("test device")
	wire, err := request.MarshalMsg()
	if err != nil {
		return err
	}
	_, err = conn.WriteTo(wire, addr)
	return err
}

func serveSnmpDiscovery(conn net.PacketConn, security *gosnmp.UsmSecurityParameters) error {
	buffer := make([]byte, 65535)
	n, addr, err := conn.ReadFrom(buffer)
	if err != nil {
		return fmt.Errorf("read discovery request: %w", err)
	}
	decoder := &gosnmp.GoSNMP{}
	discovery, err := decoder.SnmpDecodePacket(buffer[:n])
	if err != nil {
		return fmt.Errorf("decode discovery request: %w", err)
	}
	if discovery.Version != gosnmp.Version3 || discovery.SecurityParameters.(*gosnmp.UsmSecurityParameters).AuthoritativeEngineID != "" {
		return fmt.Errorf("expected engine discovery")
	}
	security.AuthoritativeEngineID = "\x80\x00\x00\x00\x01test-engine"
	security.AuthoritativeEngineBoots, security.AuthoritativeEngineTime = 1, 1
	if err = security.InitSecurityKeys(); err != nil {
		return fmt.Errorf("initialize security keys: %w", err)
	}
	discovery.PDUType = gosnmp.Report
	discovery.MsgFlags = gosnmp.NoAuthNoPriv
	discovery.SecurityParameters = &gosnmp.UsmSecurityParameters{
		AuthoritativeEngineID:    security.AuthoritativeEngineID,
		AuthoritativeEngineBoots: 1, AuthoritativeEngineTime: 1,
	}
	discovery.Variables = []gosnmp.SnmpPDU{{Name: ".1.3.6.1.6.3.15.1.1.4.0", Type: gosnmp.Counter32, Value: uint32(1)}}
	wire, err := discovery.MarshalMsg()
	if err != nil {
		return fmt.Errorf("encode discovery response: %w", err)
	}
	if _, err = conn.WriteTo(wire, addr); err != nil {
		return fmt.Errorf("send discovery response: %w", err)
	}
	return nil
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
		{"report", &gosnmp.SnmpPacket{PDUType: gosnmp.Report, Variables: []gosnmp.SnmpPDU{{Type: gosnmp.Counter32, Value: uint(1)}}}},
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
		factory := func(config *DeviceConfig, debug bool) (SnmpInterface, error) {
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
