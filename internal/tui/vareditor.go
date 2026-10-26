package tui

import (
	"regexp"
	"strings"

	bkey "charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/ossyrian/tau/internal/envfile"
)

var varNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// varRow is one KEY=value row. The value is masked until reveal is set.
type varRow struct {
	v      envfile.Var
	reveal bool
}

func (r varRow) FilterValue() string { return r.v.Key }
func (r varRow) Title() string       { return r.v.Key }
func (r varRow) Description() string {
	if r.reveal {
		return r.v.Value
	}
	return envfile.Mask(r.v.Value)
}

type varStep int

const (
	varBrowse varStep = iota
	varName
	varValue
)

// varEditor lists variables and edits them: reveal, new, edit, delete. The
// env section and each proxy's page both use it; they differ in what the
// owner does with a change.
type varEditor struct {
	list    list.Model
	vars    []envfile.Var
	reveal  bool
	step    varStep
	key     string // the name being set
	editing bool   // true when the name was chosen from the list
	name    textinput.Model
	value   textinput.Model
	msg     string // validation message while naming

	// Set receives a finished edit, Delete a confirmed removal. Each returns
	// the command that does it. Where names what the variables belong to.
	set    func(key, value string) tea.Cmd
	remove func(key string) tea.Cmd
	where  string
}

func newVarEditor() *varEditor {
	return &varEditor{
		list:  newList(nil, 30, 10),
		name:  newInput("VARIABLE_NAME", 40),
		value: newSecretInput("value", 50),
	}
}

func (e *varEditor) Capturing() bool { return e.step != varBrowse }

func (e *varEditor) Keys() []bkey.Binding {
	if e.step != varBrowse {
		return []bkey.Binding{binding("enter", "enter", "next"), binding("esc", "esc", "cancel")}
	}
	return []bkey.Binding{
		binding("n", "n", "new"), binding("e", "e", "edit"), binding("d", "d", "delete"),
		binding("v", "v", "reveal"),
	}
}

// Hide masks the values again. Owners call it when the screen opens or closes.
func (e *varEditor) Hide() {
	e.reveal = false
	e.rebuild()
}

// Set replaces the variables, keeping the selection on the same key.
func (e *varEditor) Set(vars []envfile.Var) {
	keep := ""
	if v, ok := e.selected(); ok {
		keep = v.Key
	}
	e.vars = vars
	e.rebuild()
	for i, v := range vars {
		if v.Key == keep {
			e.list.Select(i)
		}
	}
}

func (e *varEditor) rebuild() {
	items := make([]list.Item, len(e.vars))
	for i, v := range e.vars {
		items[i] = varRow{v, e.reveal}
	}
	e.list.SetItems(items)
}

func (e *varEditor) selected() (envfile.Var, bool) {
	r, ok := e.list.SelectedItem().(varRow)
	return r.v, ok
}

// Update handles a key. It reports whether it used the key.
func (e *varEditor) Update(msg tea.KeyPressMsg) tea.Cmd {
	switch e.step {
	case varName:
		return e.typingName(msg)
	case varValue:
		return e.typingValue(msg)
	}
	switch msg.String() {
	case "n":
		e.step, e.editing, e.msg = varName, false, ""
		e.name.SetValue("")
		return e.name.Focus()
	case "e":
		v, ok := e.selected()
		if !ok {
			return nil
		}
		e.step, e.editing, e.key = varValue, true, v.Key
		return e.value.Focus() // the field is empty: nothing prefills a secret

	case "d":
		v, ok := e.selected()
		if !ok {
			return nil
		}
		return e.remove(v.Key)
	case "v":
		e.reveal = !e.reveal
		e.rebuild()
		return nil
	}
	var cmd tea.Cmd
	e.list, cmd = e.list.Update(msg)
	return cmd
}

func (e *varEditor) cancel() {
	e.step, e.msg = varBrowse, ""
	e.name.Blur()
	e.value.Blur()
}

func (e *varEditor) typingName(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		e.cancel()
		return nil
	case "enter":
		name := strings.TrimSpace(e.name.Value())
		if name == "" {
			e.cancel()
			return nil
		}
		if !varNamePattern.MatchString(name) {
			e.msg = "invalid name: use letters, digits and _, not starting with a digit"
			return nil
		}
		e.key, e.step, e.msg = name, varValue, ""
		e.name.Blur()
		e.value.SetValue("")
		return e.value.Focus()
	}
	e.msg = ""
	var cmd tea.Cmd
	e.name, cmd = e.name.Update(msg)
	return cmd
}

func (e *varEditor) typingValue(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		e.cancel()
		return nil
	case "enter":
		key, value := e.key, e.value.Value()
		e.cancel()
		e.value.SetValue("")
		return e.set(key, value)
	}
	var cmd tea.Cmd
	e.value, cmd = e.value.Update(msg)
	return cmd
}

// View draws the open input, if any, above the list.
func (e *varEditor) View(cols, rows int) []string {
	var out []string
	switch e.step {
	case varName:
		out = append(out, titleStyle.Render("New variable"), "", dimStyle.Render("name"), e.name.View())
		if e.msg != "" {
			out = append(out, errStyle.Render(e.msg))
		}
		out = append(out, "")
	case varValue:
		verb := "New variable"
		if e.editing {
			verb = "Edit"
		}
		out = append(out, titleStyle.Render(verb+" "+e.key), "", dimStyle.Render("value (hidden)"), e.value.View(), "")
	}
	if len(e.vars) == 0 {
		return append(out, "", dimStyle.Render("no variables — press n to add one"))
	}
	e.list.SetSize(cols, max(rows-len(out), 2))
	return append(out, strings.Split(e.list.View(), "\n")...)
}
