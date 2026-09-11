package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/natsuki0413/commiter-cli/internal/config"
	"github.com/natsuki0413/commiter-cli/internal/exitcode"
	"github.com/natsuki0413/commiter-cli/internal/gitstate"
	"github.com/natsuki0413/commiter-cli/internal/interaction"
	"github.com/natsuki0413/commiter-cli/internal/output"
	"github.com/natsuki0413/commiter-cli/internal/planning"
	"github.com/natsuki0413/commiter-cli/internal/verification"
)

var mainInput io.Reader = os.Stdin

func runCollection(opts options, root string, values config.Values, printer *output.Printer) int {
	reader := bufio.NewReader(mainInput)
	snapshot, err := gitstate.Collect(root, gitstate.Options{
		Pathspecs:                opts.pathspecs,
		Include:                  values.Include,
		Exclude:                  values.Exclude,
		AdditionalSensitiveGlobs: values.SensitivePatterns,
		ApproveSensitiveCandidates: func(candidates []gitstate.Candidate) (bool, error) {
			if printer.JSON() {
				return false, nil
			}
			lines := []string{"Sensitive candidates require approval before reading:"}
			for _, candidate := range candidates {
				lines = append(lines, fmt.Sprintf("%s (%s)", candidate.Path, candidate.Reason))
			}
			lines = append(lines, "Read all listed candidates? [y/N]")
			if err := printer.Lines(lines...); err != nil {
				return false, err
			}
			answer := strings.TrimSpace(readLineFrom(reader))
			return answer == "y" || answer == "Y" || strings.EqualFold(answer, "yes"), nil
		},
	})
	if err != nil {
		return fail(printer, classifyCollectionError(err))
	}

	if len(snapshot.Changes) == 0 {
		if printer.JSON() {
			if err := printer.Value(map[string]any{"snapshot": snapshot, "dry_run": opts.dryRun}); err != nil {
				return fail(printer, exitcode.New(exitcode.Internal, "cannot write output"))
			}
			return exitcode.Success
		}
		if err := printSnapshot(snapshot, printer); err != nil {
			return fail(printer, exitcode.New(exitcode.Internal, "cannot write output"))
		}
		return exitcode.Success
	}

	definition, err := verification.Resolve(root, values)
	if err != nil {
		return fail(printer, exitcode.New(exitcode.Usage, err.Error()))
	}
	pushTarget := interaction.ResolvePushTarget(root)
	plan, err := planFlow(context.Background(), root, snapshot, values, "")
	if err != nil {
		return fail(printer, err)
	}
	request := reviewRequest(snapshot, plan, definition, pushTarget)
	if printer.JSON() {
		result := map[string]any{"snapshot": snapshot, "plan": plan, "push_target": pushTarget, "dry_run": true, "verification": map[string]any{"definition": definition, "executed": false}}
		if err := printer.Value(result); err != nil {
			return fail(printer, exitcode.New(exitcode.Internal, "cannot write output"))
		}
		return exitcode.Success
	}
	if opts.dryRun || !values.CommitConfirm {
		if err := interaction.Render(printer, request); err != nil {
			return fail(printer, exitcode.New(exitcode.Internal, "cannot write output"))
		}
		if !opts.dryRun {
			if err := printer.Lines("Commit confirmation skipped by configuration."); err != nil {
				return fail(printer, exitcode.New(exitcode.Internal, "cannot write output"))
			}
		}
		return exitcode.Success
	}

	reviewer := interaction.Reviewer{In: reader, Printer: printer}
	for {
		decision, supplement, err := reviewer.Review(request)
		if err != nil {
			return fail(printer, exitcode.New(exitcode.Internal, "cannot review commit plan"))
		}
		switch decision {
		case interaction.Approve:
			if err := printer.Lines("Commit plan approved."); err != nil {
				return fail(printer, exitcode.New(exitcode.Internal, "cannot write output"))
			}
			return exitcode.Success
		case interaction.Reject:
			return fail(printer, exitcode.New(exitcode.Canceled, "commit plan rejected"))
		case interaction.Regenerate:
			plan, err = planFlow(context.Background(), root, snapshot, values, supplement)
			if err != nil {
				return fail(printer, err)
			}
			request = reviewRequest(snapshot, plan, definition, pushTarget)
		}
	}
}

func reviewRequest(snapshot gitstate.Snapshot, plan planning.Plan, definition *verification.Definition, pushTarget interaction.PushTarget) interaction.ReviewRequest {
	request := interaction.ReviewRequest{Plan: plan, Files: map[string]string{}, PushTarget: pushTarget}
	for _, change := range snapshot.Changes {
		path := ""
		if change.OldPath != nil && change.NewPath != nil && *change.OldPath != *change.NewPath {
			path = *change.OldPath + " -> " + *change.NewPath
		} else if change.NewPath != nil {
			path = *change.NewPath
		} else if change.OldPath != nil {
			path = *change.OldPath
		}
		request.Files[change.ID] = path
	}
	for _, excluded := range snapshot.Excluded {
		request.Excluded = append(request.Excluded, interaction.Excluded{Path: excluded.Path, Reason: excluded.Reason})
	}
	if definition != nil {
		for _, command := range definition.Commands {
			request.Verification = append(request.Verification, interaction.VerificationCommand{Name: command.Name, CWD: command.CWD, Argv: append([]string{}, command.Argv...)})
		}
	}
	return request
}

func classifyCollectionError(err error) error {
	switch {
	case gitstate.IsKind(err, gitstate.ErrorUsage):
		return exitcode.New(exitcode.Usage, err.Error())
	case gitstate.IsKind(err, gitstate.ErrorSafety):
		return exitcode.New(exitcode.Safety, err.Error())
	default:
		return exitcode.New(exitcode.Internal, err.Error())
	}
}

func printSnapshot(snapshot gitstate.Snapshot, printer *output.Printer) error {
	lines := []string{
		fmt.Sprintf("Repository: %s", snapshot.Root),
		fmt.Sprintf("HEAD: %s (%s)", snapshot.Head, snapshot.Branch),
	}
	for _, excluded := range snapshot.Excluded {
		lines = append(lines, fmt.Sprintf("Excluded: %s (%s)", excluded.Path, excluded.Reason))
	}
	for _, change := range snapshot.Changes {
		changePath := ""
		if change.NewPath != nil {
			changePath = *change.NewPath
		} else if change.OldPath != nil {
			changePath = *change.OldPath
		}
		lines = append(lines, fmt.Sprintf(
			"%s %s %s language=%s binary=%t opaque=%t change_hash=%s",
			change.ID, change.Status, changePath, change.Language, change.Binary, change.Opaque, change.ChangeHash,
		))
	}
	if len(snapshot.Changes) == 0 {
		lines = append(lines, "No target changes.")
	}
	return printer.Lines(lines...)
}
