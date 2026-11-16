package analytics

import (
	"regexp"
	"strings"
)

var eventUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var propertyKeys = strings.Fields(`event_id parent_operation_id conditional origin_component origin_app_version command app_version xray_version arch daemon_role privileged service_installed tun_enabled xray_running rule_count inbound_count outbound_count subscription_count total_config_count utc_offset_minutes tz_abbrev mode selection source trigger available current_version latest_version version target_version from_version target force state bytes_done bytes_total duration_ms on tun enabled reason where uptime_seconds idle method operation operation_id outcome success verification error_kind error_reason error_fingerprint error_message grpc_code http_status message stack_fingerprint matchers prepend renamed name_changed url_changed interval_changed matcher_kinds target_kind index config_count format origin_event schema_version component process_run_id window_seconds count duration_total_ms duration_max_ms stage dns_strategy`)

// Properties is the flat, bounded wire contract. Client files and RPC properties
// never get permission to set arbitrary SDK properties or nested payloads.
func Properties(in map[string]any) map[string]any {
	out := map[string]any{}
	for _, k := range propertyKeys {
		v, ok := in[k]
		if !ok {
			continue
		}
		switch x := v.(type) {
		case string:
			if k == "event_id" || k == "operation_id" || k == "process_run_id" || k == "parent_operation_id" {
				if eventUUID.MatchString(x) {
					out[k] = x
				}
				continue
			}
			out[k] = SanitizeMessage(x)
		case bool, int, int32, int64, uint, uint32, uint64, float32, float64:
			out[k] = x
		case []any:
			if len(x) > 16 {
				continue
			}
			list := make([]string, 0, len(x))
			valid := true
			for _, v := range x {
				s, ok := v.(string)
				if !ok {
					valid = false
					break
				}
				list = append(list, SanitizeMessage(s))
			}
			if valid {
				out[k] = list
			}
		case []string:
			if len(x) > 16 {
				continue
			}
			list := make([]string, len(x))
			for i, s := range x {
				list[i] = SanitizeMessage(s)
			}
			out[k] = list
		}
	}
	return out
}
