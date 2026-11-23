package subs

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Gozargah/Masir/libs/masir-daemon/internal/xray"
)

// RewriteDialers visits socket dialers, including nested XHTTP download streams.
func RewriteDialers(raw json.RawMessage, rewrite func(string) (string, error)) (json.RawMessage, error) {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	var visit func(any) error
	visit = func(v any) error {
		switch node := v.(type) {
		case map[string]any:
			if err := canonicalDialerField(node, "sockopt"); err != nil {
				return err
			}
			for key, child := range node {
				if strings.EqualFold(key, "sockopt") {
					if socket, ok := child.(map[string]any); ok {
						if err := canonicalDialerField(socket, "dialerProxy"); err != nil {
							return err
						}
						found := false
						for field, target := range socket {
							if !strings.EqualFold(field, "dialerProxy") {
								continue
							}
							if found {
								return fmt.Errorf("subscription repeats dialerProxy")
							}
							found = true
							tag, ok := target.(string)
							if !ok {
								return fmt.Errorf("subscription dialerProxy must be a string")
							}
							if tag == "" {
								continue
							}
							next, err := rewrite(tag)
							if err != nil {
								return err
							}
							socket[field] = next
						}
					}
				}
				if err := visit(child); err != nil {
					return err
				}
			}
		case []any:
			for _, child := range node {
				if err := visit(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := visit(value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

func dialerTargets(o map[string]json.RawMessage) ([]string, error) {
	var refs []string
	for key, raw := range o {
		if !strings.EqualFold(key, "streamSettings") {
			continue
		}
		_, err := RewriteDialers(raw, func(tag string) (string, error) { refs = append(refs, tag); return tag, nil })
		if err != nil {
			return nil, err
		}
	}
	return refs, nil
}

// dialerProfile carries the selected server and its transport dependencies for
// bare outbound lists. Full configs retain their entire graph instead.
func dialerProfile(main map[string]json.RawMessage, outbounds []map[string]json.RawMessage) (*OutboundProfile, error) {
	refs, err := dialerTargets(main)
	if err != nil {
		return nil, err
	}
	if len(refs) == 0 {
		return nil, nil
	}
	tags := make(map[string]map[string]json.RawMessage)
	for _, o := range outbounds {
		tag := str(o["tag"])
		if tag == "" {
			continue
		}
		if tags[tag] != nil {
			return nil, fmt.Errorf("subscription repeats outbound tag %q", tag)
		}
		tags[tag] = o
	}
	profile := &OutboundProfile{Routing: json.RawMessage(`{}`)}
	seen, visiting := map[string]bool{}, map[string]bool{}
	var visit func(map[string]json.RawMessage) error
	visit = func(o map[string]json.RawMessage) error {
		tag := str(o["tag"])
		if visiting[tag] {
			return fmt.Errorf("subscription has a cyclic dialerProxy chain")
		}
		if seen[tag] {
			return nil
		}
		visiting[tag] = true
		raw, err := json.Marshal(o)
		if err != nil {
			return err
		}
		if err := xray.ValidateOutbound(raw); err != nil {
			return err
		}
		profile.Outbounds = append(profile.Outbounds, raw)
		refs, err := dialerTargets(o)
		if err != nil {
			return err
		}
		for _, ref := range refs {
			next := tags[ref]
			if next == nil {
				return fmt.Errorf("subscription dialerProxy targets unknown outbound %q", ref)
			}
			if err := visit(next); err != nil {
				return err
			}
		}
		delete(visiting, tag)
		seen[tag] = true
		return nil
	}
	if err := visit(main); err != nil {
		return nil, err
	}
	return profile, nil
}

func canonicalDialerField(object map[string]any, field string) error {
	var found string
	for key := range object {
		if !strings.EqualFold(key, field) {
			continue
		}
		if found != "" {
			return fmt.Errorf("subscription repeats %s", field)
		}
		found = key
	}
	if found != "" && found != field {
		object[field] = object[found]
		delete(object, found)
	}
	return nil
}
