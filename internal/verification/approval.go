package verification

import (
	"errors"
	"fmt"

	"github.com/natsuki0413/commiter-cli/internal/trust"
)

var ErrNotApproved = errors.New("verification definition was not approved")

type ApprovalRequest struct {
	Definition Definition `json:"definition"`
	Hash       string     `json:"verification_definition_hash"`
}

// Authorize checks repository-scoped trust and asks for approval only when the
// current definition is new or changed. A nil definition means there is no
// verification to authorize and never creates trust state.
func Authorize(repo string, definition *Definition, store trust.Store, confirm func(ApprovalRequest) (bool, error)) error {
	if definition == nil {
		return nil
	}
	hash, err := definition.Hash()
	if err != nil {
		return err
	}
	matched, err := store.Matches(repo, hash)
	if err != nil {
		return err
	}
	if matched {
		return nil
	}
	if confirm == nil {
		return fmt.Errorf("approval callback is required")
	}
	requestDefinition := cloneDefinitionForApproval(*definition)
	approved, err := confirm(ApprovalRequest{Definition: requestDefinition, Hash: hash})
	if err != nil {
		return err
	}
	if !approved {
		return ErrNotApproved
	}
	return store.Approve(trust.Entry{
		RepoPath:       repo,
		DefinitionHash: hash,
		SourceType:     definition.SourceType,
		Commands:       definition.Argv(),
	})
}

func cloneDefinitionForApproval(definition Definition) Definition {
	cloned := definition
	cloned.Commands = make([]Command, len(definition.Commands))
	for index, command := range definition.Commands {
		cloned.Commands[index] = command
		cloned.Commands[index].Argv = append([]string{}, command.Argv...)
		cloned.Commands[index].ImplicitLifecycleScripts = append([]ManifestScript{}, command.ImplicitLifecycleScripts...)
	}
	return cloned
}
