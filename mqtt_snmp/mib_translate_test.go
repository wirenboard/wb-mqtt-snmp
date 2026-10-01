package mqtt_snmp

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func setupMibEnvironment(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MIBDIRS", filepath.Join(home, "missing"))
	t.Setenv("SMIPATH", filepath.Join(home, "missing"))
	// Translation must work without any external executables.
	t.Setenv("PATH", home)
	return home
}

func installTestMibs(t *testing.T, dir string) {
	t.Helper()
	if err := os.CopyFS(dir, os.DirFS("testdata/mibs")); err != nil {
		t.Fatal(err)
	}
}

func TestTranslateOidsNumeric(t *testing.T) {
	setupMibEnvironment(t)
	for _, oids := range [][]string{
		nil,
		{},
		{".1.3.6.1.2.1.1.5.0", "1.3.6.1.2.1.1.5.0", ".1.3.6.1.2.1.1.5.0"},
	} {
		got, err := TranslateOids(oids)
		if err != nil {
			t.Fatal(err)
		}
		want := make(map[string]string)
		for _, oid := range oids {
			want[oid] = ".1.3.6.1.2.1.1.5.0"
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("TranslateOids(%v) = %v, want %v", oids, got, want)
		}
	}
}

func TestTranslateOidsSystemMibs(t *testing.T) {
	for _, source := range []string{"MIBDIRS", "home", "snmp.conf"} {
		t.Run(source, func(t *testing.T) {
			home := setupMibEnvironment(t)
			dir := filepath.Join(home, ".snmp", "mibs")
			if source != "home" {
				dir = filepath.Join(home, "custom-mibs")
			}
			installTestMibs(t, dir)
			switch source {
			case "MIBDIRS":
				// A missing search directory must not prevent loading later ones.
				t.Setenv("MIBDIRS", filepath.Join(home, "missing")+string(os.PathListSeparator)+dir)
			case "home":
				t.Setenv("MIBDIRS", "")
			case "snmp.conf":
				t.Setenv("MIBDIRS", "")
				if err := os.MkdirAll(filepath.Join(home, ".snmp"), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(home, ".snmp", "snmp.conf"),
					[]byte("mibdirs $HOME/custom-mibs\n"), 0644); err != nil {
					t.Fatal(err)
				}
			}

			oids := []string{
				"WB-TEST-MIB::wbTestValue.0",
				"WB-TEST-MIB::wbTestValue.1.2.3",
				"wbTestValue.0",
				"WB-TEST-ROOT-MIB::wbTestRoot",
				".1.3.6.1.2.1.1.5.0",
				"iso.3.6.1.2.1.1.5.0",
				"WB-TEST-MIB::wbTestValue.0",
			}
			want := map[string]string{
				"WB-TEST-MIB::wbTestValue.0":     ".1.3.6.1.4.1.60000.1.0",
				"WB-TEST-MIB::wbTestValue.1.2.3": ".1.3.6.1.4.1.60000.1.1.2.3",
				"wbTestValue.0":                  ".1.3.6.1.4.1.60000.1.0",
				"WB-TEST-ROOT-MIB::wbTestRoot":   ".1.3.6.1.4.1.60000",
				".1.3.6.1.2.1.1.5.0":             ".1.3.6.1.2.1.1.5.0",
				"iso.3.6.1.2.1.1.5.0":            ".1.3.6.1.2.1.1.5.0",
			}
			got, err := TranslateOids(oids)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("TranslateOids() = %v, want %v", got, want)
			}
		})
	}
}

func TestTranslateOidsInvalid(t *testing.T) {
	setupMibEnvironment(t)
	dir := t.TempDir()
	installTestMibs(t, dir)
	t.Setenv("MIBDIRS", dir)
	for _, oid := range []string{
		"", ".", "1..3", "1.3.", "1.3.4294967296",
		"UNKNOWN-MIB::wbTestValue.0", "WB-TEST-MIB::unknown.0",
		"WB-TEST-MIB::wbTestValue.invalid", "WB-TEST-MIB::wbTestValue.4294967296",
	} {
		t.Run(oid, func(t *testing.T) {
			_, err := TranslateOids([]string{oid})
			if err == nil || !strings.Contains(err.Error(), "error translating OID") ||
				!strings.Contains(err.Error(), oid) {
				t.Fatalf("TranslateOids(%q) error = %v, want an error identifying the OID", oid, err)
			}
		})
	}
}

