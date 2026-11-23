package compose

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/Gozargah/Masir/libs/masir-daemon/internal/subs"
)

// overlay keeps provider object fields unless Masir explicitly supplies a value.
// Arrays are replaced, so managed DNS servers and listeners remain authoritative.
func overlay(base, overrides obj) obj {
	for key, value := range overrides {
		old, oldOK := base[key].(map[string]any)
		next, nextOK := value.(map[string]any)
		if !nextOK {
			if v, ok := value.(obj); ok {
				next, nextOK = v, true
			}
		}
		if oldOK && nextOK {
			base[key] = overlay(old, next)
		} else {
			base[key] = value
		}
	}
	return base
}

func profileBaseline(managed obj, profiles []*subs.OutboundProfile) (obj, error) {
	result := obj{}
	for _, profile := range profiles {
		raw, err := json.Marshal(profile.Base)
		if err != nil {
			return nil, err
		}
		base := obj{}
		if string(raw) != "null" {
			if err := json.Unmarshal(raw, &base); err != nil {
				return nil, err
			}
		}
		// Internal route tables are always built by Masir, never provider extensions.
		delete(base, "masirProfileRoutes")
		delete(base, "masirObservers")
		candidate := overlay(base, managed)
		// Normalize map types and numbers before comparing global settings.
		encoded, err := json.Marshal(candidate)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(encoded, &candidate); err != nil {
			return nil, err
		}
		if err := mergeGlobal(result, candidate, ""); err != nil {
			return nil, err
		}
	}
	if len(profiles) == 0 {
		return managed, nil
	}
	return result, nil
}

func mergeGlobal(dst, src obj, path string) error {
	for key, value := range src {
		field := path + key
		previous, exists := dst[key]
		if !exists {
			dst[key] = value
			continue
		}
		a, aOK := previous.(map[string]any)
		b, bOK := value.(map[string]any)
		if aOK && bOK {
			if err := mergeGlobal(a, b, field+"."); err != nil {
				return err
			}
		} else if !reflect.DeepEqual(previous, value) {
			return fmt.Errorf("subscription profiles require conflicting global Xray setting %s", field)
		}
	}
	return nil
}
