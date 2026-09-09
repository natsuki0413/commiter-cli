package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/natsuki0413/commiter-cli/internal/output"
	"github.com/natsuki0413/commiter-cli/internal/trust"
	"github.com/natsuki0413/commiter-cli/internal/verification"
)

// authorizeVerificationDefinition is the CLI bridge used immediately before a
// later pipeline executes verification. Keeping it separate prevents the
// currently incomplete main pipeline from asking for approval too early.
func authorizeVerificationDefinition(repo, stateDir string, definition *verification.Definition, input io.Reader, printer *output.Printer) error {
	if definition == nil {
		return printer.Lines("Verification: none")
	}
	store := trust.New(stateDir)
	return verification.Authorize(repo, definition, store, func(request verification.ApprovalRequest) (bool, error) {
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
			}
		}
		lines = append(lines,
			"verification_definition_hash: "+request.Hash,
			"Approve verification definition? [y/N]",
		)
		if err := printer.Lines(lines...); err != nil {
			return false, fmt.Errorf("cannot write output")
		}
		scanner := bufio.NewScanner(input)
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return false, fmt.Errorf("cannot read approval")
			}
			return false, nil
		}
		return strings.EqualFold(strings.TrimSpace(scanner.Text()), "y"), nil
	})
}
