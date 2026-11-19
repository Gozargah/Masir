package xray

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/xtls/xray-core/app/router"
	"github.com/xtls/xray-core/common"
	xserial "github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/routing"
	"github.com/xtls/xray-core/infra/conf"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

type scopedRouter struct {
	*router.Router
	publication     sync.Mutex
	readers         int
	onIdle          func()
	staging         bool
	observersOnIdle func()
	profiles        atomic.Pointer[map[string]*profileRouter]
}

var registerScopedRouter = sync.OnceFunc(func() {
	common.Must(common.RegisterConfig((*wrapperspb.StringValue)(nil), func(ctx context.Context, value interface{}) (interface{}, error) {
		raw, err := base64.StdEncoding.DecodeString(value.(*wrapperspb.StringValue).Value)
		if err != nil {
			return nil, err
		}
		cfg := new(router.Config)
		if err := proto.Unmarshal(raw, cfg); err != nil {
			return nil, err
		}
		native, err := common.CreateObject(ctx, cfg)
		if err != nil {
			return nil, err
		}
		return &scopedRouter{Router: native.(*router.Router)}, nil
	}))
})

func installScopedRouter(cfg *core.Config) {
	registerScopedRouter()
	name := string((&router.Config{}).ProtoReflect().Descriptor().FullName())
	for i, app := range cfg.App {
		if app.Type == name {
			cfg.App[i] = xserial.ToTypedMessage(wrapperspb.String(base64.StdEncoding.EncodeToString(app.Value)))
			return
		}
	}
}

func (r *scopedRouter) PickRoute(ctx routing.Context) (routing.Route, error) {
	r.publication.Lock()
	outer, profiles := r.Router, r.profiles.Load()
	r.readers++
	r.publication.Unlock()
	defer func() {
		r.publication.Lock()
		defer r.publication.Unlock()
		r.readers--
		r.cleanupIfIdle()
	}()
	route, err := outer.PickRoute(ctx)
	if err != nil {
		return nil, err
	}
	if profiles != nil {
		if scope := (*profiles)[route.GetOutboundTag()]; scope != nil {
			return scope.pick(ctx), nil
		}
	}
	return route, nil
}

type profileRouter struct {
	router        *router.Router
	prefix        string
	firstOutbound string
	loopbacks     map[string]string
}

type profileRouteContext struct {
	routing.Context
	inbound  string
	loopback bool
}

func (c profileRouteContext) GetInboundTag() string { return c.inbound }
func (c profileRouteContext) GetSkipDNSResolve() bool {
	return c.loopback || c.Context.GetSkipDNSResolve()
}

type localRoute struct {
	routing.Context
	tag string
}

func (r *localRoute) GetOutboundTag() string       { return r.tag }
func (*localRoute) GetOutboundGroupTags() []string { return nil }
func (*localRoute) GetRuleTag() string             { return "" }

func (p *profileRouter) pick(ctx routing.Context) routing.Route {
	request := profileRouteContext{Context: ctx, inbound: p.prefix + "client"}
	var seen map[string]bool
	for range 64 {
		route, err := p.router.PickRoute(request)
		tag := p.firstOutbound
		switch {
		case err == nil:
			tag = route.GetOutboundTag()
		case !errors.Is(err, common.ErrNoClue) || tag == "":
			return &localRoute{Context: ctx, tag: "block"}
		}
		step := p.loopbacks[tag]
		if step == "" {
			if route != nil {
				return route
			}
			return &localRoute{Context: ctx, tag: tag}
		}
		if seen == nil {
			seen = make(map[string]bool)
		}
		if seen[step] {
			break
		}
		seen[step] = true
		request.inbound = p.prefix + "step/" + step
		request.loopback = true
	}
	return &localRoute{Context: ctx, tag: "block"}
}

func (r *scopedRouter) configureScopes(inst *core.Instance, p *Profile) error {
	next := make(map[string]*profileRouter, len(p.scopes))
	if previous := r.profiles.Load(); previous != nil {
		for tag, scope := range *previous {
			next[tag] = scope
		}
	}
	for tag, raw := range p.scopes {
		if _, exists := next[tag]; exists {
			continue
		}
		var settings struct {
			conf.RouterConfig
			FirstOutbound string            `json:"firstOutbound"`
			Loopbacks     map[string]string `json:"loopbacks"`
		}
		if err := json.Unmarshal(raw, &settings); err != nil {
			return err
		}
		cfg, err := settings.Build()
		if err != nil {
			return fmt.Errorf("build provider routing: %w", err)
		}
		if err := validateRuleAttributes(cfg); err != nil {
			return err
		}
		obj, err := core.CreateObject(inst, cfg)
		if err != nil {
			return fmt.Errorf("prepare provider routing: %w", err)
		}
		scope := obj.(*router.Router)
		if err := scope.Start(); err != nil {
			return err
		}
		next[tag] = &profileRouter{router: scope, prefix: strings.TrimPrefix(tag, "masir-profile-route/"),
			firstOutbound: settings.FirstOutbound, loopbacks: settings.Loopbacks}
	}
	r.profiles.Store(&next)
	return nil
}

// retainScopes runs under the publication lock after the new table is visible.
func (r *scopedRouter) retainScopes(p *Profile) {
	if existing := r.profiles.Load(); existing != nil {
		next := make(map[string]*profileRouter, len(p.scopes))
		for tag := range p.scopes {
			next[tag] = (*existing)[tag]
		}
		r.profiles.Store(&next)
	}
}

// cleanupIfIdle runs under publication and keeps route lookups independent of
// each other, including DNS lookups that re-enter routing.
func (r *scopedRouter) cleanupIfIdle() {
	if r.readers != 0 || r.staging {
		return
	}
	if r.onIdle != nil {
		r.onIdle()
		r.onIdle = nil
	}
	if r.observersOnIdle != nil {
		r.observersOnIdle()
		r.observersOnIdle = nil
	}
}
