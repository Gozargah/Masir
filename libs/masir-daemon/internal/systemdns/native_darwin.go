package systemdns

import (
	"context"
	"fmt"
	"runtime"
	"sync"

	"github.com/ebitengine/purego"
)

// All references are CoreFoundation objects, never Go pointers retained by C.
// Frameworks remain loaded for the lifetime of the process. This works in the
// CGO_ENABLED=0 CLI and the desktop's bundled daemon alike.
type native struct {
	release          func(uintptr)
	stringCreate     func(uintptr, string, uint32) uintptr
	stringLength     func(uintptr) int64
	stringGet        func(uintptr, []byte, int64, uint32) bool
	arrayCreate      func(uintptr, int64, uintptr) uintptr
	arrayAppend      func(uintptr, uintptr)
	arrayCount       func(uintptr) int64
	arrayGet         func(uintptr, int64) uintptr
	dictCopy         func(uintptr, int64, uintptr) uintptr
	dictCreate       func(uintptr, int64, uintptr, uintptr) uintptr
	dictGet          func(uintptr, uintptr) uintptr
	dictSet          func(uintptr, uintptr, uintptr)
	dictRemove       func(uintptr, uintptr)
	dictCount        func(uintptr) int64
	prefsCreate      func(uintptr, uintptr, uintptr) uintptr
	prefsLock        func(uintptr, bool) bool
	prefsUnlock      func(uintptr) bool
	prefsCommit      func(uintptr) bool
	prefsApply       func(uintptr) bool
	servicesCopy     func(uintptr) uintptr
	currentSet       func(uintptr) uintptr
	setServices      func(uintptr) uintptr
	serviceID        func(uintptr) uintptr
	serviceEnabled   func(uintptr) bool
	serviceInterface func(uintptr) uintptr
	interfaceType    func(uintptr) uintptr
	protocolGet      func(uintptr, uintptr) uintptr
	protocolConfig   func(uintptr) uintptr
	protocolSet      func(uintptr, uintptr) bool
	storeCreate      func(uintptr, uintptr, uintptr, uintptr) uintptr
	storeNotify      func(uintptr, uintptr, uintptr) bool
	storeSource      func(uintptr, uintptr, int64) uintptr
	loopCurrent      func() uintptr
	loopAdd          func(uintptr, uintptr, uintptr)
	loopRunMode      func(uintptr, float64, bool) int32
	loopRemove       func(uintptr, uintptr, uintptr)
	scError          func() int32
	arrayCallbacks   uintptr
	keyCallbacks     uintptr
	valueCallbacks   uintptr
}

var nativeOnce sync.Once
var nativeAPI *native
var nativeErr error
var watchers sync.Map // SCDynamicStoreRef -> chan<- struct{}
var notifyCallback uintptr

func newNative() (*native, error) {
	nativeOnce.Do(func() { nativeAPI, nativeErr = loadNative() })
	return nativeAPI, nativeErr
}