func TestTranslateOidsWithBrokenMib(t *testing.T) {
	setupMibEnvironment(t)
	dir := t.TempDir()
	installTestMibs(t, dir)
	t.Setenv("MIBDIRS", dir)
	if err := os.WriteFile(filepath.Join(dir, "BROKEN-MIB.txt"), []byte(`
BROKEN-MIB DEFINITIONS ::= BEGIN
IMPORTS missingRoot FROM MISSING-MIB;
broken OBJECT IDENTIFIER ::= { missingRoot 1 }
END
`), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := TranslateOids([]string{"WB-TEST-MIB::wbTestValue.0"})
	if err != nil {
		t.Fatal(err)
	}
	if got["WB-TEST-MIB::wbTestValue.0"] != ".1.3.6.1.4.1.60000.1.0" {
		t.Fatalf("unexpected translation: %v", got)
	}
}

func TestLoadMibsOnlyRequiredModules(t *testing.T) {
	setupMibEnvironment(t)
	dir := t.TempDir()
	installTestMibs(t, dir)
	t.Setenv("MIBDIRS", dir)
	if err := os.WriteFile(filepath.Join(dir, "WB-TEST-OTHER-MIB.txt"), []byte(`
WB-TEST-OTHER-MIB DEFINITIONS ::= BEGIN
IMPORTS enterprises FROM SNMPv2-SMI;
wbTestOther OBJECT IDENTIFIER ::= { enterprises 60001 }
END
`), 0644); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		oids      []string
		wantOther bool
	}{
		{[]string{"WB-TEST-MIB::wbTestValue.0", "WB-TEST-MIB::wbTestValue.1"}, false},
		{[]string{"WB-TEST-MIB::wbTestValue.0", "wbTestValue.0"}, true},
	} {
		modules, err := loadMibs(tc.oids)
		if err != nil {
			t.Fatal(err)
		}
		// Imports of the requested modules are loaded too.
		for _, name := range []string{"WB-TEST-MIB", "WB-TEST-ROOT-MIB"} {
			if modules.Module(name) == nil {
				t.Errorf("loadMibs(%v): module %s not loaded", tc.oids, name)
			}
		}
		if got := modules.Module("WB-TEST-OTHER-MIB") != nil; got != tc.wantOther {
			t.Errorf("loadMibs(%v): WB-TEST-OTHER-MIB loaded = %v, want %v", tc.oids, got, tc.wantOther)
		}
	}
}

func TestTranslateOidsInDaemonConfig(t *testing.T) {
	setupMibEnvironment(t)
	dir := t.TempDir()
	installTestMibs(t, dir)
	t.Setenv("MIBDIRS", dir)
	first := &ChannelConfig{Oid: "WB-TEST-MIB::wbTestValue.0"}
	second := &ChannelConfig{Oid: first.Oid}
	numeric := &ChannelConfig{Oid: "1.3.6.1.2.1.1.5.0"}
	config := &DaemonConfig{Devices: map[string]*DeviceConfig{
		"first":  {Channels: map[string]*ChannelConfig{"value": first, "numeric": numeric}},
		"second": {Channels: map[string]*ChannelConfig{"value": second}},
	}}
	if err := TranslateOidsInDaemonConfig(config); err != nil {
		t.Fatal(err)
	}
	if first.Oid != ".1.3.6.1.4.1.60000.1.0" || second.Oid != first.Oid || numeric.Oid != ".1.3.6.1.2.1.1.5.0" {
		t.Fatalf("unexpected channel OIDs: %q, %q, %q", first.Oid, second.Oid, numeric.Oid)
	}

	first.Oid = "WB-TEST-MIB::wbTestValue.0"
	second.Oid = "UNKNOWN-MIB::unknown.0"
	if err := TranslateOidsInDaemonConfig(config); err == nil {
		t.Fatal("expected translation to fail")
	}
	if first.Oid != "WB-TEST-MIB::wbTestValue.0" || second.Oid != "UNKNOWN-MIB::unknown.0" {
		t.Fatal("configuration was partially translated on failure")
	}
}
