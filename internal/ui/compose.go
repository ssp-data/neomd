package ui

import (
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// composeStep tracks which field is active in the compose form.
type composeStep int

const (
	stepTo      composeStep = iota
	stepSubject             // after Subject is filled, launch editor
)

// composeModel holds state for the compose view.
type composeModel struct {
	to      textinput.Model
	subject textinput.Model
	step    composeStep
}

func newComposeModel() composeModel {
	to := textinput.New()
	to.Placeholder = "recipient@example.com"
	to.Focus()
	to.CharLimit = 256
	to.Width = 60
	to.Prompt = ""

	sub := textinput.New()
	sub.Placeholder = "Subject"
	sub.CharLimit = 256
	sub.Width = 60
	sub.Prompt = ""

	return composeModel{to: to, subject: sub, step: stepTo}
}

// reset clears both fields and refocuses on To.
func (c *composeModel) reset() {
	c.to.Reset()
	c.subject.Reset()
	c.step = stepTo
	c.to.Focus()
	c.subject.Blur()
}

// update handles key input for the compose form.
// Returns (updated model, cmd, launchEditor bool).
func (c composeModel) update(msg tea.Msg) (composeModel, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "tab", "enter":
			if c.step == stepTo {
				c.step = stepSubject
				c.to.Blur()
				c.subject.Focus()
				return c, nil, false
			}
			if c.step == stepSubject {
				// Signal to model to launch editor
				return c, nil, true
			}
		}
	}

	var cmd tea.Cmd
	if c.step == stepTo {
		c.to, cmd = c.to.Update(msg)
	} else {
		c.subject, cmd = c.subject.Update(msg)
	}
	return c, cmd, false
}

// view renders the compose header form.
func (c composeModel) view() string {
	toLabel := styleInputLabel.Render("To:")
	subLabel := styleInputLabel.Render("Subject:")

	toField := c.to.View()
	subField := c.subject.View()

	if c.step == stepTo {
		toField = styleInputField.Render(toField)
	} else {
		subField = styleInputField.Render(subField)
	}

	return toLabel + toField + "\n" + subLabel + subField
}
