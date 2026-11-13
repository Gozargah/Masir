package main

import (
	"fmt"
	"slices"
	"strings"

	pb "github.com/Gozargah/Masir/libs/masir-proto/gen/masir/daemon/v1"
	"github.com/egoist/mygo/ui"
)

type ruleGeoOption struct {
	code, title, titleKey, flag, search, count string
}

type ruleGeoState struct {
	all, filtered                    []ruleGeoOption
	known                            map[string]bool
	selected                         map[string]bool
	query, lastQuery, lastMatch, err string
	tab, lastTab                     int
	loading, loaded, dirty           bool
	list                             ui.ListState
}

func geoWebsiteTitle(code string) (string, string) {
	title, flag, _, ok := humanizeGeosite("geosite:" + code)
	if !ok {
		title = formatTopic(strings.TrimPrefix(code, "category-"))
	}
	return title, flag
}

func geoWebsiteOption(code string, count uint32) ruleGeoOption {
	title, flag := geoWebsiteTitle(code)
	v := ruleGeoOption{code: code, title: title, flag: flag}
	v.titleKey = strings.ToLower(title)
	v.search = strings.ToLower(title + " " + code + " geosite:" + code)
	if count > 0 {
		if count > 1 {
			v.count = fmt.Sprintf("%d domains", count)
		} else {
			v.count = "1 domain"
		}
	}
	return v
}

// Cache metadata across editors, but revalidate on opening: the daemon already
// indexes installed files by modification time and size. No geo data enters a frame.
func (a *app) prepareRuleGeo() {
	st := &a.ruleGeo
	st.query, st.tab, st.lastTab, st.dirty = "", 0, -1, true
	a.loadRuleGeo()
}

func (a *app) loadRuleGeo() {
	st := &a.ruleGeo
	if st.loading {
		return
	}
	st.loading, st.err = true, ""
	d := a.d
	a.async(func() {
		ctx, cancel := rpc()
		defer cancel()
		res, err := d.ListGeoCategories(ctx, &pb.ListGeoCategoriesRequest{Kind: pb.GeoKind_GEO_KIND_SITE})
		var all []ruleGeoOption
		known := map[string]bool{}
		if err == nil && res != nil {
			for _, cat := range res.Categories {
				code := strings.ToLower(strings.TrimSpace(cat.Code))
				if code == "" || known[code] {
					continue
				}
				known[code] = true
				all = append(all, geoWebsiteOption(code, cat.Entries))
			}
			slices.SortFunc(all, func(a, b ruleGeoOption) int {
				if title := strings.Compare(a.titleKey, b.titleKey); title != 0 {
					return title
				}
				return strings.Compare(a.code, b.code)
			})
		}
		a.post(func() {
			st.loading = false
			if err != nil {
				st.err = "Couldn't load website lists. Retry, or enter a category in Manual."
				return
			}
			st.all, st.known, st.loaded, st.dirty = all, known, true, true
		})
	})
}

// Only recompute on a query, selection or catalog change, never on a paint frame.
func (a *app) filterRuleGeo() {
	st := &a.ruleGeo
	match := a.rulePage.values["domain"]
	if !st.dirty && st.query == st.lastQuery && st.tab == st.lastTab && st.lastMatch == match {
		return
	}
	st.selected = map[string]bool{}
	var selectedCodes []string
	for _, entry := range ruleEntries("domain", match) {
		if code, ok := strings.CutPrefix(entry, "geosite:"); ok {
			st.selected[code] = true
			selectedCodes = append(selectedCodes, code)
		}
	}
	q := strings.ToLower(strings.TrimSpace(st.query))
	q = strings.TrimPrefix(q, "geosite:")
	st.filtered = st.filtered[:0]
	add := func(v ruleGeoOption) {
		if (st.tab != 1 || st.selected[v.code]) && strings.Contains(v.search, q) {
			st.filtered = append(st.filtered, v)
		}
	}
	for _, v := range st.all {
		add(v)
	}
	// Old or manually entered categories remain removable even after a geo update.
	if st.tab == 1 {
		for _, code := range selectedCodes {
			if !st.known[code] {
				add(geoWebsiteOption(code, 0))
			}
		}
	}
	rank := func(v ruleGeoOption) int {
		title := v.titleKey
		if v.code == q || title == q {
			return 0
		}
		if strings.HasPrefix(v.code, q) || strings.HasPrefix(title, q) {
			return 1
		}
		return 2
	}
	slices.SortStableFunc(st.filtered, func(a, b ruleGeoOption) int { return rank(a) - rank(b) })
	if st.query != st.lastQuery || st.tab != st.lastTab {
		st.list.ScrollTo(0, ui.Start)
	}
	st.lastQuery, st.lastTab, st.lastMatch, st.dirty = st.query, st.tab, match, false
	st.list.Key = func(i int) any { return st.filtered[i].code }
	st.list.Label = func(i int) string { return st.filtered[i].title }
}

