package ui

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/quyenhl16/script/internal/domain"
	"github.com/quyenhl16/script/internal/registry"
	"github.com/quyenhl16/script/internal/runner"
)

type UI struct {
	input  *bufio.Reader
	output io.Writer
}

func New(input io.Reader, output io.Writer) *UI {
	return &UI{input: bufio.NewReader(input), output: output}
}

func (u *UI) Run(ctx context.Context, registry *registry.Registry, selectedProfile domain.Profile, options runner.Options) error {
	features := registry.List()
	selected := make(map[string]bool)
	profileParameters := make(map[string]map[string]any)
	for _, item := range selectedProfile.Features {
		selected[item.ID] = true
		profileParameters[item.ID] = item.Parameters
	}

	for {
		u.draw(features, selected)
		fmt.Fprint(u.output, "Command [number=toggle, a=all, n=none, r=run, q=quit]: ")
		line, err := u.input.ReadString('\n')
		if err != nil && len(line) == 0 {
			return err
		}
		command := strings.TrimSpace(strings.ToLower(line))
		switch command {
		case "q":
			return nil
		case "a":
			for _, feature := range features {
				selected[feature.ID] = true
			}
		case "n":
			clear(selected)
		case "r":
			ids := selectedIDs(features, selected)
			if len(ids) == 0 {
				fmt.Fprintln(u.output, "Select at least one feature.")
				continue
			}
			profile := domain.Profile{APIVersion: "syssetup/v1", Name: "interactive"}
			for _, id := range ids {
				profile.Features = append(profile.Features, domain.FeatureSelection{
					ID:         id,
					Parameters: profileParameters[id],
				})
			}
			resolved, err := registry.Resolve(profile)
			if err != nil {
				return err
			}
			executor, err := runner.New(options)
			if err != nil {
				return err
			}
			_, runErr := executor.Execute(ctx, resolved)
			closeErr := executor.Close()
			if runErr != nil {
				return runErr
			}
			return closeErr
		default:
			index, err := strconv.Atoi(command)
			if err == nil && index > 0 && index <= len(features) {
				id := features[index-1].ID
				selected[id] = !selected[id]
			}
		}
	}
}

func (u *UI) draw(features []domain.Feature, selected map[string]bool) {
	fmt.Fprint(u.output, "\033[2J\033[H")
	fmt.Fprintln(u.output, "SYSSETUP · RHEL System Setup")
	fmt.Fprintln(u.output, strings.Repeat("─", 58))
	for index, feature := range features {
		mark := " "
		if selected[feature.ID] {
			mark = "x"
		}
		fmt.Fprintf(u.output, "%2d. [%s] %-20s %s\n", index+1, mark, feature.ID, feature.Description)
	}
	fmt.Fprintln(u.output, strings.Repeat("─", 58))
}

func selectedIDs(features []domain.Feature, selected map[string]bool) []string {
	var ids []string
	for _, feature := range features {
		if selected[feature.ID] {
			ids = append(ids, feature.ID)
		}
	}
	sort.Strings(ids)
	return ids
}
