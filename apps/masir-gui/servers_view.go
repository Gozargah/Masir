package main

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	pb "github.com/Gozargah/Masir/libs/masir-proto/gen/masir/daemon/v1"
)

// maxServerListHeight caps the picker; below it, the list keeps the height it has with
// no search while a search narrows its rows.
const maxServerListHeight float32 = 300

func (a *app) serverPicker(c *ui.Context, p palette) {
	servers := a.filteredServers()
	groups := orderedGroups(servers)

	panelBar(c, p, func() { searchInput(c, "server-search", &a.query, "Search servers…", p) }, func() {
		ui.Row(c).Gap(tw(1)).AlignItems(ui.Center).Children(func() {
			busy := len(a.testing) > 0
			if iconButton(c, "test-all", iconZap, map[bool]string{true: "Testing latency...", false: "Test Latency (All nodes)"}[busy], p, iconMD, variantSubtle, 12) && !busy {
				a.testServers()
			}
		})
	})
	height := a.serverListH
	if height <= 0 {
		height = maxServerListHeight
	}
	if len(servers) == 0 {
		ui.Column(c.Key("server-empty")).Height(height).AlignItems(ui.Center).Justify(ui.Center).Gap(tw(1)).Children(func() {
			label(c, "No matching servers", sizeSubhead, 18, 500, p.muted)
			label(c, "Try a different search term", sizeMicro, 14, 400, p.tertiary)
		})
		return
	}
	active, _ := a.activeKey()
	items := make([]serverListItem, 0, len(servers)+len(groups))
	for i := range groups {
		g := &groups[i]
		items = append(items, serverListItem{group: g})
		collapsed := a.isGroupCollapsed(g.id) && a.query == ""
		for _, s := range g.servers {
			if !collapsed || s.key == active {
				items = append(items, serverListItem{srv: s})
			}
		}
	}
	a.serverItems = items
	queryCleared := a.query == "" && a.lastServerQuery != ""
	a.lastServerQuery = a.query
	if a.scrollToSubID != "" && len(items) > 0 {
		target := -1
		for i, it := range items {
			if it.group != nil && it.group.id == a.scrollToSubID {
				target = i
				break
			}
		}
		if target >= 0 {
			a.serversList.ScrollTo(target, ui.Start)
			a.serverScrollRow = target
			a.serverScrollKey = serverItemKey(items[target])
			a.lastServerTop = target
			a.scrollToSubID = ""
			a.serverScrollRestored = true
		}
	} else if (!a.serverScrollRestored || queryCleared) && len(items) > 0 {
		target := findServerScrollRow(items, a.serverScrollKey, a.serverScrollRow)
		if target > 0 {
			a.serversList.ScrollTo(target, ui.Start)
		}
		a.serverScrollRestored = true
		a.lastServerTop = target
	}
	list := ui.List(c, &a.serversList, len(items), func(i int) {
		it := a.serverItems[i]
		if it.group != nil {
			a.groupHeader(c, p, *it.group)
			return
		}
		a.serverItem(c, p, it.srv)
	}).Padding(0, tw(1.5), 0, tw(1)).Gap(2).Shrink(0)
	if a.query == "" {
		list.MaxHeight(maxServerListHeight)
		if h := list.Bounds().H; h > 0 {
			a.serverListH = h
		}
	} else {
		list.Height(height)
	}
	first, last := a.serversList.Visible()
	if first >= 0 && last >= first && a.serverScrollRestored && a.query == "" {
		if a.lastServerTop >= 0 && first != a.lastServerTop {
			a.lastServerTop = first
			a.serverScrollRow = first
			if first < len(a.serverItems) {
				a.serverScrollKey = serverItemKey(a.serverItems[first])
			}
			a.saveGUISettings()
		} else if a.lastServerTop < 0 {
			a.lastServerTop = first
		}
	}
}

func serverItemKey(it serverListItem) string {
	if it.group != nil {
		return "g:" + it.group.id
	}
	return "s:" + it.srv.key.sub + ":" + it.srv.key.cfg
}

