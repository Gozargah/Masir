package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	pb "github.com/Gozargah/Masir/libs/masir-proto/gen/masir/daemon/v1"
	"google.golang.org/grpc"
)

type geoPickerDaemon struct {
	*fakeDaemon
	calls int
	kinds []pb.GeoKind
	err   error
}

func geoPickerCategories() []*pb.GeoCategory {
	return []*pb.GeoCategory{{Code: "category-ads-all", Entries: 125000}, {Code: "ir", Entries: 12000}, {Code: "google", Entries: 1500}, {Code: "category-new-topic", Entries: 19}, {Code: "ir", Entries: 12000}}
}

func (f *geoPickerDaemon) ListGeoCategories(_ context.Context, req *pb.ListGeoCategoriesRequest, _ ...grpc.CallOption) (*pb.ListGeoCategoriesResponse, error) {
	f.calls++
	f.kinds = append(f.kinds, req.Kind)
	if req.Kind == pb.GeoKind_GEO_KIND_IP {
		return &pb.ListGeoCategoriesResponse{Categories: []*pb.GeoCategory{{Code: "ir", Entries: 200}, {Code: "private", Entries: 10}}}, f.err
	}
	return &pb.ListGeoCategoriesResponse{Categories: geoPickerCategories()}, f.err
}

func TestGeoPickerPrefetchAndSearch(t *testing.T) {
	f := &geoPickerDaemon{fakeDaemon: ukServers()}
	a := newTestApp(f.fakeDaemon)
	a.d = f
	var work []func()
	a.async = func(fn func()) { work = append(work, fn) }
	a.newRule()
	if !a.ruleGeo.loading || len(work) != 3 {
		t.Fatal("geo catalog was not prefetched alongside apps")
	}
	a.loadRuleGeo()
	if len(work) != 3 {
		t.Fatal("duplicate catalog request")
	}
	work[1]()
	if f.calls != 1 || len(a.ruleGeo.all) != 4 {
		t.Fatalf("catalog: %d calls, %d items", f.calls, len(a.ruleGeo.all))
	}
	for query, code := range map[string]string{"Iran": "ir", "geosite:ir": "ir", "ads": "category-ads-all", "Google": "google", "new topic": "category-new-topic"} {
		a.ruleGeo.query = query
		a.filterRuleGeo()
		if len(a.ruleGeo.filtered) == 0 || a.ruleGeo.filtered[0].code != code {
			t.Fatalf("query %q: %+v", query, a.ruleGeo.filtered)
		}
	}
	a.prepareRuleGeo()
	if len(a.ruleGeo.all) != 4 || !a.ruleGeo.loading {
		t.Fatal("revalidation discarded usable catalog")
	}
	a.rulePage = decodeRule(nil)
	a.rulePage.values["domain"] = "domain:new-draft.example"
	work[3]()
	if a.rulePage.values["domain"] != "domain:new-draft.example" {
		t.Fatal("late catalog result modified a newer draft")
	}
}

func TestGeoPickerSelectionPreservesOtherDomainMatchers(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint(dark), func(t *testing.T) {
			a := newTestApp(ukServers())
			a.d = &geoPickerDaemon{fakeDaemon: ukServers()}
			a.newRule()
			a.rulePage.expanded, a.rulePage.domainMode = "domain", 0
			a.rulePage.values["domain"] = "domain:example.com\ngeosite:old-custom"
			tt := tester(a)
			tt.SetDark(dark)
			tt.SetSize(panelWidth, 620)
			tt.Frame()
			tt.Frame()
			click := func(s string) {
				t.Helper()
				if err := tt.Click(s); err != nil {
					t.Fatal(err)
				}
				tt.Frame()
				tt.Frame()
			}
			click("Select website list category-ads-all")
			click("Search category or website…")
			tt.Type("iran")
			tt.Frame()
			tt.Frame()
			click("Select website list ir")
			click("Clear search")
			if len(a.ruleGeo.selected) != 3 {
				t.Fatalf("missing selected categories: %+v", a.ruleGeo.filtered)
			}
			click("Select website list old-custom")
			if !strings.Contains(a.rulePage.values["domain"], "domain:example.com") || strings.Contains(a.rulePage.values["domain"], "old-custom") {
				t.Fatal("toggle altered unrelated matchers")
			}
			raw, err := a.rulePage.matchJSON()
			if err != nil || !strings.Contains(raw, "geosite:ir") || !strings.Contains(raw, "geosite:category-ads-all") {
				t.Fatalf("selection serialization: %s %v", raw, err)
			}

			panel, _ := tt.Find("Websites and domains options")
			card, _ := tt.Find("Rule conditions")
			if abs32(panel.X-card.X-1) > .5 || abs32(panel.W-card.W+2) > .5 {
				t.Fatal("category content is inset")
			}
		})
	}
}

