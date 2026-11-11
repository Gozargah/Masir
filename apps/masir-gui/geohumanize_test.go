package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
	pb "github.com/Gozargah/Masir/libs/masir-proto/gen/masir/daemon/v1"
)

func TestHumanizeGeosite(t *testing.T) {
	tests := []struct {
		code     string
		wantTit  string
		wantFlag bool
		wantIcon bool // true if iconGlobe expected with no flag
	}{
		// User requirements
		{"category-ir", "Iran Websites", true, false},
		{"geosite:category-ir", "Iran Websites", true, false},
		{"category-ai-!cn", "AI (Excluding China)", false, true},
		{"category-ai-cn", "AI China", true, false},
		{"category-bank-ir", "Iran Banks", true, false},

		// Additional country-scoped categories
		{"category-ru", "Russia Websites", true, false},
		{"category-cn", "China Websites", true, false},
		{"geosite:ir", "Iran Websites", true, false},
		{"geosite:cn", "China Websites", true, false},
		{"geolocation-cn", "Websites In China", true, false},
		{"geolocation-!cn", "Websites (Excluding China)", false, true},
		{"category-scholar-!cn", "Scholar (Excluding China)", false, true},
		{"category-scholar-gb", "United Kingdom Scholar", true, false},
		{"google-cn", "Google (China)", true, false},
		{"tld-!cn", "TLD (Excluding China)", false, true},
		{"category-ads-all", "Ads & Trackers", false, true},
	}

	for _, tc := range tests {
		title, flag, icon, ok := humanizeGeosite(tc.code)
		if !ok {
			t.Errorf("humanizeGeosite(%q) returned ok=false", tc.code)
			continue
		}
		if title != tc.wantTit {
			t.Errorf("humanizeGeosite(%q) title = %q, want %q", tc.code, title, tc.wantTit)
		}
		if tc.wantFlag && flag == "" {
			t.Errorf("humanizeGeosite(%q) expected flag, got empty", tc.code)
		}
		if !tc.wantFlag && flag != "" {
			t.Errorf("humanizeGeosite(%q) expected empty flag, got %q", tc.code, flag)
		}
		if tc.wantIcon && (icon == nil || icon != iconGlobe) {
			t.Errorf("humanizeGeosite(%q) expected iconGlobe, got %v", tc.code, icon)
		}
	}
}

func TestHumanizeGeoIP(t *testing.T) {
	tests := []struct {
		code     string
		wantTit  string
		wantFlag bool
	}{
		// User requirements
		{"private", "Private IPs", false},
		{"geoip:private", "Private IPs", false},
		{"geoip:ir", "Iran", true},
		{"geoip:cn", "China", true},
		{"geoip:de", "Germany", true},
		{"geoip:us", "United States", true},
		{"geoip:telegram", "Telegram Servers", false},
	}

	for _, tc := range tests {
		title, flag, icon, ok := humanizeGeoIP(tc.code)
		if !ok {
			t.Errorf("humanizeGeoIP(%q) returned ok=false", tc.code)
			continue
		}
		if title != tc.wantTit {
			t.Errorf("humanizeGeoIP(%q) title = %q, want %q", tc.code, title, tc.wantTit)
		}
		if tc.wantFlag && flag == "" {
			t.Errorf("humanizeGeoIP(%q) expected flag, got empty", tc.code)
		}
		if !tc.wantFlag && flag != "" {
			t.Errorf("humanizeGeoIP(%q) expected empty flag, got %q", tc.code, flag)
		}
		if icon == nil || icon != iconLayers {
			t.Errorf("humanizeGeoIP(%q) expected iconLayers, got %v", tc.code, icon)
		}
	}
}

func TestRuleInfoHumanization(t *testing.T) {
	tests := []struct {
		json     string
		wantTit  string
		wantSec  string
		wantFlag bool
		wantIcon *ui.SVG
	}{
		{`{"domain":["geosite:category-ir"]}`, "Iran Websites", "Website Categories", true, iconGlobe},
		{`{"domain":["geosite:category-ai-!cn"]}`, "AI (Excluding China)", "Website Categories", false, iconGlobe},
		{`{"domain":["geosite:category-ai-cn"]}`, "AI China", "Website Categories", true, iconGlobe},
		{`{"domain":["geosite:category-bank-ir"]}`, "Iran Banks", "Website Categories", true, iconGlobe},
		{`{"ip":["geoip:private"]}`, "Private IPs", "IP Ranges & Ports", false, iconLayers},
		{`{"ip":["geoip:ir"]}`, "Iran", "IP Ranges & Ports", true, iconLayers},
		{`{"ip":["geoip:cn"]}`, "China", "IP Ranges & Ports", true, iconLayers},
	}

	for _, tc := range tests {
		r := &pb.RoutingRule{RuleTag: "test", RuleJson: tc.json}
		inf := ruleInfo(r)
		if inf.title != tc.wantTit {
			t.Errorf("ruleInfo(%s) title = %q, want %q", tc.json, inf.title, tc.wantTit)
		}
		if inf.section != tc.wantSec {
			t.Errorf("ruleInfo(%s) section = %q, want %q", tc.json, inf.section, tc.wantSec)
		}
		if tc.wantFlag && inf.flag == "" {
			t.Errorf("ruleInfo(%s) expected flag, got empty", tc.json)
		}
		if !tc.wantFlag && inf.flag != "" {
			t.Errorf("ruleInfo(%s) expected empty flag, got %q", tc.json, inf.flag)
		}
		if tc.wantIcon != nil && inf.icon != tc.wantIcon {
			t.Errorf("ruleInfo(%s) expected icon %v, got %v", tc.json, tc.wantIcon, inf.icon)
		}
	}
}
