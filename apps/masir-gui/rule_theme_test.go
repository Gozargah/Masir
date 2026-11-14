package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestRuleFormPreservesTooltipColors(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, panel := range []bool{false, true} {
			tt := ui.NewTester(func(c *ui.Context) {
				base := *c.Theme()
				wantFill, wantText := base.Inverse, base.InverseText
				if wantFill == ui.Transparent {
					wantFill = base.Text
				}
				if wantText == ui.Transparent {
					wantText = base.Background
				}
				form := ruleFormTheme(c, paletteFor(c), panel)
				if form.Inverse != wantFill || form.InverseText != wantText || form.InverseText.A == 0 {
					t.Errorf("dark=%v panel=%v: tooltip colors changed: fill=%+v text=%+v; want %+v %+v", dark, panel, form.Inverse, form.InverseText, wantFill, wantText)
				}
			}, 300, 100)
			tt.SetDark(dark)
			tt.Frame()
		}
	}
}
