package server

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/Gozargah/Masir/libs/masir-daemon/internal/geodata"
	pb "github.com/Gozargah/Masir/libs/masir-proto/gen/masir/daemon/v1"
)

type geoCacheEntry struct {
	modTime time.Time
	size    int64
	cats    []*pb.GeoCategory
}

func (s *Server) ListGeoCategories(_ context.Context, req *pb.ListGeoCategoriesRequest) (*pb.ListGeoCategoriesResponse, error) {
	var file string
	switch req.Kind {
	case pb.GeoKind_GEO_KIND_SITE:
		file = "geosite.dat"
	case pb.GeoKind_GEO_KIND_IP:
		file = "geoip.dat"
	default:
		return nil, status.Error(codes.InvalidArgument, "kind must be site or ip")
	}

	path := filepath.Join(s.xray.AssetDir(), file)
	fi, err := os.Stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, status.Errorf(codes.FailedPrecondition, "%s isn't installed; `masir update geo` downloads it", file)
	case err != nil:
		return nil, status.Errorf(codes.Internal, "read %s: %v", file, err)
	}

	s.geoMu.RLock()
	cached, ok := s.geoCache[path]
	if ok && cached.modTime.Equal(fi.ModTime()) && cached.size == fi.Size() {
		out := cloneCategories(cached.cats)
		s.geoMu.RUnlock()
		return &pb.ListGeoCategoriesResponse{Categories: out}, nil
	}
	s.geoMu.RUnlock()

	s.geoMu.Lock()
	defer s.geoMu.Unlock()
	if cached, ok := s.geoCache[path]; ok && cached.modTime.Equal(fi.ModTime()) && cached.size == fi.Size() {
		out := cloneCategories(cached.cats)
		return &pb.ListGeoCategoriesResponse{Categories: out}, nil
	}

	cats, err := geodata.Categories(path)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "read %s: %v", file, err)
	}
	out := make([]*pb.GeoCategory, len(cats))
	for i, c := range cats {
		out[i] = &pb.GeoCategory{Code: c.Code, Entries: uint32(c.Entries)}
	}
	if s.geoCache == nil {
		s.geoCache = map[string]geoCacheEntry{}
	}
	s.geoCache[path] = geoCacheEntry{
		modTime: fi.ModTime(),
		size:    fi.Size(),
		cats:    out,
	}
	return &pb.ListGeoCategoriesResponse{Categories: cloneCategories(out)}, nil
}

func cloneCategories(cats []*pb.GeoCategory) []*pb.GeoCategory {
	out := make([]*pb.GeoCategory, len(cats))
	for i, cat := range cats {
		out[i] = proto.Clone(cat).(*pb.GeoCategory)
	}
	return out
}
