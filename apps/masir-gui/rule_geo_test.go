package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	pb "github.com/Gozargah/Masir/libs/masir-proto/gen/masir/daemon/v1"
	"github.com/egoist/mygo/ui"
	"google.golang.org/grpc"
)

type geoPickerDaemon struct {
	*fakeDaemon
	calls int
	err   error
}

func geoPickerCategories() []*pb.GeoCategory {
	return []*pb.GeoCategory{{Code: "category-ads-all", Entries: 125000}, {Code: "ir", Entries: 12000}, {Code: "google", Entries: 1500}, {Code: "category-new-topic", Entries: 19}, {Code: "ir", Entries: 12000}}
}

func (f *geoPickerDaemon) ListGeoCategories(context.Context, *pb.ListGeoCategoriesRequest, ...grpc.CallOption) (*pb.ListGeoCategoriesResponse, error) {
	f.calls++
	return &pb.ListGeoCategoriesResponse{Categories: geoPickerCategories()}, f.err
}

func TestGeoPickerPrefetchAndSearch(t *testing.T) {
	f := &geoPickerDaemon{fakeDaemon: ukServers()}
	a := newTestApp(f.fakeDaemon)
	a.d = f
	var work []func()
	a.async = func(fn func()) { work = append(work, fn) }
	a.newRule()
	if !a.ruleGeo.loading || len(work) != 2 {
		t.Fatal("geo catalog was not prefetched alongside apps")
	}
	a.loadRuleGeo()
	if len(work) != 2 {
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
	work[2]()
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
			a.rulePage.expanded, a.rulePage.domainMode = "domain", 2
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
			click("Search website lists…")
			tt.Type("iran")
			tt.Frame()
			tt.Frame()
			click("Select website list ir")
			click("Clear search")
			click("Selected")
			if len(a.ruleGeo.filtered) != 3 {
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
			click("Manual")
			click("Website category")
			tt.Type("geosite:custom-list")
			tt.KeyDown(ui.KeyEnter)
			tt.Frame()
			tt.Frame()
			if !strings.Contains(a.rulePage.values["domain"], "geosite:custom-list") {
				t.Fatal("manual category was not added")
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
