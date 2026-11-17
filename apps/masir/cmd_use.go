package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/Gozargah/Masir/apps/masir/internal/ui"
	pb "github.com/Gozargah/Masir/libs/masir-proto/gen/masir/daemon/v1"
)

func cmdUse(a args) error {
	all, err := loadNodes()
	if err != nil {
		return err
	}
	if len(all) == 0 {
		return advised("There are no subscriptions yet.", "Add one:", "masir sub add <url>")
	}

	var key, reason string
	asJSON := a.boolean("json", false)
	switch {
	case a.boolean("fastest", false):
		key, err = fastest(all, asJSON)
		reason = "fastest"
	case a.arg(0) != "":
		s, cfg, ok := findNode(all, a.arg(0))
		if !ok {
			return fail("outbound node with ID %q not found in any subscription", a.arg(0))
		}
		key = nodeKey(s.sub.Id, cfg.Id)
		reason = "id"
	case !ui.Interactive():
		return advised("Without a terminal, pass a proxy id, or --fastest.", "The ids are listed by:", "masir sub list --nodes")
	default:
		key, err = pickNode(all, "Choose active outbound configuration", activeKey())
		reason = "interactive"
	}
	if err != nil {
		return err
	}
	if asJSON {
		name, err := switchConfig(all, key, reason)
		if err != nil {
			return err
		}
		b, err := marshalUse(key, name, reason)
		if err != nil {
			return err
		}
		fmt.Println(string(b))
		return nil
	}
	return selectNode(all, key, "Active outbound switched to", reason)
}

// marshalUse renders `use --json`: the selection just made, in the same
// active shape as `sub list --json`.
func marshalUse(key, name, reason string) ([]byte, error) {
	subID, cfgID := splitKey(key)
	return json.MarshalIndent(map[string]any{
		"active": map[string]string{"subscriptionId": subID, "configId": cfgID},
		"name":   name,
		"reason": reason,
	}, "", "  ")
}

// switchConfig runs the SelectConfig RPC and returns the chosen config's
// display name — the shared step behind `use`'s human and JSON output.
func switchConfig(all []subNodes, key, reason string) (string, error) {
	subID, cfgID := splitKey(key)
	if _, err := call(func(ctx context.Context, c pb.DaemonServiceClient) (*pb.SelectConfigResponse, error) {
		return c.SelectConfig(ctx, &pb.SelectConfigRequest{SubscriptionId: subID, ConfigId: cfgID, Reason: reason})
	}); err != nil {
		return "", err
	}
	_, cfg, _ := findNode(all, key)
	if cfg == nil {
		return "", nil
	}
	return cfg.Name, nil
}

func selectNode(all []subNodes, key, verb, reason string) error {
	name, err := switchConfig(all, key, reason)
	if err != nil {
		return err
	}
	_, cfgID := splitKey(key)
	ui.Success(fmt.Sprintf("%s %q [%s].", verb, ui.Visual(name), cfgID))

	st, err := currentStatus()
	if err != nil {
		return err
	}
	switch {
	case !st.Settings.GetPower() && !st.ProxyRunning:
		ui.Info("masir is off; turn it on with `masir connect`.")
	case st.Settings.GetFallback() == pb.Fallback_FALLBACK_DIRECT:
		ui.Info("Unmatched traffic goes direct; send it through this proxy with `masir mode proxy`.")
	}
	return nil
}

// fastest tests every config and returns the quickest one's key. quiet keeps
// stdout clean for JSON output: no spinner frames, no outcome line — the
// spinner's Stop writes to stdout and would corrupt the JSON document.
func fastest(all []subNodes, quiet bool) (string, error) {
	c, err := daemon()
	if err != nil {
		return "", err
	}
	total := 0
	for _, s := range all {
		total += len(s.configs)
	}
	var sp *ui.Spinner
	if !quiet {
		sp = ui.Spin(fmt.Sprintf("Testing %d proxies", total))
	}

	var (
		mu       sync.Mutex
		done     int
		best     string
		bestMs   int64 = -1
		bestName string
		wg       sync.WaitGroup
	)
	names := map[string]string{}
	for _, s := range all {
		for _, cfg := range s.configs {
			names[nodeKey(s.sub.Id, cfg.Id)] = cfg.Name
		}
	}
	for _, s := range all {
		ids := make([]string, len(s.configs))
		for i, cfg := range s.configs {
			ids[i] = cfg.Id
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = probe(appCtx, c, s.sub.Id, ids, func(cfgID string, l *pb.LatencyResult) {
				mu.Lock()
				defer mu.Unlock()
				done++
				if sp != nil {
					sp.Message(fmt.Sprintf("Testing proxies... %d/%d", done, total))
				}
				if l.Error == "" && (bestMs < 0 || l.LatencyMs < bestMs) {
					best, bestMs, bestName = nodeKey(s.sub.Id, cfgID), l.LatencyMs, names[nodeKey(s.sub.Id, cfgID)]
				}
			})
		}()
	}
	wg.Wait()
	if appCtx.Err() != nil {
		return "", appCtx.Err()
	}
	if best == "" {
		if sp != nil {
			sp.Fail("No proxy responded.")
		}
		return "", errReported
	}
	if sp != nil {
		sp.Stop(fmt.Sprintf("Fastest: %s (%dms)", ui.Visual(bestName), bestMs))
	}
	return best, nil
}
