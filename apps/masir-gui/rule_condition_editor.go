package main

import (
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
)

func (a *app) ruleConditionEditor(c *ui.Context, p palette, d ruleCondition) {
	st := &a.rulePage
	switch d.key {
	case "network":
		index := 0
		switch st.values[d.key] {
		case "tcp":
			index = 1
		case "udp":
			index = 2
		}
		if ruleSegments(c, "network", &index, p, "Any", "TCP", "UDP") {
			st.values[d.key] = []string{"", "tcp", "udp"}[index]
		}
		label(c, "Any includes both TCP and UDP traffic.", sizeMicro, 15, 400, p.muted)
	case "protocol":
		label(c, "Match one or more detected protocols. Requires protocol sniffing.", sizeMicro, 15, 400, p.muted)
		t := c.Theme()
		ui.Row(c).Label("Application protocols").Padding(t.Space(0.5)).Gap(t.Space(0.5)).Radius(t.Radius).Background(ruleFieldColor(p)).Border(1, t.Border).Shrink(0).Children(func() {
			for _, value := range []string{"http", "tls", "quic", "bittorrent"} {
				entries := ruleEntries(d.key, st.values[d.key])
				selected := slices.Contains(entries, value)
				title := strings.ToUpper(value)
				if value == "bittorrent" {
					title = "BitTorrent"
				}
				button := ui.ToggleBase(c.Key(value), &selected).Label(title).Disabled(st.busy)
				ruleSegmentStyle(button, selected, t).Children(func() { ui.Text(c, title).SingleLine().TextAlign(ui.Center) })
				if button.Changed() {
					if !selected {
						var next []string
						for _, v := range entries {
							if v != value {
								next = append(next, v)
							}
						}
						entries = next
					} else {
						entries = append(entries, value)
					}
					st.values[d.key] = strings.Join(entries, "\n")
				}
			}
		})
	case "port", "sourcePort":
		value := st.values[d.key]
		field(c, p, "ports", "Ports or ranges", &value, "80, 443, 1000-2000", iconHash, p.muted, nil)
		st.values[d.key] = value
	case "process":
		a.ruleAppsEditor(c, p)
	case "domain":
		ui.Column(c).Padding(10, 10, 0).Children(func() {
			if ruleSegments(c, "domain-mode", &st.domainMode, p, "Domain", "Exact", "Category", "Regex") {
				st.entry = ""
			}
		})
		if st.domainMode == 2 {
			a.ruleGeoEditor(c, p, d)
		} else {
			ui.Column(c).Padding(0, 10, 10).Gap(8).Children(func() {
				title := []string{"Domain and subdomains", "Exact hostname", "Website category", "Regular expression"}[st.domainMode]
				placeholder := []string{"example.com", "www.example.com", "geosite:cn", "^.*\\.example\\.com$"}[st.domainMode]
				a.ruleEntryEditor(c, p, d, title, placeholder)
			})
		}
	case "ip", "source":
		title, placeholder := "IP addresses or ranges", "1.1.1.1, 10.0.0.0/8"
		if d.key == "ip" {
			placeholder = "1.1.1.1, 10.0.0.0/8 or geoip:private"
		}
		a.ruleEntryEditor(c, p, d, title, placeholder)
	}
	if d.key != "process" && len(ruleEntries(d.key, st.values[d.key])) > 0 {
		reset := func() {
			if ui.Button(c.Key("any-"+d.key), "Reset to Any").Disabled(st.busy).Clicked() {
				st.values[d.key] = ""
				st.err = ""
			}
		}
		if d.key == "domain" {
			ui.Column(c).Padding(0, 10, 10).Children(reset)
		} else {
			reset()
		}
	}
}

func (a *app) ruleEntryEditor(c *ui.Context, p palette, d ruleCondition, title, placeholder string) {
	st := &a.rulePage
	submitted := field(c, p, "condition-entry", title, &st.entry, placeholder, d.icon, p.muted, nil)
	if ui.Button(c.Key("add-entry"), "Add").Disabled(st.busy || strings.TrimSpace(st.entry) == "").Clicked() || submitted {
		a.addRuleConditionEntry(d.key)
	}
	a.ruleConditionEntries(c, p, d)
}

