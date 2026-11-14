package main

import (
	"testing"
	"time"
)

func TestEditorDeleteConfirmationPreservesDraftAndHandlesFailure(t *testing.T) {
	f := ukServers()
	a := newTestApp(f)
	a.openRule(a.ruleByTag("r2"))
	a.editRule(a.ruleByTag("r2"))
	a.rulePage.name = "Unsaved name"
	tt := tester(a)
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
		tt.Advance(time.Second)
	}
	click("Delete rule")
	if f.called("remove r2") || !tt.HasText("Remove") {
		t.Fatal("delete did not ask about the original rule")
	}
	// Leaving the editor cancels confirmation; reopening preserves the saved rule.
	a.goBack()
	if a.rulePage.removing || f.called("remove r2") {
		t.Fatal("leaving the editor did not cancel removal")
	}
	a.editRule(a.ruleByTag("r2"))
	a.rulePage.name = "Unsaved name"
	tt.Advance(time.Second)
	f.failRemove = errFake
	click("Delete rule")
	click("Remove")
	if a.rulePage.err == "" || a.rulePage.busy || a.rulePage.name != "Unsaved name" || a.path != ruleEditPath || a.ruleByTag("r2") == nil {
		t.Fatal("failed deletion lost the editor or rule")
	}
	f.failRemove = nil
	click("Delete rule")
	click("Remove")
	if a.ruleByTag("r2") != nil || a.path != mainPath || f.count("remove r2") != 2 {
		t.Fatal("confirmed deletion did not remove the original rule and leave its pages")
	}
}

func TestNewRuleHasNoDeleteIcon(t *testing.T) {
	a := newTestApp(ukServers())
	a.newRule()
	tt := tester(a)
	tt.Frame()
	if _, ok := tt.Find("Delete rule"); ok {
		t.Fatal("new rule has a delete action")
	}
}

func TestContextMenuDeleteCanBeCanceled(t *testing.T) {
	a, f, tt := openRules(t)
	if err := tt.RightClick(ltr("bank.test")); err != nil {
		t.Fatal(err)
	}
	tt.Advance(time.Second)
	if err := tt.Click("Remove Rule"); err != nil {
		t.Fatal(err)
	}
	tt.ClickAt(5, 5)
	tt.Advance(time.Second)
	if err := tt.RightClick(ltr("bank.test")); err != nil {
		t.Fatal(err)
	}
	tt.Advance(time.Second)
	if f.called("remove r2") || a.menu.confirmRemove || !tt.HasText(ltr("bank.test")) {
		t.Fatal("cancel removed the rule or left the confirmation open")
	}
}

func TestSwipeRemoveConfirmationResetsOnClose(t *testing.T) {
	a, f, tt := openRules(t)
	sw := a.swipeFor("r1")
	sw.x, sw.open = -swipeWidth, true
	tt.Frame()
	if err := tt.Click("Remove Rule"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !sw.confirmRemove || f.called("remove r1") {
		t.Fatal("first click removed the rule")
	}
	b, ok := tt.Find("Remove")
	if !ok || abs32(b.W-ruleRemoveWidth()) > 0.5 {
		t.Fatalf("confirmation does not span the action buttons: %+v", b)
	}
	if _, ok := tt.Find("Edit Rule"); ok {
		t.Fatal("edit action still shown while confirming")
	}
	sw.close(time.Now())
	tt.Advance(time.Second)
	sw.x, sw.open = -swipeWidth, true
	tt.Frame()
	if sw.confirmRemove {
		t.Fatal("closing did not reset confirmation")
	}
	if err := tt.Click("Remove Rule"); err != nil {
		t.Fatal(err)
	}
	if f.called("remove r1") {
		t.Fatal("reopening skipped confirmation")
	}
	if err := tt.Click("Remove"); err != nil {
		t.Fatal(err)
	}
	if !f.called("remove r1") {
		t.Fatal("second click did not remove rule")
	}
}

func TestEditorRemoveConfirmationDoesNotMoveForm(t *testing.T) {
	a := newTestApp(ukServers())
	a.editRule(a.ruleByTag("r1"))
	tt := tester(a)
	tt.Frame()
	tt.Frame()
	before, ok := tt.Find("MATCH WHEN")
	if !ok {
		t.Fatal("missing form heading")
	}
	trash, trashOK := tt.Find("Delete rule")
	cancel, cancelOK := tt.Find("Cancel rule editing")
	save, saveOK := tt.Find("Save rule changes")
	if !trashOK || !cancelOK || !saveOK || abs32(trash.W-trash.H) > 0.01 || trash.X+trash.W >= cancel.X || trash.Y != cancel.Y {
		t.Fatalf("trash must be square and beside Cancel: trash=%+v cancel=%+v", trash, cancel)
	}
	beforeHeight := a.rulePage.editHeight
	if err := tt.Click("Delete rule"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	after, ok := tt.Find("MATCH WHEN")
	if !ok {
		t.Fatal("missing form heading after confirmation")
	}
	remove, removeOK := tt.Find("Confirm remove rule")
	saveAfter, _ := tt.Find("Save rule changes")
	if !removeOK || abs32(remove.W-save.W) > 0.01 || remove.H != cancel.H || saveAfter != save {
		t.Fatalf("confirmation must fill the left half without moving Save: remove=%+v save=%+v after=%+v", remove, save, saveAfter)
	}
	if abs32(before.Y-after.Y) > 0.01 || abs32(beforeHeight-a.rulePage.editHeight) > 0.01 {
		t.Fatalf("confirmation moved form from y=%g to %g; page height %g to %g", before.Y, after.Y, beforeHeight, a.rulePage.editHeight)
	}
}
