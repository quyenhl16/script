package ui

import (
	"context"
	"io"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/quyenhl16/script/internal/domain"
	"github.com/quyenhl16/script/internal/registry"
	"github.com/quyenhl16/script/internal/runner"
)

type UI struct {
	input  io.Reader
	output io.Writer
}

func New(input io.Reader, output io.Writer) *UI {
	return &UI{input: input, output: output}
}

func (u *UI) Run(ctx context.Context, registry *registry.Registry, profile domain.Profile, options runner.Options) error {
	dashboard := newModel(ctx, registry, profile, options)
	program := tea.NewProgram(
		dashboard,
		tea.WithContext(ctx),
		tea.WithInput(u.input),
		tea.WithOutput(u.output),
		tea.WithAltScreen(),
	)
	final, err := program.Run()
	if err != nil {
		return err
	}
	if state, ok := final.(*model); ok {
		return state.runErr
	}
	return nil
}
