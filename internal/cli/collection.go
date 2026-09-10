package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/natsuki0413/commiter-cli/internal/config"
	"github.com/natsuki0413/commiter-cli/internal/exitcode"
	"github.com/natsuki0413/commiter-cli/internal/gitstate"
	"github.com/natsuki0413/commiter-cli/internal/output"
)

var mainInput io.Reader = os.Stdin

func runCollection(opts options, root string, values config.Values, printer *output.Printer) int {
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
			scanner := bufio.NewScanner(mainInput)
			if !scanner.Scan() {
				return false, scanner.Err()
			}
			answer := strings.TrimSpace(scanner.Text())
			return answer == "y" || answer == "Y" || strings.EqualFold(answer, "yes"), nil
		},
	})
	if err != nil {
		return fail(printer, classifyCollectionError(err))
	}

	planningUnavailable := len(snapshot.Changes) > 0
	if printer.JSON() {
		result := map[string]any{"snapshot": snapshot}
		if planningUnavailable {
			result["error"] = "commit planning is not implemented yet"
			result["exit_code"] = exitcode.Internal
		}
		if err := printer.Value(result); err != nil {
			return fail(printer, exitcode.New(exitcode.Internal, "cannot write output"))
		}
	} else if err := printSnapshot(snapshot, printer); err != nil {
		return fail(printer, exitcode.New(exitcode.Internal, "cannot write output"))
	}
	if !planningUnavailable {
		return exitcode.Success
	}
	if printer.JSON() {
		return exitcode.Internal
	}
	return fail(printer, exitcode.New(exitcode.Internal, "commit planning is not implemented yet"))
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
