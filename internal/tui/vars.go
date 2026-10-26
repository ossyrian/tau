package tui

import (
	"regexp"
	"strings"

	bkey "charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/ossyrian/tau/internal/envfile"
	"github.com/ossyrian/tau/internal/theme"
)

var varNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type varStep int

const (
	varBrowse varStep = iota
	varName
	varValue
)

// varEditor lists KEY=value pairs in a table and edits them. The env section
// and each proxy's variables use it; they differ in what the owner does with a
// change. Values are masked until the user reveals them.
type varEditor struct {
	profile string
	pick    *picker
	vars    []envfile.Var
	reveal  bool

	step    varStep
	key     string // the name being set
	editing bool   // true when the name was chosen from the table
	name    textinput.Model
	value   textinput.Model
	msg     string // validation message while naming

	// set receives a finished edit, remove a confirmed removal. Each returns
	// the command that does it.
	set    func(key, value string) tea.Cmd
	remove func(key string) tea.Cmd
}

func newVarEditor() *varEditor {
	return &varEditor{
		pick:  newPicker("PROFILE", "KEY", "VALUE"),
		name:  newInput("VARIABLE_NAME", 40),
		value: newSecretInput("value", 50),
	}
}

func (e *varEditor) Capturing() bool { return e.step != varBrowse || e.pick.Capturing() }

func (e *varEditor) Keys() []bkey.Binding {
	if e.step != varBrowse {
		return []bkey.Binding{binding("enter", "enter", "next"), binding("esc", "esc", "cancel")}
	}
	keys := e.pick.Keys()
	if e.pick.Capturing() {
		return keys
	}
	return append(keys, binding("n", "n", "new"), binding("e", "e", "edit"), binding("d", "d", "delete"), binding("v", "v", "reveal"))
}

// Hide masks the values again. Owners call it when the screen opens or closes.
func (e *varEditor) Hide() { e.reveal = false; e.refill() }

// Set replaces the variables, keeping the cursor on the same key.
func (e *varEditor) Set(profile string, vars []envfile.Var) {
	e.profile, e.vars = profile, vars
	e.refill()
}

func (e *varEditor) refill() {
	rows := make([]pickerRow, len(e.vars))
	for i, v := range e.vars {
		val := envfile.Mask(v.Value)
		if e.reveal {
			val = v.Value
		}
		// Search covers every column, and matches the real value even while
		// it is masked on screen.
		rows[i] = pickerRow{Key: v.Key, Cells: []string{e.profile, v.Key, val}, Search: e.profile + " " + v.Key + " " + v.Value}
	}
	e.pick.SetRows(rows)
}

func (e *varEditor) selected() (envfile.Var, bool) {
	r, ok := e.pick.Selected()
	if !ok {
		return envfile.Var{}, false
	}
	for _, v := range e.vars {
		if v.Key == r.Key {
			return v, true
		}
	}
	return envfile.Var{}, false
}

// Update handles a key.
func (e *varEditor) Update(msg tea.KeyPressMsg) tea.Cmd {
	switch e.step {
	case varName:
		return e.typingName(msg)
	case varValue:
		return e.typingValue(msg)
	}
	if e.pick.Capturing() {
		return e.pick.Update(msg)
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
		return e.value.Focus() // the field starts empty: nothing prefills a secret
	case "d":
		if v, ok := e.selected(); ok {
			return e.remove(v.Key)
		}
		return nil
	case "v":
		e.reveal = !e.reveal
		e.refill()
		return nil
	}
	return e.pick.Update(msg)
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

// prompt is the input pane shown above the table while a variable is edited.
func (e *varEditor) prompt() (title, body string) {
	switch e.step {
	case varName:
		body = theme.Dim.Render("name") + "\n" + e.name.View()
		if e.msg != "" {
			body += "\n" + theme.Err.Render(e.msg)
		}
		return "new variable", body
	case varValue:
		title = "new variable " + e.key
		if e.editing {
			title = "edit " + e.key
		}
		return title, theme.Dim.Render("value (hidden)") + "\n" + e.value.View()
	}
	return "", ""
}
