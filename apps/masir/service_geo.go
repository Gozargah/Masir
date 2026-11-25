package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// preflightServiceGeo decides whether to install the source pair or retain the
// service's pair. Run before replacing the executable: a bare development binary
// must not turn a fresh service installation into one without routing data.
func preflightServiceGeo(source, installed string, managed bool) (bool, error) {
	if !managed {
		if err := checkServiceGeoPair(installed); err != nil {
			return false, fmt.Errorf("the service's custom geo data is incomplete or unreadable: %w", err)
		}
		return false, nil
	}
	missing := 0
	for _, name := range []string{"geoip.dat", "geosite.dat"} {
		if _, err := os.Stat(filepath.Join(source, name)); os.IsNotExist(err) {
			missing++
		}
	}
	if missing == 2 {
		if err := checkServiceGeoPair(installed); err == nil {
			return false, nil
		}
		return false, fmt.Errorf("no geo data beside the executable: build with make build, or place geoip.dat and geosite.dat beside masir before installing the service")
	}
	if err := checkServiceGeoPair(source); err != nil {
		return false, fmt.Errorf("cannot install an incomplete or unreadable geo pair: %w", err)
	}
	return true, nil
}

func checkServiceGeoPair(dir string) error {
	for _, name := range []string{"geoip.dat", "geosite.dat"} {
		path := filepath.Join(dir, name)
		fi, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("stat %s: %w", name, err)
		}
		if !fi.Mode().IsRegular() || fi.Size() == 0 {
			return fmt.Errorf("%s must be a nonempty regular file", name)
		}
		f, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("open %s: %w", name, err)
		}
		fi, statErr := f.Stat()
		closeErr := f.Close()
		if statErr != nil {
			return statErr
		}
		if closeErr != nil {
			return closeErr
		}
		if !fi.Mode().IsRegular() || fi.Size() == 0 {
			return fmt.Errorf("%s must be a nonempty regular file", name)
		}
	}
	return nil
}