func TestGeoPickerKeepsResultsOnRefreshError(t *testing.T) {
	f := &geoPickerDaemon{fakeDaemon: ukServers()}
	a := newTestApp(f.fakeDaemon)
	a.d = f
	a.newRule()
	f.err = errors.New("unavailable")
	a.prepareRuleGeo()
	if len(a.ruleGeo.all) != 4 || a.ruleGeo.err == "" || a.ruleGeo.loading {
		t.Fatal("failed refresh discarded the catalog")
	}
}

func BenchmarkGeoPickerSearch(b *testing.B) {
	a := &app{rulePage: decodeRule(nil)}
	for i := range 10000 {
		a.ruleGeo.all = append(a.ruleGeo.all, geoWebsiteOption(fmt.Sprintf("category-list-%d", i), 100))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		a.ruleGeo.query = fmt.Sprintf("list-%d", i%10)
		a.filterRuleGeo()
	}
}

func TestGeoIPPickerUsesIPCatalogAndPreservesManualRanges(t *testing.T) {
	f := &geoPickerDaemon{fakeDaemon: ukServers()}
	a := newTestApp(f.fakeDaemon)
	a.d = f
	a.newRule()
	if len(f.kinds) != 2 || f.kinds[0] != pb.GeoKind_GEO_KIND_SITE || f.kinds[1] != pb.GeoKind_GEO_KIND_IP {
		t.Fatalf("wrong catalog requests: %v", f.kinds)
	}
	a.rulePage.expanded = "ip"
	a.rulePage.values["domain"] = "geosite:google"
	tt := tester(a)
	tt.SetSize(panelWidth, 620)
	tt.Frame()
	tt.Frame()
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		tt.Frame()
	}
	if !tt.Focused("Search country or GeoIP category…") {
		t.Fatal("GeoIP search did not autofocus")
	}
	tt.Type("iran")
	tt.Frame()
	tt.Frame()
	if len(a.ruleGeoIP.filtered) != 1 || a.ruleGeoIP.filtered[0].code != "ir" {
		t.Fatal("country search failed")
	}
	click("Select geoip list ir")
	click("Manual")
	if !tt.Focused("IP addresses or ranges") {
		t.Fatal("manual IP input did not autofocus")
	}
	tt.Type("10.0.0.0/8")
	tt.Frame()
	if got := a.rulePage.values["ip"]; got != "geoip:ir\n10.0.0.0/8" {
		t.Fatalf("manual edit lost categories: %q", got)
	}
	click("GeoIP")
	click("Select geoip list ir")
	if got := a.rulePage.values["ip"]; got != "10.0.0.0/8" {
		t.Fatalf("deselection lost manual range: %q", got)
	}
	if a.rulePage.values["domain"] != "geosite:google" {
		t.Fatal("IP picker modified websites")
	}
	raw, err := a.rulePage.matchJSON()
	if err != nil || !strings.Contains(raw, `"ip":["10.0.0.0/8"]`) {
		t.Fatalf("IP serialization: %s %v", raw, err)
	}
}
