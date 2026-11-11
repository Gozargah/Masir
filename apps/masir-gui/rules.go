package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	pb "github.com/Gozargah/Masir/libs/masir-proto/gen/masir/daemon/v1"
)

// ruleSection is a titled group of rules in the list.
type ruleSection struct {
	title string
	rules []*pb.RoutingRule
}

// info is how a rule is drawn: its title, the icon or flag of its tile, and a note.
type info struct {
	title, summary, flag string
	icon                 *ui.SVG
	section              string
	appPath              string // the application's bundle, for an application rule
	domain               string // domain for fetching favicon
}

// ruleInfo reads what a rule matches out of its xray match: its first process, domain,
// address or port.
func ruleInfo(r *pb.RoutingRule) info {
	out := info{title: r.RuleTag, icon: iconGlobe, section: "Domains"}
	var m map[string]json.RawMessage
	if json.Unmarshal([]byte(r.RuleJson), &m) != nil {
		return out
	}
	list := func(key string) []string {
		raw, ok := m[key]
		if !ok {
			return nil
		}
		var l []string
		if json.Unmarshal(raw, &l) == nil {
			return l
		}
		var one string
		if json.Unmarshal(raw, &one) == nil && one != "" {
			return strings.Split(one, ",")
		}
		return nil
	}
	more := func(l []string) string {
		if len(l) > 1 {
			return fmt.Sprintf(" +%d", len(l)-1)
		}
		return ""
	}
	switch {
	case len(list("process")) > 0:
		l := list("process")
		out.title, out.icon, out.section = strings.TrimSuffix(strings.TrimPrefix(l[0], "self/"), "/")+more(l), iconApp, "Applications"
		if strings.HasSuffix(strings.TrimSuffix(l[0], "/"), ".app") {
			out.appPath = l[0]
			out.title = fallbackName(l[0]) + more(l)
		}
	case len(list("domain")) > 0:
		l := list("domain")
		first := l[0]
		out.section = "Domains"
		isGeosite := strings.HasPrefix(first, "geosite:")
		if isGeosite {
			out.section = "Website Categories"
			if title, flag, icon, ok := humanizeGeosite(first); ok {
				out.title = title + more(l)
				out.flag = flag
				if icon != nil {
					out.icon = icon
				}
			} else {
				trimmed := strings.TrimPrefix(first, "geosite:")
				out.title = trimmed + more(l)
			}
		} else {
			trimmed := first
			for _, prefix := range []string{"full:", "domain:", "regexp:"} {
				trimmed = strings.TrimPrefix(trimmed, prefix)
			}
			out.title = trimmed + more(l)
		}
		if out.flag == "" {
			for _, raw := range l {
				if dom := cleanRuleDomain(raw); dom != "" {
					out.domain = dom
					break
				}
			}
		}
	case len(list("ip")) > 0:
		l := list("ip")
		out.section, out.icon = "IP Ranges & Ports", iconLayers
		first := l[0]
		if title, flag, icon, ok := humanizeGeoIP(first); ok {
			out.title = title + more(l)
			out.flag = flag
			if icon != nil {
				out.icon = icon
			}
		} else {
			raw := strings.TrimPrefix(first, "geoip:")
			if code := raw; strings.HasPrefix(first, "geoip:") && len(code) == 2 {
				out.flag = flagFor(code)
			}
			out.title = raw + more(l)
		}
	case len(list("port")) > 0:
		l := list("port")
		out.section, out.icon = "IP Ranges & Ports", iconHash
		out.title = l[0] + more(l)
	}
	if n := list("network"); len(n) > 0 {
		out.summary = strings.ToUpper(strings.Join(n, "/"))
	}
	if out.section == "Applications" || out.section == "Domains" || out.section == "Website Categories" {
		if ip := list("ip"); len(ip) > 0 && out.section != "IP Ranges & Ports" {
			out.summary = strings.TrimSpace(out.summary + " " + ip[0])
		}
	}
	return out
}

// flagFor is the flag emoji of a two-letter country code.
func flagFor(code string) string {
	code = strings.ToUpper(code)
	return string([]rune{rune(code[0]-'A') + 0x1F1E6, rune(code[1]-'A') + 0x1F1E6})
}

// ruleMeta is what a rule shows, parsed once per rule instead of on every frame: the
// info its row draws, the section it groups into, and the lowercased text the search
// matches. json remembers the rule it was made from.
type ruleMeta struct {
	json   string
	info   info
	search string
}

// ruleListItem is one row of the rules list: a section header, or a rule.
type ruleListItem struct {
	title string // the section, when the row is a header
	count int
	rule  *pb.RoutingRule
}

// groupRules gathers rules into the sections the list shows, in a fixed order.
func (a *app) groupRules(rules []*pb.RoutingRule) []ruleSection {
	order := []string{"Applications", "Website Categories", "Domains", "IP Ranges & Ports"}
	by := map[string][]*pb.RoutingRule{}
	for _, r := range rules {
		s := a.metaFor(r).info.section
		by[s] = append(by[s], r)
	}
	var out []ruleSection
	for _, title := range order {
		if len(by[title]) > 0 {
			out = append(out, ruleSection{title: title, rules: by[title]})
		}
	}
	return out
}

// metaFor returns what a rule shows, parsing its JSON once for as long as the rule is the
// same. The rules list calls it for every rule in a frame; parsing there was the cost of
// the first open.
func (a *app) metaFor(r *pb.RoutingRule) ruleMeta {
	if m, ok := a.rulesMetas[r.RuleTag]; ok && m.json == r.RuleJson {
		return m
	}
	inf := ruleInfo(r)
	m := ruleMeta{
		json:   r.RuleJson,
		info:   inf,
		search: strings.ToLower(r.RuleTag + " " + r.Target + " " + r.RuleJson + " " + inf.title),
	}
	a.rulesMetas[r.RuleTag] = m
	return m
}

// indexRules parses every rule now, off the frames that show the list.
func (a *app) indexRules() {
	for _, r := range a.rules.rules {
		a.metaFor(r)
	}
}

// prefetchAppIcons reads the icons of application rules in the background, before the
// rules list is opened, one at a time.
func (a *app) prefetchAppIcons() {
	for _, r := range a.rules.rules {
		if p := a.metaFor(r).info.appPath; p != "" {
			a.apps.lookup(p, a.async, func() { a.post(func() {}) })
		}
	}
}

// prefetchFavicons reads and revalidates the favicons of domain/website rules in the background.
func (a *app) prefetchFavicons() {
	for _, r := range a.rules.rules {
		if d := a.metaFor(r).info.domain; d != "" {
			a.favicons.lookup(d, a.async, func() { a.post(func() {}) })
		}
	}
}

// filteredRules are the rules matching the search.
func (a *app) filteredRules() []*pb.RoutingRule {
	q := strings.ToLower(strings.TrimSpace(a.ruleQuery))
	rules := a.ruleList()
	if q == "" {
		return rules
	}
	var out []*pb.RoutingRule
	for _, r := range rules {
		if strings.Contains(a.metaFor(r).search, q) {
			out = append(out, r)
		}
	}
	return out
}

func (a *app) isRuleSectionCollapsed(title string) bool {
	if a.collapsedRules == nil {
		return false
	}
	return a.collapsedRules[title]
}

func (a *app) toggleRuleSectionCollapse(title string) {
	if a.collapsedRules == nil {
		a.collapsedRules = make(map[string]bool)
	}
	a.collapsedRules[title] = !a.collapsedRules[title]
	a.saveGUISettings()
}