func findServerScrollRow(items []serverListItem, key string, savedRow int) int {
	if key != "" {
		for i, it := range items {
			if serverItemKey(it) == key {
				return i
			}
		}
		if strings.HasPrefix(key, "s:") {
			parts := strings.SplitN(key[2:], ":", 2)
			if len(parts) > 0 {
				groupKey := "g:" + parts[0]
				for i, it := range items {
					if serverItemKey(it) == groupKey {
						return i
					}
				}
			}
		}
	}
	if savedRow > 0 {
		if savedRow >= len(items) {
			return len(items) - 1
		}
		return savedRow
	}
	return -1
}

type serverGroup struct {
	id      string
	name    string
	servers []server
}

// serverListItem is one row of the server picker: a subscription's header, or a server.
type serverListItem struct {
	group *serverGroup
	srv   server
}

// orderedGroups gathers servers by subscription, in the order the subscriptions come.
func orderedGroups(servers []server) []serverGroup {
	var out []serverGroup
	idx := map[string]int{}
	for _, s := range servers {
		name := s.subName
		if name == "" {
			name = "Custom Outbounds"
		}
		subID := s.key.sub
		if subID == "" {
			subID = "custom"
		}
		i, ok := idx[subID]
		if !ok {
			i = len(out)
			idx[subID] = i
			out = append(out, serverGroup{id: subID, name: name})
		}
		out[i].servers = append(out[i].servers, s)
	}
	return out
}

func (a *app) groupHeader(c *ui.Context, p palette, g serverGroup) {
	subID := g.id
	collapsed := a.isGroupCollapsed(subID) && a.query == ""
	// Pinned headers cover the rows scrolling underneath them.
	ui.Column(c).Background(p.listHeader).Children(func() {
		ui.Row(c).Padding(tw(1), tw(2)).Radius(tw(1)).AlignItems(ui.Center).Justify(ui.SpaceBetween).Shrink(0).Children(func() {
			ui.Row(c).Gap(tw(1)).AlignItems(ui.Center).Grow(1).Basis(0).MinWidth(0).Children(func() {
				chevron := iconChevronDown
				collapseTitle := "Collapse " + g.name
				if collapsed {
					chevron = iconChevronRight
					collapseTitle = "Expand " + g.name
				}
				if iconButton(c, "group-collapse-"+subID, chevron, collapseTitle, p, iconXS, variantGhost, 11) {
					a.toggleGroupCollapse(subID)
				}
				name := ui.ButtonBase(c.Key("subscription-" + subID)).Grow(1).Basis(0).MinWidth(0).Clip().Cursor(ui.CursorPointer)
				name.Children(func() {
					label(c, strings.ToUpper(ltr(g.name)), sizeMicro, 18, 600, p.muted).LetterSpacing(0.5).SingleLine().Grow(1).MinWidth(0).MarginEnd(tw(2))
				})
				if name.Clicked() {
					if subID != "" && subID != "custom" {
						a.openSubscription(subID)
					}
				}
			})
			ui.Row(c).Gap(tw(1.5)).AlignItems(ui.Center).Shrink(0).Children(func() {
				n := fmt.Sprintf("%d node", len(g.servers))
				if len(g.servers) > 1 {
					n += "s"
				}
				countBtn := ui.ButtonBase(c.Key("group-count-" + subID)).Cursor(ui.CursorPointer)
				countBtn.Children(func() {
					count := label(c, n, sizeCaption, 18, 400, p.muted)
					if p.dark {
						count.Opacity(0.75)
					}
				})
				if countBtn.Clicked() {
					a.toggleGroupCollapse(subID)
				}
				if iconButton(c, "group-test-"+subID, iconZap, "Test all "+g.name+" configs", p, iconXS, variantGhost, 10) {
					a.testServers(g.servers...)
				}
			})
		})
		for _, s := range a.subscriptions.subscriptions {
			if s.Id == subID && s.LastError != "" {
				label(c, "Refresh failed: "+s.LastError, sizeCaption, 15, 400, p.errorC).Padding(0, tw(2), tw(1)).MaxLines(2)
			}
		}
	})
}

