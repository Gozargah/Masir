package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	xgeo "github.com/xtls/xray-core/common/geodata"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/Gozargah/Masir/libs/masir-daemon/internal/identity"
	"github.com/Gozargah/Masir/libs/masir-daemon/internal/xray"
	pb "github.com/Gozargah/Masir/libs/masir-proto/gen/masir/daemon/v1"
)

func TestListGeoCategoriesCachingAndInvalidation(t *testing.T) {
	assetDir := t.TempDir()
	xc := xray.New(assetDir)
	t.Cleanup(func() { _ = xc.Close() })
	s := New(xc, identity.Identity{UID: os.Getuid(), GID: os.Getgid(), Name: "tester"})

	ctx := context.Background()

	// Missing file returns FailedPrecondition
	_, err := s.ListGeoCategories(ctx, &pb.ListGeoCategoriesRequest{Kind: pb.GeoKind_GEO_KIND_SITE})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition for missing file, got %v", err)
	}

	// Invalid kind returns InvalidArgument
	_, err = s.ListGeoCategories(ctx, &pb.ListGeoCategoriesRequest{Kind: pb.GeoKind_GEO_KIND_UNSPECIFIED})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument for unspecified kind, got %v", err)
	}

	// Create geosite.dat
	site := &xgeo.GeoSiteList{Entry: []*xgeo.GeoSite{
		{Code: "CATEGORY-AI-CHAT", Domain: []*xgeo.Domain{{Value: "openai.com"}, {Value: "claude.ai"}}},
		{Code: "google", Domain: []*xgeo.Domain{{Value: "google.com"}}},
	}}
	siteBytes, _ := proto.Marshal(site)
	sitePath := filepath.Join(assetDir, "geosite.dat")
	if err := os.WriteFile(sitePath, siteBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	// 1. First call loads from disk into cache
	res1, err := s.ListGeoCategories(ctx, &pb.ListGeoCategoriesRequest{Kind: pb.GeoKind_GEO_KIND_SITE})
	if err != nil {
		t.Fatalf("ListGeoCategories failed: %v", err)
	}
	if len(res1.Categories) != 2 {
		t.Fatalf("expected 2 categories, got %d", len(res1.Categories))
	}
	if res1.Categories[0].Code != "category-ai-chat" || res1.Categories[0].Entries != 2 {
		t.Errorf("unexpected category 0: %+v", res1.Categories[0])
	}
	if res1.Categories[1].Code != "google" || res1.Categories[1].Entries != 1 {
		t.Errorf("unexpected category 1: %+v", res1.Categories[1])
	}

	// Verify server cache entry exists
	s.geoMu.RLock()
	cached, ok := s.geoCache[sitePath]
	s.geoMu.RUnlock()
	if !ok || len(cached.cats) != 2 {
		t.Fatal("expected cache entry to be populated")
	}
	res1.Categories[0].Code = "modified"
	res1.Categories[0].Entries = 99

	// 2. Second call hits cache (same mod time and size)
	res2, err := s.ListGeoCategories(ctx, &pb.ListGeoCategoriesRequest{Kind: pb.GeoKind_GEO_KIND_SITE})
	if err != nil {
		t.Fatalf("ListGeoCategories repeat failed: %v", err)
	}
	if len(res2.Categories) != 2 {
		t.Fatalf("expected 2 categories from cache, got %d", len(res2.Categories))
	}
	if res2.Categories[0].Code != "category-ai-chat" || res2.Categories[0].Entries != 2 {
		t.Fatalf("cache changed through first response: %+v", res2.Categories[0])
	}
	res2.Categories[0].Code = "modified again"
	res2.Categories[0].Entries = 42
	resCached, err := s.ListGeoCategories(ctx, &pb.ListGeoCategoriesRequest{Kind: pb.GeoKind_GEO_KIND_SITE})
	if err != nil || resCached.Categories[0].Code != "category-ai-chat" || resCached.Categories[0].Entries != 2 {
		t.Fatalf("cache changed through cached response: %+v, %v", resCached, err)
	}

	// 3. Rewrite file with newer modTime -> cache invalidates and returns new categories
	newSite := &xgeo.GeoSiteList{Entry: []*xgeo.GeoSite{
		{Code: "github", Domain: []*xgeo.Domain{{Value: "github.com"}}},
	}}
	newSiteBytes, _ := proto.Marshal(newSite)
	if err := os.WriteFile(sitePath, newSiteBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(2 * time.Hour)
	if err := os.Chtimes(sitePath, later, later); err != nil {
		t.Fatal(err)
	}

	res3, err := s.ListGeoCategories(ctx, &pb.ListGeoCategoriesRequest{Kind: pb.GeoKind_GEO_KIND_SITE})
	if err != nil {
		t.Fatalf("ListGeoCategories after update failed: %v", err)
	}
	if len(res3.Categories) != 1 || res3.Categories[0].Code != "github" {
		t.Fatalf("expected invalidated cache to load new category, got %+v", res3.Categories)
	}
}