func (a *app) ruleGeoEditor(c *ui.Context, p palette, d ruleCondition) {
	st := &a.ruleGeo
	ui.Column(c).Padding(0, 10).Children(func() { ruleSegments(c, "geo-scope", &st.tab, p, "All lists", "Selected", "Manual") })
	a.filterRuleGeo()
	if st.tab == 2 {
		ui.Column(c).Padding(0, 10, 10).Gap(8).Children(func() {
			a.ruleEntryEditor(c, p, d, "Website category", "geosite:category-ads-all")
		})
		return
	}
	panelBar(c, p, func() { searchInput(c, "geo-search", &st.query, "Search category or website…", p) }, func() {
		if iconButton(c, "retry-geosite", iconRefresh, "Refresh website lists", p, iconMD, variantSubtle, 12) && !st.loading {
			a.loadRuleGeo()
		}
	})
	a.filterRuleGeo()
	ui.Column(c).Label("Website list results").Height(208).Shrink(0).Children(func() {
		if len(st.filtered) > 0 {
			containListScroll(ui.List(c, &st.list, len(st.filtered), func(i int) { a.ruleGeoRow(c, p, st.filtered[i]) })).Label("Website list choices").Grow(1).Basis(0).MinHeight(0).Padding(4, 6).Gap(3).ScrollbarInsets(0, 1, 0)
			return
		}
		ui.Column(c).Grow(1).Padding(12).Gap(6).Justify(ui.Center).AlignItems(ui.Center).Children(func() {
			message := "No website lists installed. You can enter a category in Manual."
			switch {
			case st.loading && !st.loaded:
				ui.Spinner(c).Size(14, 14)
				message = "Loading website lists…"
			case st.err != "":
				message = st.err
			case st.query != "":
				message = "No matching website lists."
			case st.tab == 1:
				message = "Choose website lists in All lists."
			}
			label(c, message, sizeCaption, 16, 400, p.muted).TextAlign(ui.Center)
		})
	})
	ui.Row(c).Padding(6, 10).Gap(6).Justify(ui.SpaceBetween).Children(func() {
		label(c, fmt.Sprintf("%d selected", len(st.selected)), sizeMicro, 15, 400, p.muted)
		label(c, fmt.Sprintf("%d lists", len(st.all)), sizeMicro, 15, 400, p.tertiary)
	})
	if st.err != "" && len(st.filtered) > 0 {
		label(c, st.err, sizeCaption, 16, 400, p.errorC).Padding(0, 10, 8)
	}
}

func (a *app) ruleGeoRow(c *ui.Context, p palette, v ruleGeoOption) {
	selected := a.ruleGeo.selected[v.code]
	row := ui.ToggleBase(c.Key(v.code), &selected).Label("Select website list "+v.code).Disabled(a.rulePage.busy).Padding(6).Gap(8).Radius(6).AlignItems(ui.Center).Border(1, ui.Transparent).Cursor(ui.CursorPointer)
	if selected {
		row.Background(p.accentSurface).Border(1, p.borderFocus)
	} else if row.Hovered() {
		row.Background(p.cardHover)
	}
	row.Children(func() {
		iconTile(c, p, func() {
			if v.flag != "" {
				flagElement(c, v.flag, 18)
			} else {
				ui.Icon(c, iconGlobe).FontSize(14).TextColor(p.muted)
			}
		})
		ui.Column(c).Grow(1).Basis(0).MinWidth(0).Children(func() {
			ui.Row(c).Justify(ui.SpaceBetween).Children(func() {
				label(c, v.title, sizeBody, 16, 500, p.text).SingleLine()
				label(c, v.count, sizeMicro, 14, 400, p.muted).SingleLine()
			})
			code := "geosite:" + v.code
			if a.ruleGeo.loaded && !a.ruleGeo.known[v.code] {
				code += " · Not installed"
			}
			label(c, code, sizeMicro, 14, 400, p.muted).SingleLine()
		})
		if selected {
			ui.Icon(c, iconCheck).FontSize(12).TextColor(p.accent).Shrink(0)
		}
	})
	if row.Changed() {
		entries := ruleEntries("domain", a.rulePage.values["domain"])
		value := "geosite:" + v.code
		if selected {
			entries = append(entries, value)
		} else {
			entries = slices.DeleteFunc(entries, func(s string) bool { return s == value })
		}
		a.rulePage.values["domain"] = strings.Join(entries, "\n")
	}
}
