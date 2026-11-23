package compose

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/Gozargah/Masir/libs/masir-daemon/internal/subs"
	"github.com/xtls/xray-core/infra/conf"
)

// overlay keeps provider object fields unless Masir explicitly supplies a value.
// Arrays are replaced, so managed DNS servers and listeners remain authoritative.
func overlay(base, overrides obj) obj {
	return overlayFields(base, overrides, reflect.TypeFor[conf.Config]())
}

func overlayFields(base, overrides obj, schema reflect.Type) obj {
	for key, value := range overrides {
		previousKey, childSchema := matchingField(base, key, schema)
		old, oldOK := base[previousKey].(map[string]any)
		next, nextOK := value.(map[string]any)
		if !nextOK {
			if v, ok := value.(obj); ok {
				next, nextOK = v, true
			}
		}
		if oldOK && nextOK {
			base[key] = overlayFields(old, next, childSchema)
		} else {
			base[key] = value
		}
		if previousKey != key {
			delete(base, previousKey)
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
		// Masir owns all listeners, including API and debug listeners outside
		// the ordinary inbounds list, and builds its internal routing tables.
		for key := range base {
			switch strings.ToLower(key) {
			case "api", "metrics", "masirprofileroutes", "masirobservers":
				delete(base, key)
			}
		}
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
	return mergeGlobalFields(dst, src, path, reflect.TypeFor[conf.Config]())
}

func mergeGlobalFields(dst, src obj, path string, schema reflect.Type) error {
	for key, value := range src {
		field := path + key
		previousKey, childSchema := matchingField(dst, key, schema)
		previous, exists := dst[previousKey]
		if !exists {
			dst[key] = value
			continue
		}
		a, aOK := previous.(map[string]any)
		b, bOK := value.(map[string]any)
		if aOK && bOK {
			if err := mergeGlobalFields(a, b, field+".", childSchema); err != nil {
				return err
			}
		} else if !reflect.DeepEqual(previous, value) {
			return fmt.Errorf("subscription profiles require conflicting global Xray setting %s", field)
		}
	}
	return nil
}

// Match struct fields like encoding/json, while keeping map/dictionary keys
// exact. Custom JSON decoders own their field semantics and remain opaque.
func matchingField(object obj, key string, schema reflect.Type) (string, reflect.Type) {
	for schema != nil && schema.Kind() == reflect.Pointer {
		schema = schema.Elem()
	}
	if schema == nil {
		return key, nil
	}
	if reflect.PointerTo(schema).Implements(reflect.TypeFor[json.Unmarshaler]()) {
		return key, nil
	}
	if schema.Kind() == reflect.Map {
		return key, schema.Elem()
	}
	if schema.Kind() != reflect.Struct {
		return key, nil
	}
	for i := 0; i < schema.NumField(); i++ {
		field := schema.Field(i)
		if !field.IsExported() {
			continue
		}
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "-" {
			continue
		}
		if name == "" {
			name = field.Name
		}
		if !strings.EqualFold(key, name) {
			continue
		}
		if _, exists := object[key]; exists {
			return key, field.Type
		}
		for existing := range object {
			if strings.EqualFold(existing, name) {
				return existing, field.Type
			}
		}
		return key, field.Type
	}
	return key, nil
}