func (a *app) serverItem(c *ui.Context, p palette, s server) {
	active, _ := a.activeKey()
	selected := active == s.key
	row := ui.ButtonBase(c.Key(s.key)).Padding(tw(1.5), tw(1), tw(1.5), tw(2)).Radius(tw(2)).MinHeight(38).Cursor(ui.CursorPointer).
		AlignItems(ui.Center).Justify(ui.SpaceBetween).Shrink(0)
	bg := ui.Color{}
	switch {
	case selected:
		bg = p.healthySurface
	case row.Hovered():
		bg = p.cardHover
	}
	tint(row, bg)
	row.Children(func() {
		flag, title, detail := serverTitle(s.cfg.Name)
		ui.Row(c).Gap(tw(2.5)).AlignItems(ui.Center).Grow(1).Basis(0).MinWidth(0).MarginEnd(tw(2)).Children(func() {
			iconTile(c, p, func() { flagElement(c, flag, 18) })
			ui.Column(c).Grow(1).Basis(0).MinWidth(0).Children(func() {
				ltrLabel(c, title, sizeBody, 16, 600, p.text)
				if detail != "" {
					ltrLabel(c, detail, sizeCaption, 14, 400, p.muted)
				}
			})
		})
		ui.Row(c).Gap(tw(1.5)).AlignItems(ui.Center).Shrink(0).Children(func() {
			if latencyBadge(c, "lat", a.latency[s.key], a.testing[s.key], p) {
				a.testServers(s)
			}
		})
	})
	if row.Clicked() {
		a.selectServer(s)
	}
}

// serverRow shows the selected server, or guidance when none is selected.
func (a *app) serverRow(c *ui.Context, p palette) {
	s, ok := a.active()
	row := ui.ButtonBase(c.Key("server-row")).Padding(tw(2.5), tw(3)).Gap(tw(2)).Cursor(ui.CursorPointer).
		AlignItems(ui.Center).Justify(ui.SpaceBetween).Shrink(0)
	bg := ui.Color{}
	if row.Hovered() {
		bg = p.cardHover
	}
	tint(row, bg)
	row.Children(func() {
		flag, title, detail := serverTitle(s.cfg.GetName())
		if !ok {
			flag, title, detail = "🌐", "Select a server", ""
		}
		ui.Column(c).Grow(1).Basis(0).MinWidth(0).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(tw(1)).Children(func() {
				flagElementSize(c, flag, 13*4.0/3, 13)
				ltrLabel(c, title, sizeTitle, 17, 600, p.text)
			})
			subtitle := "Choose a server to connect"
			if !ok && len(a.subscriptions.servers) == 0 {
				subtitle = "Add a subscription to get started"
				if !a.reachable {
					subtitle = "Waiting for available servers"
				}
			}
			if ok {
				sub := s.subName
				extra := detail
				if extra == "" {
					extra = "Default server"
					if a.fallback() == pb.Fallback_FALLBACK_DIRECT {
						extra = "Proxy server"
					}
				}
				subtitle = sub + " • " + extra
			}
			ltrLabel(c, subtitle, sizeCaption, 14, 400, p.muted).MaxWidth(180)
		})
		ui.Row(c).Gap(tw(1)).AlignItems(ui.Center).Shrink(0).Children(func() {
			if ok {
				if latencyBadge(c, "active-latency", a.latency[s.key], a.testing[s.key], p) {
					a.testServers(s)
				}
			}
			ui.Box(c).Size(tw(4), tw(4)).Center().Shrink(0).Children(func() {
				ui.Icon(c, iconChevronDown).FontSize(15).TextColor(p.tertiary).Rotate(180 * a.motion.servers.x)
			})
		})
	})
	if row.Clicked() {
		a.toggle(sectionServers)
	}
}
