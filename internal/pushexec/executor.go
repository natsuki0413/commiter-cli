// Package pushexec performs the single network mutation allowed after an
// approved commit plan has completed successfully.
package pushexec

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/natsuki0413/commiter-cli/internal/interaction"
)

var ErrPush = errors.New("git push failed; local commits were kept")

// Execute pushes HEAD exactly once to the already-resolved target. The
// explicit refspec avoids depending on push.default, and -- separates the
// validated remote name from command options.
func Execute(ctx context.Context, root string, target interaction.PushTarget) error {
	if !target.Resolved || target.Remote == "" || target.Branch == "" {
		return errors.New("push target is unresolved; local commits were kept")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return errors.New("push repository cannot be resolved; local commits were kept")
	}
	args := []string{"-C", absolute, "push"}
	if target.SetUpstream {
		args = append(args, "--set-upstream")
	}
	args = append(args, "--", target.Remote, "HEAD:refs/heads/"+target.Branch)
	command := exec.CommandContext(ctx, "git", args...)
	command.Env = append(os.Environ(), "LC_ALL=C")
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrPush
	}
	return nil
}
