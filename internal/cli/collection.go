package cli

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"time"

	"github.com/natsuki0413/commiter-cli/internal/commitexec"
	"github.com/natsuki0413/commiter-cli/internal/config"
	"github.com/natsuki0413/commiter-cli/internal/exitcode"
	"github.com/natsuki0413/commiter-cli/internal/gitstate"
	"github.com/natsuki0413/commiter-cli/internal/interaction"
	"github.com/natsuki0413/commiter-cli/internal/output"
	"github.com/natsuki0413/commiter-cli/internal/planning"
	"github.com/natsuki0413/commiter-cli/internal/verification"
)

var mainInput io.Reader = os.Stdin

var verificationFlow = verification.Run
var commitFlow = commitexec.Execute

func runCollection(opts options, root string, values config.Values, printer *output.Printer) int {
	reader := bufio.NewReader(mainInput)
	repeatedMutation := map[string]int{}
	for {
		code, reanalyze := runCollectionCycle(opts, root, values, reader, printer, repeatedMutation)
		if !reanalyze {
			return code
		}
	}
}

func runCollectionCycle(opts options, root string, values config.Values, reader *bufio.Reader, printer *output.Printer, repeatedMutation map[string]int) (int, bool) {
	snapshot, err := collectSnapshot(root, values, opts.pathspecs, func(candidates []gitstate.Candidate) (bool, error) {
		lines := []string{"Sensitive candidates require approval before reading:"}
		for _, candidate := range candidates {
			lines = append(lines, fmt.Sprintf("%s (%s)", candidate.Path, candidate.Reason))
		}
		lines = append(lines, "Read all listed candidates? [y/N]")
		if err := printer.PromptLines(lines...); err != nil {
			return false, err
		}
		return readYes(reader), nil
	})
	if err != nil {
		return fail(printer, classifyCollectionError(err)), false
	}

	if len(snapshot.Changes) == 0 {
		if printer.JSON() {
			if err := printer.Value(map[string]any{"snapshot": snapshot, "dry_run": opts.dryRun}); err != nil {
				return fail(printer, exitcode.New(exitcode.Internal, "cannot write output")), false
			}
			return exitcode.Success, false
		}
		if err := printSnapshot(snapshot, printer); err != nil {
			return fail(printer, exitcode.New(exitcode.Internal, "cannot write output")), false
		}
		return exitcode.Success, false
	}

	initialState, err := verification.CaptureRepositoryState(root, snapshot.Untracked)
	if err != nil {
		return fail(printer, exitcode.New(exitcode.Safety, err.Error())), false
	}
	definition, err := verification.Resolve(root, values)
	if err != nil {
		return fail(printer, exitcode.New(exitcode.Usage, err.Error())), false
	}
	pushTarget := interaction.ResolvePushTarget(root)
	plan, err := planFlow(context.Background(), root, snapshot, values, "")
	if err != nil {
		return fail(printer, err), false
	}
	request := reviewRequest(snapshot, plan, definition, pushTarget)
	if printer.JSON() {
		result := map[string]any{"snapshot": snapshot, "plan": plan, "push_target": pushTarget, "dry_run": true, "verification": map[string]any{"definition": definition, "executed": false}}
		if err := printer.Value(result); err != nil {
			return fail(printer, exitcode.New(exitcode.Internal, "cannot write output")), false
		}
		return exitcode.Success, false
	}
	if opts.dryRun {
		if err := interaction.Render(printer, request); err != nil {
			return fail(printer, exitcode.New(exitcode.Internal, "cannot write output")), false
		}
		return exitcode.Success, false
	}

	if values.CommitConfirm {
		reviewer := interaction.Reviewer{In: reader, Printer: printer}
		for {
			decision, supplement, err := reviewer.Review(request)
			if err != nil {
				return fail(printer, exitcode.New(exitcode.Internal, "cannot review commit plan")), false
			}
			switch decision {
			case interaction.Approve:
				if err := printer.Lines("Commit plan approved."); err != nil {
					return fail(printer, exitcode.New(exitcode.Internal, "cannot write output")), false
				}
				goto approved
			case interaction.Reject:
				return fail(printer, exitcode.New(exitcode.Canceled, "commit plan rejected")), false
			case interaction.Regenerate:
				plan, err = planFlow(context.Background(), root, snapshot, values, supplement)
				if err != nil {
					return fail(printer, err), false
				}
				request = reviewRequest(snapshot, plan, definition, pushTarget)
			}
		}
	} else {
		if err := interaction.Render(printer, request); err != nil {
			return fail(printer, exitcode.New(exitcode.Internal, "cannot write output")), false
		}
		if err := printer.Lines("Commit confirmation skipped by configuration."); err != nil {
			return fail(printer, exitcode.New(exitcode.Internal, "cannot write output")), false
		}
	}

approved:
	if err := authorizeVerificationDefinition(root, configStateDir(root), definition, reader, printer); err != nil {
		return fail(printer, err), false
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	runResult, runErr := verificationFlow(ctx, root, definition, time.Duration(values.Timeout)*time.Second, snapshot.Untracked)
	if err := printVerificationResults(printer, runResult); err != nil {
		if restoreErr := initialState.RestoreIndex(); restoreErr != nil {
			return fail(printer, exitcode.New(exitcode.Commit, "verification output failed and index restoration is unknown")), false
		}
		return fail(printer, exitcode.New(exitcode.Internal, "cannot write verification output")), false
	}
	if runErr != nil {
		if err := initialState.RestoreIndex(); err != nil {
			return fail(printer, exitcode.New(exitcode.Commit, "verification failed and index restoration is unknown")), false
		}
		if err := printer.Lines("Initial index restored after verification failure."); err != nil {
			return fail(printer, exitcode.New(exitcode.Internal, "cannot report index restoration")), false
		}
		var failure *verification.RunError
		if errors.As(runErr, &failure) && failure.Kind == verification.RunInterrupted {
			return fail(printer, exitcode.New(exitcode.Interrupted, runErr.Error())), false
		}
		return fail(printer, exitcode.New(exitcode.Verification, runErr.Error())), false
	}

	afterState, stateErr := verification.CaptureRepositoryState(root, snapshot.Untracked)
	current, collectErr := revalidateSnapshot(root, values, opts.pathspecs, snapshot)
	if ctx.Err() != nil {
		if err := initialState.RestoreIndex(); err != nil {
			return fail(printer, exitcode.New(exitcode.Commit, "verification interrupted and index restoration is unknown")), false
		}
		if err := printer.Lines("Initial index restored after verification interruption."); err != nil {
			return fail(printer, exitcode.New(exitcode.Internal, "cannot report index restoration")), false
		}
		return fail(printer, exitcode.New(exitcode.Interrupted, "verification interrupted")), false
	}
	changed := verification.ChangedPaths(initialState, afterState)
	if stateErr != nil {
		changed = append(changed, "<repository-state>")
	}
	if collectErr != nil {
		changed = append(changed, "<snapshot>")
	} else {
		changed = append(changed, changedSnapshotPaths(snapshot, current)...)
	}
	changed = uniqueStrings(changed)
	if len(changed) > 0 {
		if err := initialState.RestoreIndex(); err != nil {
			return fail(printer, exitcode.New(exitcode.Commit, "verification mutation detected and index restoration is unknown")), false
		}
		if err := printer.Lines("Initial index restored after verification mutation."); err != nil {
			return fail(printer, exitcode.New(exitcode.Internal, "cannot report index restoration")), false
		}
		mutationCommands := mutatingVerificationCommands(runResult)
		if len(mutationCommands) == 0 {
			if lastCommand := lastVerificationCommand(runResult); lastCommand != "" {
				mutationCommands = append(mutationCommands, lastCommand)
			}
		}
		for _, command := range mutationCommands {
			repeatedMutation[command]++
			if repeatedMutation[command] > 1 {
				if err := printer.Lines("Repeated verification mutation command: " + command); err != nil {
					return fail(printer, exitcode.New(exitcode.Internal, "cannot write output")), false
				}
			}
		}
		lines := []string{"Verification changed Git-visible state:"}
		for _, path := range changed {
			lines = append(lines, path)
		}
		lines = append(lines, "Re-analyze changed state? [y/N]")
		if err := printer.PromptLines(lines...); err != nil {
			return fail(printer, exitcode.New(exitcode.Internal, "cannot write output")), false
		}
		if readYes(reader) {
			return exitcode.Success, true
		}
		return fail(printer, exitcode.New(exitcode.Safety, "changed state was not re-analyzed")), false
	}

	var hookOutput bytes.Buffer
	result, commitErr := commitFlow(commitexec.Options{Context: ctx, Root: root, Changes: snapshot.Changes, Plan: plan, Writer: &hookOutput})
	if hookOutput.Len() > 0 {
		if err := printer.Lines("Git hook output: " + hookOutput.String()); err != nil {
			return fail(printer, exitcode.New(exitcode.Internal, "cannot write hook output")), false
		}
	}
	if commitErr != nil {
		return fail(printer, classifyCommitError(commitErr)), false
	}
	lines := make([]string, 0, len(result.Hashes)+1)
	lines = append(lines, "Commit execution completed.")
	for _, hash := range result.Hashes {
		lines = append(lines, "commit: "+hash)
	}
	if err := printer.Lines(lines...); err != nil {
		return fail(printer, exitcode.New(exitcode.Internal, "cannot write output")), false
	}
	return exitcode.Success, false
}

func collectSnapshot(root string, values config.Values, pathspecs []string, approve func([]gitstate.Candidate) (bool, error)) (gitstate.Snapshot, error) {
	return gitstate.Collect(root, gitstate.Options{
		Pathspecs:                  pathspecs,
		Include:                    values.Include,
		Exclude:                    values.Exclude,
		AdditionalSensitiveGlobs:   values.SensitivePatterns,
		ApproveSensitiveCandidates: approve,
	})
}

func configStateDir(root string) string {
	paths, _ := config.DefaultPaths(root)
	return paths.StateDir
}

func readYes(reader *bufio.Reader) bool {
	line, err := reader.ReadString('\n')
	if err != nil {
		return false
	}
	answer := strings.TrimSpace(line)
	return answer == "y" || answer == "Y" || strings.EqualFold(answer, "yes")
}

func revalidateSnapshot(root string, values config.Values, pathspecs []string, original gitstate.Snapshot) (gitstate.Snapshot, error) {
	approved := map[string]bool{}
	for _, change := range original.Changes {
		if change.Sensitive {
			for _, path := range snapshotChangePaths(change) {
				approved[path] = true
			}
		}
	}
	return collectSnapshot(root, values, pathspecs, func(candidates []gitstate.Candidate) (bool, error) {
		for _, candidate := range candidates {
			if !approved[candidate.Path] {
				return false, nil
			}
		}
		return true, nil
	})
}

func changedSnapshotPaths(before, after gitstate.Snapshot) []string {
	changed := map[string]bool{}
	beforeChanges := map[string]gitstate.Change{}
	afterChanges := map[string]gitstate.Change{}
	for _, change := range before.Changes {
		beforeChanges[snapshotChangeKey(change)] = change
	}
	for _, change := range after.Changes {
		afterChanges[snapshotChangeKey(change)] = change
	}
	for key, prior := range beforeChanges {
		current, ok := afterChanges[key]
		if !ok || current.ChangeHash != prior.ChangeHash {
			for _, path := range snapshotChangePaths(prior) {
				changed[path] = true
			}
			if ok {
				for _, path := range snapshotChangePaths(current) {
					changed[path] = true
				}
			}
		}
	}
	for key, current := range afterChanges {
		if _, ok := beforeChanges[key]; !ok {
			for _, path := range snapshotChangePaths(current) {
				changed[path] = true
			}
		}
	}
	if before.Head != after.Head {
		changed["HEAD"] = true
	}
	if before.IndexIdentity != after.IndexIdentity && len(changed) == 0 {
		changed["<index>"] = true
	}
	return mapKeys(changed)
}

func snapshotChangeKey(change gitstate.Change) string {
	oldPath, newPath := "", ""
	if change.OldPath != nil {
		oldPath = *change.OldPath
	}
	if change.NewPath != nil {
		newPath = *change.NewPath
	}
	return change.Status + "\x00" + oldPath + "\x00" + newPath
}

func snapshotChangePaths(change gitstate.Change) []string {
	paths := []string{}
	if change.OldPath != nil {
		paths = append(paths, *change.OldPath)
	}
	if change.NewPath != nil && (change.OldPath == nil || *change.NewPath != *change.OldPath) {
		paths = append(paths, *change.NewPath)
	}
	return paths
}

func printVerificationResults(printer *output.Printer, result verification.RunResult) error {
	for _, command := range result.Commands {
		if err := printer.Lines("Verification command: " + command.Name); err != nil {
			return err
		}
		if command.Output != "" {
			if err := printer.Lines("Verification output (" + command.Name + "): " + command.Output); err != nil {
				return err
			}
		}
	}
	return nil
}

func lastVerificationCommand(result verification.RunResult) string {
	if len(result.Commands) == 0 {
		return ""
	}
	return result.Commands[len(result.Commands)-1].Name
}

func mutatingVerificationCommands(result verification.RunResult) []string {
	commands := []string{}
	for _, command := range result.Commands {
		if len(command.ChangedPaths) > 0 {
			commands = append(commands, command.Name)
		}
	}
	return uniqueStrings(commands)
}

func classifyCommitError(err error) error {
	var failure *commitexec.Error
	if !errors.As(err, &failure) {
		return exitcode.New(exitcode.Commit, "commit execution failed")
	}
	if failure.Code == commitexec.ExitInterrupted {
		return exitcode.New(exitcode.Interrupted, failure.Message)
	}
	return exitcode.New(exitcode.Commit, failure.Message)
}

func uniqueStrings(values []string) []string {
	set := map[string]bool{}
	for _, value := range values {
		set[value] = true
	}
	return mapKeys(set)
}

func mapKeys(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
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
