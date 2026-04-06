package ui

import (
	"fmt"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// passwordPromptModel holds state for the password prompt view.
type passwordPromptModel struct {
	input      textinput.Model
	account    string // account name requesting password
	promptType promptType
	errMsg     string // error message to display (e.g., "authentication failed")
}

// promptType indicates why we're asking for a password.
type promptType int

const (
	promptNewAccount promptType = iota // new account setup
	promptAuthFailed                   // auth failed, need to re-enter
	promptManualSet                    // user ran :set-password command
)

// newPasswordPromptModel creates a new password prompt with masked input.
func newPasswordPromptModel() passwordPromptModel {
	ti := textinput.New()
	ti.Placeholder = "Enter password"
	ti.EchoMode = textinput.EchoPassword
	ti.EchoCharacter = '•'
	ti.Focus()
	ti.CharLimit = 256
	ti.Width = 50
	ti.Prompt = ""

	return passwordPromptModel{input: ti}
}

// reset clears the input and error message.
func (p *passwordPromptModel) reset() {
	p.input.Reset()
	p.errMsg = ""
	p.input.Focus()
}

// setPrompt configures the prompt for a specific account and type.
func (p *passwordPromptModel) setPrompt(account string, pt promptType, errMsg string) {
	p.account = account
	p.promptType = pt
	p.errMsg = errMsg
	p.reset()
}

// passwordSubmittedMsg is sent when the user submits a password.
type passwordSubmittedMsg struct {
	account  string
	password string
}

// passwordCancelledMsg is sent when the user cancels the prompt.
type passwordCancelledMsg struct{}

// Update handles input for the password prompt.
func (p passwordPromptModel) Update(msg tea.Msg) (passwordPromptModel, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyEnter:
			if p.input.Value() != "" {
				return p, func() tea.Msg {
					return passwordSubmittedMsg{
						account:  p.account,
						password: p.input.Value(),
					}
				}
			}
		case tea.KeyEsc, tea.KeyCtrlC:
			return p, func() tea.Msg {
				return passwordCancelledMsg{}
			}
		}
	}

	p.input, cmd = p.input.Update(msg)
	return p, cmd
}

// View renders the password prompt.
func (p passwordPromptModel) View(width, height int) string {
	// Center the dialog
	dialogWidth := 60

	var title string
	switch p.promptType {
	case promptNewAccount:
		title = fmt.Sprintf("🔐 Set password for %s", p.account)
	case promptAuthFailed:
		title = fmt.Sprintf("🔐 Authentication failed for %s", p.account)
	case promptManualSet:
		title = fmt.Sprintf("🔐 Update password for %s", p.account)
	}

	var hint string
	switch p.promptType {
	case promptNewAccount:
		hint = "This account uses the OS keyring for secure password storage.\nEnter your password to continue."
	case promptAuthFailed:
		hint = "Your password may be incorrect or have expired.\nEnter the correct password to continue."
	case promptManualSet:
		hint = "Enter the new password to store in the OS keyring."
	}

	// Build content
	titleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#7aa2f7")).Bold(true)
	content := titleStyle.Render(title) + "\n\n"
	content += lipgloss.NewStyle().Foreground(lipgloss.Color("#7aa2f7")).Render(hint) + "\n\n"

	if p.errMsg != "" {
		content += styleError.Render("Error: "+p.errMsg) + "\n\n"
	}

	// Input field with border
	inputStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#565f89")).
		Padding(0, 1).
		Width(dialogWidth - 4)

	content += inputStyle.Render(p.input.View()) + "\n\n"

	// Help text
	help := "[Enter] Confirm  [Esc] Cancel"
	content += lipgloss.NewStyle().Foreground(lipgloss.Color("#565f89")).Render(help)

	// Dialog box
	dialogStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#7aa2f7")).
		Padding(1, 2).
		Width(dialogWidth)

	dialog := dialogStyle.Render(content)

	// Center on screen
	return lipgloss.Place(width, height,
		lipgloss.Center, lipgloss.Center,
		dialog,
	)
}
