package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/ossyrian/tau/internal/envfile"
)

func keys(e *varEditor, keys ...string) {
	for _, k := range keys {
		e.Update(key(k))
	}
}

func typed(e *varEditor, s string) {
	for _, r := range s {
		e.Update(key(string(r)))
	}
}

func newTestEditor() (*varEditor, *[]string) {
	var calls []string
	e := newVarEditor()
	e.set = func(k, v string) tea.Cmd { calls = append(calls, "set:"+k+"="+v); return nil }
	e.remove = func(k string) tea.Cmd { calls = append(calls, "remove:"+k); return nil }
	e.Set([]envfile.Var{{Key: "TOKEN", Value: "supersecretvalue"}})
	return e, &calls
}

func TestValuesAreMaskedUntilRevealedAndMaskedAgainByHide(t *testing.T) {
	e, _ := newTestEditor()
	show := func() string { return strings.Join(e.View(60, 10), "\n") }
	if strings.Contains(show(), "supersecret") {
		t.Fatal("a value is visible by default")
	}
	keys(e, "v")
	if !strings.Contains(show(), "supersecretvalue") {
		t.Fatal("v did not reveal")
	}
	e.Hide()
	if strings.Contains(show(), "supersecret") {
		t.Error("Hide left the value visible")
	}
}

func TestNewAsksForANameThenAHiddenValueThenSets(t *testing.T) {
	e, calls := newTestEditor()
	keys(e, "n")
	typed(e, "A_B")
	keys(e, "enter")
	typed(e, "shh")
	if strings.Contains(strings.Join(e.View(60, 10), "\n"), "shh") {
		t.Error("the value is echoed while typed")
	}
	keys(e, "enter")
	if len(*calls) != 1 || (*calls)[0] != "set:A_B=shh" {
		t.Errorf("calls %v", *calls)
	}
}

func TestAnInvalidNameIsRefusedBeforeAskingForAValue(t *testing.T) {
	e, _ := newTestEditor()
	keys(e, "n")
	typed(e, "1bad")
	keys(e, "enter")
	if e.step != varName || e.msg == "" {
		t.Errorf("step %v, msg %q", e.step, e.msg)
	}
}

func TestEditNeverPrefillsTheOldValueAndKeepsTheName(t *testing.T) {
	e, calls := newTestEditor()
	keys(e, "e")
	if e.value.Value() != "" {
		t.Errorf("prefilled %q", e.value.Value())
	}
	typed(e, "new")
	keys(e, "enter")
	if len(*calls) != 1 || (*calls)[0] != "set:TOKEN=new" {
		t.Errorf("calls %v", *calls)
	}
}

func TestEscCancelsAtEitherStepAndDeleteAsksTheOwner(t *testing.T) {
	e, calls := newTestEditor()
	keys(e, "n", "esc")
	keys(e, "n")
	typed(e, "X")
	keys(e, "enter", "esc")
	if e.step != varBrowse || len(*calls) != 0 {
		t.Errorf("step %v, calls %v", e.step, *calls)
	}
	keys(e, "d")
	if len(*calls) != 1 || (*calls)[0] != "remove:TOKEN" {
		t.Errorf("calls %v", *calls)
	}
}