func (a *app) addRuleConditionEntry(key string) {
	st := &a.rulePage
	value := strings.TrimSpace(st.entry)
	if value == "" {
		return
	}
	if key == "domain" {
		prefix := []string{"domain:", "full:", "geosite:", "regexp:"}[st.domainMode]
		// Explicit Xray matchers can also be pasted directly.
		if !strings.HasPrefix(value, "domain:") && !strings.HasPrefix(value, "full:") && !strings.HasPrefix(value, "geosite:") && !strings.HasPrefix(value, "regexp:") {
			value = prefix + value
		}
	}
	if err := validateRuleCondition(key, value); err != nil {
		st.err = err.Error()
		return
	}
	entries := ruleEntries(key, st.values[key]+"\n"+value)
	st.values[key] = strings.Join(entries, "\n")
	st.entry, st.err = "", ""
}

func (a *app) ruleConditionEntries(c *ui.Context, p palette, d ruleCondition) {
	st := &a.rulePage
	entries := ruleEntries(d.key, st.values[d.key])
	if len(entries) == 0 {
		return
	}
	st.entryList.Key = func(i int) any { return entries[i] }
	containListScroll(ui.List(c, &st.entryList, len(entries), func(i int) {
		value := entries[i]
		ui.Row(c.Key(value)).Padding(5, 6).Gap(5).AlignItems(ui.Center).Children(func() {
			label(c, value, sizeCaption, 16, 400, p.text).Grow(1).Basis(0).MinWidth(0).SingleLine()
			if ui.Button(c.Key("remove-entry"), "Remove").Label("Remove " + value).Clicked() {
				var next []string
				for _, v := range entries {
					if v != value {
						next = append(next, v)
					}
				}
				st.values[d.key] = strings.Join(next, "\n")
			}
		})
	})).Label(d.title + " entries").Height(min(136, float32(len(entries))*40)).Shrink(0).Radius(6).Background(p.subdued)
}

// Use the same translucent card, recessed content and control surfaces as
// the main panel and subscription details, including over native material.
func ruleFormTheme(c *ui.Context, p palette, panel bool) ui.Theme {
	t := *c.Theme()
	t.FontSize = sizeSubhead
	t.Spacing = 3
	t.Radius = 6
	t.Text, t.TextMuted = p.text, p.muted
	t.Border = p.borderDefault
	t.Accent, t.AccentHover, t.AccentText = p.accent, p.accentHover, ui.Hex("#ffffff")
	t.InactiveAccent = p.accent
	t.Focus = p.borderFocus
	t.Background = ui.Transparent
	if panel {
		t.Background = p.subdued
	}
	t.Surface = p.cardHover
	t.SurfaceHover = p.cardActive
	t.SurfacePressed = p.cardActive

	return t
}

func ruleFieldColor(p palette) ui.Color { return p.subdued }

// Use MyGo's radio-group behavior, with equal-width segment triggers.
func ruleSegments(c *ui.Context, key string, selected *int, p palette, options ...string) bool {
	empty := *selected < 0
	parts := ui.SegmentedBase(c.Key(key), selected, len(options))
	if empty {
		*selected = -1
	}
	t := c.Theme()
	groupName := key
	switch key {
	case "app-scope":
		groupName = "Application source"
	case "domain-mode":
		groupName = "Domain matching mode"
	case "geo-scope":
		groupName = "Website list source"
	case "network":
		groupName = "Network type"
	case "destination-kind":
		groupName = "Destination"
	}
	parts.Track.Label(groupName).Padding(t.Space(0.5)).Gap(t.Space(0.5)).Radius(t.Radius).Background(ruleFieldColor(p)).Border(1, t.Border).Shrink(0).Children(func() {
		for i, title := range options {
			segment := ruleSegmentStyle(parts.Segment(i).Label(title), i == *selected, t)
			segment.Children(func() { ui.Text(c, title).SingleLine().TextAlign(ui.Center) })
		}
	})
	return parts.Track.Changed()
}

// Both native radio segments and multi-select toggles share the same surfaces.
func ruleSegmentStyle(segment ui.Element, selected bool, t *ui.Theme) ui.Element {
	segment = segment.Grow(1).Basis(0).MinWidth(0).Padding(t.Space(1), 0).Radius(max(t.Radius-t.Space(0.5), 0)).Cursor(ui.CursorPointer)
	if selected {
		segment.Background(t.SurfacePressed).Shadow(0, 1, 2, 0, ui.RGBA(0, 0, 0, 0.12))
	} else if segment.Hovered() {
		segment.Background(t.SurfaceHover)
	}
	return segment
}
