package mqtt_snmp

import (
	"fmt"

	"github.com/gosnmp/gosnmp"
)

// SnmpV3Config contains the per-device USM credentials and scoped PDU context.
type SnmpV3Config struct {
	UserName, SecurityLevel      string
	AuthProtocol, AuthPassphrase string
	PrivProtocol, PrivPassphrase string
	ContextName                  string
}

func (c *SnmpV3Config) parse(entry map[string]any) error {
	fields := []struct {
		key   string
		value *string
	}{
		{"snmp_user", &c.UserName},
		{"snmp_security_level", &c.SecurityLevel},
		{"snmp_auth_protocol", &c.AuthProtocol},
		{"snmp_auth_passphrase", &c.AuthPassphrase},
		{"snmp_priv_protocol", &c.PrivProtocol},
		{"snmp_priv_passphrase", &c.PrivPassphrase},
		{"snmp_context_name", &c.ContextName},
	}
	for _, field := range fields {
		if err := copyString(entry, field.key, field.value, false); err != nil {
			return err
		}
	}
	return nil
}

func (c *SnmpV3Config) securityParameters() (gosnmp.SnmpV3MsgFlags, *gosnmp.UsmSecurityParameters, error) {
	if c.UserName == "" {
		return 0, nil, fmt.Errorf("snmp_user is required")
	}

	var flags gosnmp.SnmpV3MsgFlags
	switch c.SecurityLevel {
	case "", "noAuthNoPriv":
		flags = gosnmp.NoAuthNoPriv
	case "authNoPriv":
		flags = gosnmp.AuthNoPriv
	case "authPriv":
		flags = gosnmp.AuthPriv
	default:
		return 0, nil, fmt.Errorf("snmp_security_level must be noAuthNoPriv, authNoPriv or authPriv")
	}

	authProtocols := map[string]gosnmp.SnmpV3AuthProtocol{
		"": gosnmp.NoAuth, "NoAuth": gosnmp.NoAuth,
		"MD5": gosnmp.MD5, "SHA": gosnmp.SHA,
		"SHA224": gosnmp.SHA224, "SHA256": gosnmp.SHA256,
		"SHA384": gosnmp.SHA384, "SHA512": gosnmp.SHA512,
	}
	auth, ok := authProtocols[c.AuthProtocol]
	if !ok {
		return 0, nil, fmt.Errorf("unsupported snmp_auth_protocol: %s", c.AuthProtocol)
	}
	privProtocols := map[string]gosnmp.SnmpV3PrivProtocol{
		"": gosnmp.NoPriv, "NoPriv": gosnmp.NoPriv,
		"DES": gosnmp.DES, "AES": gosnmp.AES,
		"AES192": gosnmp.AES192, "AES256": gosnmp.AES256,
		"AES192C": gosnmp.AES192C, "AES256C": gosnmp.AES256C,
	}
	priv, ok := privProtocols[c.PrivProtocol]
	if !ok {
		return 0, nil, fmt.Errorf("unsupported snmp_priv_protocol: %s", c.PrivProtocol)
	}

	if flags == gosnmp.NoAuthNoPriv {
		if auth != gosnmp.NoAuth || c.AuthPassphrase != "" {
			return 0, nil, fmt.Errorf("snmp_auth_protocol and snmp_auth_passphrase require authNoPriv or authPriv")
		}
	} else {
		if auth == gosnmp.NoAuth {
			return 0, nil, fmt.Errorf("snmp_auth_protocol is required for authNoPriv and authPriv")
		}
		if len(c.AuthPassphrase) < 8 {
			return 0, nil, fmt.Errorf("snmp_auth_passphrase must contain at least 8 bytes")
		}
	}
	if flags == gosnmp.AuthPriv {
		if priv == gosnmp.NoPriv {
			return 0, nil, fmt.Errorf("snmp_priv_protocol is required for authPriv")
		}
		if len(c.PrivPassphrase) < 8 {
			return 0, nil, fmt.Errorf("snmp_priv_passphrase must contain at least 8 bytes")
		}
	} else if priv != gosnmp.NoPriv || c.PrivPassphrase != "" {
		return 0, nil, fmt.Errorf("snmp_priv_protocol and snmp_priv_passphrase require authPriv")
	}

	return flags, &gosnmp.UsmSecurityParameters{
		UserName:                 c.UserName,
		AuthenticationProtocol:   auth,
		AuthenticationPassphrase: c.AuthPassphrase,
		PrivacyProtocol:          priv,
		PrivacyPassphrase:        c.PrivPassphrase,
	}, nil
}