func loadNative() (*native, error) {
	cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		return nil, err
	}
	sc, err := purego.Dlopen("/System/Library/Frameworks/SystemConfiguration.framework/SystemConfiguration", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		return nil, err
	}
	n := new(native)
	for _, lib := range []struct {
		handle uintptr
		funcs  map[string]any
	}{
		{cf, map[string]any{
			"CFRelease": &n.release, "CFStringCreateWithCString": &n.stringCreate,
			"CFStringGetLength": &n.stringLength, "CFStringGetCString": &n.stringGet,
			"CFArrayCreateMutable": &n.arrayCreate, "CFArrayAppendValue": &n.arrayAppend,
			"CFArrayGetCount": &n.arrayCount, "CFArrayGetValueAtIndex": &n.arrayGet,
			"CFDictionaryCreateMutableCopy": &n.dictCopy, "CFDictionaryCreateMutable": &n.dictCreate,
			"CFDictionaryGetValue": &n.dictGet, "CFDictionarySetValue": &n.dictSet,
			"CFDictionaryRemoveValue": &n.dictRemove, "CFDictionaryGetCount": &n.dictCount,
			"CFRunLoopGetCurrent": &n.loopCurrent, "CFRunLoopAddSource": &n.loopAdd,
			"CFRunLoopRunInMode": &n.loopRunMode, "CFRunLoopRemoveSource": &n.loopRemove,
		}},
		{sc, map[string]any{
			"SCPreferencesCreate": &n.prefsCreate, "SCPreferencesLock": &n.prefsLock,
			"SCPreferencesUnlock": &n.prefsUnlock, "SCPreferencesCommitChanges": &n.prefsCommit,
			"SCPreferencesApplyChanges": &n.prefsApply, "SCNetworkServiceCopyAll": &n.servicesCopy,
			"SCNetworkSetCopyCurrent": &n.currentSet, "SCNetworkSetCopyServices": &n.setServices,
			"SCNetworkServiceGetServiceID": &n.serviceID, "SCNetworkServiceGetEnabled": &n.serviceEnabled,
			"SCNetworkServiceGetInterface": &n.serviceInterface, "SCNetworkInterfaceGetInterfaceType": &n.interfaceType,
			"SCNetworkServiceCopyProtocol": &n.protocolGet, "SCNetworkProtocolGetConfiguration": &n.protocolConfig,
			"SCNetworkProtocolSetConfiguration": &n.protocolSet,
			"SCDynamicStoreCreate":              &n.storeCreate, "SCDynamicStoreSetNotificationKeys": &n.storeNotify,
			"SCDynamicStoreCreateRunLoopSource": &n.storeSource, "SCError": &n.scError,
		}},
	} {
		for name, target := range lib.funcs {
			addr, err := purego.Dlsym(lib.handle, name)
			if err != nil {
				return nil, err
			}
			purego.RegisterFunc(target, addr)
		}
	}
	for name, target := range map[string]*uintptr{
		"kCFTypeArrayCallBacks":           &n.arrayCallbacks,
		"kCFTypeDictionaryKeyCallBacks":   &n.keyCallbacks,
		"kCFTypeDictionaryValueCallBacks": &n.valueCallbacks,
	} {
		addr, err := purego.Dlsym(cf, name)
		if err != nil {
			return nil, err
		}
		*target = addr
	}
	notifyCallback = purego.NewCallback(func(store, keys, info uintptr) {
		if ch, ok := watchers.Load(store); ok {
			select {
			case ch.(chan<- struct{}) <- struct{}{}:
			default:
			}
		}
	})
	return n, nil
}

const utf8 = 0x08000100

func (n *native) str(s string) uintptr { return n.stringCreate(0, s, utf8) }
func (n *native) text(s uintptr) string {
	if s == 0 {
		return ""
	}
	buf := make([]byte, n.stringLength(s)*4+1)
	if !n.stringGet(s, buf, int64(len(buf)), utf8) {
		return ""
	}
	for i, b := range buf {
		if b == 0 {
			return string(buf[:i])
		}
	}
	return ""
}
func (n *native) strings(a uintptr) []string {
	if a == 0 {
		return nil
	}
	var out []string
	for i := int64(0); i < n.arrayCount(a); i++ {
		out = append(out, n.text(n.arrayGet(a, i)))
	}
	return out
}
func (n *native) array(ss []string) uintptr {
	a := n.arrayCreate(0, 0, n.arrayCallbacks)
	for _, s := range ss {
		ref := n.str(s)
		n.arrayAppend(a, ref)
		n.release(ref)
	}
	return a
}
func (n *native) err(op string) error {
	return fmt.Errorf("%s: SystemConfiguration error %d", op, n.scError())
}

