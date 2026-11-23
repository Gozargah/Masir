package compose

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
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
		if err := validateFieldAliases(base, reflect.TypeFor[conf.Config](), ""); err != nil {
			return nil, err
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
		// Xray ignores provider metadata and unknown struct fields. Retain
		// the first value without treating a different provider's value as
		// a runtime conflict.
		if childSchema == nil && ordinaryStruct(schema) {
			continue
		}
		if err := compareGlobalValue(previous, value, field, childSchema); err != nil {
			return err
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
		if _, exists := object[key]; exists {
			return key, schema.Elem()
		}
		if number, ok := integerMapKey(key, schema.Key()); ok {
			for existing := range object {
				if other, valid := integerMapKey(existing, schema.Key()); valid && number == other {
					return existing, schema.Elem()
				}
			}
		}
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

func integerMapKey(key string, schema reflect.Type) (string, bool) {
	switch schema.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		value, err := strconv.ParseInt(key, 10, schema.Bits())
		return strconv.FormatInt(value, 10), err == nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		value, err := strconv.ParseUint(key, 10, schema.Bits())
		return strconv.FormatUint(value, 10), err == nil
	}
	return "", false
}

func ordinaryStruct(schema reflect.Type) bool {
	for schema != nil && schema.Kind() == reflect.Pointer {
		schema = schema.Elem()
	}
	return schema != nil && schema.Kind() == reflect.Struct && !reflect.PointerTo(schema).Implements(reflect.TypeFor[json.Unmarshaler]())
}

// Reject ambiguity before managed overrides can conceal one of the aliases.
// String dictionaries and custom JSON decoders retain their own semantics.
func validateFieldAliases(value any, schema reflect.Type, path string) error {
	for schema != nil && schema.Kind() == reflect.Pointer {
		schema = schema.Elem()
	}
	if schema == nil || reflect.PointerTo(schema).Implements(reflect.TypeFor[json.Unmarshaler]()) {
		return nil
	}
	switch node := value.(type) {
	case map[string]any:
		if schema.Kind() != reflect.Struct && schema.Kind() != reflect.Map {
			return nil
		}
		seen := obj{}
		for key, child := range node {
			previousKey, childSchema := matchingField(seen, key, schema)
			if childSchema == nil {
				continue
			}
			if _, exists := seen[previousKey]; exists {
				return fmt.Errorf("subscription repeats Xray field %s%s", path, key)
			}
			seen[key] = true
			if err := validateFieldAliases(child, childSchema, path+key+"."); err != nil {
				return err
			}
		}
	case []any:
		if schema.Kind() == reflect.Slice || schema.Kind() == reflect.Array {
			for i, child := range node {
				if err := validateFieldAliases(child, schema.Elem(), fmt.Sprintf("%s%d.", path, i)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func compareGlobalValue(previous, value any, path string, schema reflect.Type) error {
	a, aOK := previous.(map[string]any)
	b, bOK := value.(map[string]any)
	if aOK && bOK {
		return mergeGlobalFields(a, b, path+".", schema)
	}
	for schema != nil && schema.Kind() == reflect.Pointer {
		schema = schema.Elem()
	}
	left, leftOK := previous.([]any)
	right, rightOK := value.([]any)
	if leftOK && rightOK && len(left) == len(right) && schema != nil && (schema.Kind() == reflect.Slice || schema.Kind() == reflect.Array) && !reflect.PointerTo(schema).Implements(reflect.TypeFor[json.Unmarshaler]()) {
		for i := range left {
			if err := compareGlobalValue(left[i], right[i], fmt.Sprintf("%s.%d", path, i), schema.Elem()); err != nil {
				return err
			}
		}
		return nil
	}
	if !reflect.DeepEqual(previous, value) {
		return fmt.Errorf("subscription profiles require conflicting global Xray setting %s", path)
	}
	return nil
}
