package mqtt_snmp

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/contactless/wbgo"
	"github.com/golangsnmp/gomib"
	"github.com/golangsnmp/gomib/mib"
)

// TranslateOids translates mixed numeric and symbolic OIDs using locally
// installed MIBs. System paths include Debian's Net-SNMP and downloaded MIBs,
// as well as directories configured through MIBDIRS and snmp.conf.
func TranslateOids(oids []string) (map[string]string, error) {
	out := make(map[string]string, len(oids))
	var symbolic []string

	// Numeric OIDs do not require any installed MIBs.
	for _, value := range oids {
		oid, err := mib.ParseOID(value)
		if err != nil {
			symbolic = append(symbolic, value)
			continue
		}
		// Preserve the numeric format produced by snmptranslate -On.
		out[value] = "." + oid.String()
	}

	if len(symbolic) == 0 {
		wbgo.Info.Printf("OID translation: %d numeric OIDs, no MIBs loaded", len(out))
		return out, nil
	}
	// Sort for a stable log order.
	slices.Sort(symbolic)
	symbolic = slices.Compact(symbolic)

	modules, err := loadMibs(symbolic)
	if err != nil {
		return nil, err
	}
	wbgo.Info.Printf("OID translation: %d numeric, %d symbolic OIDs, %d MIB modules loaded",
		len(out), len(symbolic), len(modules.Modules()))

	// Report all failed OIDs at once, not only the first one.
	var errs []error
	for _, value := range symbolic {
		oid, err := modules.ResolveOID(value)
		if err != nil {
			errs = append(errs, fmt.Errorf("error translating OID %q: %w", value, err))
			continue
		}
		out[value] = "." + oid.String()
		wbgo.Info.Printf("OID translation: %s -> %s", value, out[value])
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	return out, nil
}

// loadMibs loads the MIBs needed to resolve the given symbolic OIDs.
func loadMibs(oids []string) (*mib.Mib, error) {
	opts := []gomib.LoadOption{
		gomib.WithSystemPaths(),
		gomib.WithResolverStrictness(mib.ResolverPermissive),
	}
	// Qualified OIDs (MODULE::name) need only their modules and imports to
	// be parsed and resolved. Every file in the MIB directories is still
	// scanned to find out which module it defines. An unqualified name may
	// be defined in any installed MIB, so in that case all of them are loaded.
	if names, ok := qualifiedModules(oids); ok {
		opts = append(opts, gomib.WithModules(names...))
	}

	modules, err := gomib.Load(context.Background(), opts...)
	switch {
	case err == nil:
	case errors.Is(err, gomib.ErrDiagnosticThreshold):
		// Like Net-SNMP, keep usable definitions even when other
		// installed MIBs contain parsing or resolution errors.
		wbgo.Info.Printf("MIB loading diagnostics: %v", err)
	case errors.Is(err, gomib.ErrMissingModules):
		// ResolveOID reports the missing module for the OID that needs it.
	default:
		return nil, fmt.Errorf("error loading MIBs: %w", err)
	}
	return modules, nil
}

// qualifiedModules returns the modules named by MODULE::name OIDs,
// or false if any OID is unqualified.
func qualifiedModules(oids []string) ([]string, bool) {
	var names []string
	for _, value := range oids {
		name, _, ok := strings.Cut(value, "::")
		if !ok {
			return nil, false
		}
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	return names, true
}

// Translate all OIDs in given configuration
func TranslateOidsInDaemonConfig(config *DaemonConfig) error {
	// collect all unique OIDs into list
	oids_set := make(map[string]bool)

	for _, device := range config.Devices {
		for _, channel := range device.Channels {
			oids_set[channel.Oid] = true
		}
	}

	oids_list := make([]string, len(oids_set))

	i := 0
	for key := range oids_set {
		oids_list[i] = key
		i += 1
	}

	// parse list
	tmap, err := TranslateOids(oids_list)
	if err != nil {
		return err
	}

	// translate OIDs in config
	for _, device := range config.Devices {
		for _, channel := range device.Channels {
			channel.Oid = tmap[channel.Oid]
		}
	}

	return nil
}
