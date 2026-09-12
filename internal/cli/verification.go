package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/natsuki0413/commiter-cli/internal/exitcode"
	"github.com/natsuki0413/commiter-cli/internal/output"
	"github.com/natsuki0413/commiter-cli/internal/trust"
	"github.com/natsuki0413/commiter-cli/internal/verification"
)

// authorizeVerificationDefinition is the CLI bridge used immediately before a
// later pipeline executes verification. Keeping it separate prevents the
// currently incomplete main pipeline from asking for approval too early.
func authorizeVerificationDefinition(repo, stateDir string, definition *verification.Definition, input io.Reader, printer *output.Printer) error {
	return authorizeVerificationDefinitionContext(context.Background(), repo, stateDir, definition, input, printer)
}

func authorizeVerificationDefinitionContext(ctx context.Context, repo, stateDir string, definition *verification.Definition, input io.Reader, printer *output.Printer) error {
	if definition == nil {
		return printer.Lines("Verification: none")
	}
	store := trust.New(stateDir)
	err := verification.Authorize(repo, definition, store, func(request verification.ApprovalRequest) (bool, error) {
		lines := []string{
			"Verification approval required",
			"source_type: " + request.Definition.SourceType,
		}
		for _, command := range request.Definition.Commands {
			argv, err := json.Marshal(command.Argv)
			if err != nil {
				return false, fmt.Errorf("cannot format verification definition")
			}
			lines = append(lines,
				"command: "+command.Name,
				"cwd: "+command.CWD,
				"argv: "+string(argv),
			)
			if request.Definition.SourceType == verification.SourcePackageJSONAutodetect {
				lines = append(lines,
					"manifest_path: "+command.ManifestPath,
					"script_name: "+command.ScriptName,
					"script_body: "+command.ScriptBody,
				)
				for _, script := range command.ImplicitLifecycleScripts {
					lines = append(lines,
						"implicit_lifecycle_script_name: "+script.Name,
						"implicit_lifecycle_script_body: "+script.Body,
					)
				}
			}
		}
		lines = append(lines,
			"verification_definition_hash: "+request.Hash,
			"Approve verification definition? [y/N]",
		)
		if err := printer.Lines(lines...); err != nil {
			return false, fmt.Errorf("cannot write output")
		}
		line, err := readVerificationLineContext(ctx, input)
		if err != nil {
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			return false, nil
		}
		return strings.EqualFold(strings.TrimSpace(line), "y"), nil
	})
	if errors.Is(err, verification.ErrNotApproved) {
		return exitcode.New(exitcode.Canceled, "verification approval canceled")
	}
	return err
}

func readVerificationLine(input io.Reader) (string, error) {
	if reader, ok := input.(*bufio.Reader); ok {
		return reader.ReadString('\n')
	}
	return bufio.NewReader(input).ReadString('\n')
}

func readVerificationLineContext(ctx context.Context, input io.Reader) (string, error) {
	result := make(chan struct {
		line string
		err  error
	}, 1)
	go func() {
		line, err := readVerificationLine(input)
		result <- struct {
			line string
			err  error
		}{line, err}
	}()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case outcome := <-result:
		return outcome.line, outcome.err
	}
}