func (n *native) edit(fn func([]service, func(string, []string) error) error) error {
	name := n.str("Masir TUN DNS")
	defer n.release(name)
	prefs := n.prefsCreate(0, name, 0)
	if prefs == 0 {
		return n.err("create preferences")
	}
	defer n.release(prefs)
	// Never wait indefinitely for another network configuration tool.
	if !n.prefsLock(prefs, false) {
		return n.err("lock network preferences")
	}
	defer n.prefsUnlock(prefs)
	all := n.servicesCopy(prefs)
	if all == 0 {
		return n.err("enumerate network services")
	}
	defer n.release(all)
	current := n.currentSet(prefs)
	if current == 0 {
		return n.err("read current network location")
	}
	defer n.release(current)
	members := n.setServices(current)
	if members == 0 {
		return n.err("read network location services")
	}
	defer n.release(members)
	enabled := map[string]bool{}
	for i := int64(0); i < n.arrayCount(members); i++ {
		s := n.arrayGet(members, i)
		enabled[n.text(n.serviceID(s))] = n.serviceEnabled(s)
	}
	dnsType := n.str("DNS")
	defer n.release(dnsType)
	key := n.str("ServerAddresses")
	defer n.release(key)
	protocols := map[string]uintptr{}
	defer func() {
		for _, p := range protocols {
			n.release(p)
		}
	}()
	var services []service
	for i := int64(0); i < n.arrayCount(all); i++ {
		s := n.arrayGet(all, i)
		id := n.text(n.serviceID(s))
		p := n.protocolGet(s, dnsType)
		if p == 0 {
			continue
		} // no configurable DNS protocol (e.g. some VPN services)
		protocols[id] = p
		cfg := n.protocolConfig(p)
		var servers []string
		if cfg != 0 {
			servers = n.strings(n.dictGet(cfg, key))
		}
		physical := false
		if iface := n.serviceInterface(s); iface != 0 {
			switch n.text(n.interfaceType(iface)) {
			case "Ethernet", "IEEE80211", "WWAN", "Bluetooth", "FireWire", "Bond", "Bridge", "VLAN":
				physical = true
			}
		}
		services = append(services, service{ID: id, Servers: servers, Managed: physical && enabled[id]})
	}
	changed := false
	err := fn(services, func(id string, servers []string) error {
		p := protocols[id]
		if p == 0 {
			return fmt.Errorf("DNS service %s disappeared", id)
		}
		old := n.protocolConfig(p)
		var cfg uintptr
		if old != 0 {
			cfg = n.dictCopy(0, 0, old)
		} else {
			cfg = n.dictCreate(0, 0, n.keyCallbacks, n.valueCallbacks)
		}
		defer n.release(cfg)
		if len(servers) == 0 {
			n.dictRemove(cfg, key)
		} else {
			a := n.array(servers)
			n.dictSet(cfg, key, a)
			n.release(a)
		}
		value := cfg
		if n.dictCount(cfg) == 0 {
			value = 0
		}
		if !n.protocolSet(p, value) {
			return n.err("set service DNS")
		}
		changed = true
		return nil
	})
	if err != nil {
		return err
	}
	if changed {
		if !n.prefsCommit(prefs) {
			return n.err("commit service DNS")
		}
		if !n.prefsApply(prefs) {
			return n.err("apply service DNS")
		}
	}
	return nil
}

func (n *native) watch(ctx context.Context, events chan<- struct{}) (func(), error) {
	type result struct{ err error }
	ready := make(chan result, 1)
	done := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer close(done)
		name := n.str("Masir DNS network changes")
		defer n.release(name)
		store := n.storeCreate(0, name, notifyCallback, 0)
		if store == 0 {
			ready <- result{err: n.err("watch network state")}
			return
		}
		defer n.release(store)
		patterns := n.array([]string{
			"State:/Network/Global/.*", "State:/Network/Service/.*/(DNS|IPv4|IPv6)",
			"Setup:/Network/.*",
		})
		defer n.release(patterns)
		if !n.storeNotify(store, 0, patterns) {
			ready <- result{err: n.err("subscribe to network changes")}
			return
		}
		source := n.storeSource(0, store, 0)
		if source == 0 {
			ready <- result{err: n.err("create network event source")}
			return
		}
		defer n.release(source)
		mode := n.str("kCFRunLoopDefaultMode")
		defer n.release(mode)
		loop := n.loopCurrent()
		watchers.Store(store, events)
		defer watchers.Delete(store)
		n.loopAdd(loop, source, mode)
		defer n.loopRemove(loop, source, mode)
		ready <- result{}
		for ctx.Err() == nil {
			n.loopRunMode(mode, 0.25, false)
		}
	}()
	r := <-ready
	if r.err != nil {
		<-done
		return nil, r.err
	}
	// A bounded run-loop wait makes cancellation safe even before its first run.
	// Network state itself is event-driven; this timeout only bounds shutdown.
	return func() { <-done }, nil
}
