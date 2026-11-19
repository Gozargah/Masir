package xray

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/xtls/xray-core/app/observatory"
	"github.com/xtls/xray-core/common"
	xserial "github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/extension"
	confserial "github.com/xtls/xray-core/infra/conf/serial"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// liveObservatory is the Xray feature referenced by every native balancer.
// Individual native observers can be started and replaced behind it.
type liveObservatory struct {
	mu      sync.RWMutex
	current map[string]extension.Observatory
}

var registerLiveObservatory = sync.OnceFunc(func() {
	common.Must(common.RegisterConfig((*wrapperspb.BoolValue)(nil), func(context.Context, interface{}) (interface{}, error) {
		return new(liveObservatory), nil
	}))
})

func (*liveObservatory) Type() interface{} { return extension.ObservatoryType() }
func (*liveObservatory) Start() error      { return nil }

func (o *liveObservatory) Close() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	var err error
	for _, observer := range o.current {
		err = errors.Join(err, observer.Close())
	}
	o.current = nil
	return err
}

func (o *liveObservatory) GetObservation(ctx context.Context) (proto.Message, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	result := &observatory.ObservationResult{}
	for _, observer := range o.current {
		report, err := observer.GetObservation(ctx)
		if err != nil {
			continue // a failed probe source must leave other profiles usable
		}
		if observed, ok := report.(*observatory.ObservationResult); ok {
			result.Status = append(result.Status, observed.Status...)
		}
	}
	return result, nil
}

// configure runs under Core.mu. Keep unchanged observers and their probe history.
func (o *liveObservatory) configure(inst *core.Instance, p *Profile) error {
	next := make(map[string]extension.Observatory, len(p.observers))
	var started []extension.Observatory
	for _, spec := range p.observers {
		raw, err := json.Marshal(spec)
		if err != nil {
			return err
		}
		key := string(raw)
		if next[key] != nil {
			continue
		}
		if previous := o.current[key]; previous != nil {
			next[key] = previous
			continue
		}
		observer, err := startObserver(inst, raw)
		if err != nil {
			for _, added := range started {
				_ = added.Close()
			}
			return err
		}
		started = append(started, observer)
		next[key] = observer
	}
	o.mu.Lock()
	for key, observer := range o.current {
		if next[key] == nil {
			next[key] = observer
		}
	}
	o.current = next
	o.mu.Unlock()
	return nil
}

func (o *liveObservatory) retain(p *Profile) {
	keep := make(map[string]bool, len(p.observers))
	for _, spec := range p.observers {
		keep[string(marshal(spec))] = true
	}
	var removed []extension.Observatory
	o.mu.Lock()
	for key, observer := range o.current {
		if !keep[key] {
			delete(o.current, key)
			removed = append(removed, observer)
		}
	}
	o.mu.Unlock()
	for _, observer := range removed {
		_ = observer.Close()
	}
}

func startObserver(inst *core.Instance, raw []byte) (extension.Observatory, error) {
	cfg, err := confserial.LoadJSONConfig(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	for _, app := range cfg.App {
		switch app.Type {
		case "xray.core.app.observatory.Config", "xray.core.app.observatory.burst.Config":
			settings, err := app.GetInstance()
			if err != nil {
				return nil, err
			}
			obj, err := core.CreateObject(inst, settings)
			if err != nil {
				return nil, err
			}
			observer := obj.(extension.Observatory)
			if err := observer.Start(); err != nil {
				_ = observer.Close()
				return nil, err
			}
			return observer, nil
		}
	}
	return nil, fmt.Errorf("missing Xray observatory configuration")
}

func installObservatory(cfg *core.Config) {
	registerLiveObservatory()
	cfg.App = append(cfg.App, xserial.ToTypedMessage(wrapperspb.Bool(true)))
}
