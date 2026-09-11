// Package interaction contains the small, side-effect-free interaction
// boundaries used by the planning and dry-run flows.  It deliberately does
// not know how plans are generated or how commits are made.
package interaction

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/natsuki0413/commiter-cli/internal/planning"
)

type Decision string

const (
	Approve    Decision = "approve"
	Regenerate Decision = "regenerate"
	Reject     Decision = "reject"
)

// ReviewRequest is the immutable information displayed to a user. Supplement
// is only populated for a regenerate decision and is never logged by this
// package.
type ReviewRequest struct {
	Plan       planning.Plan
	Prompt     string
	Supplement string
}

// Reviewer presents a plan once and obtains y/r/N.  A short supplement is
// read only after r, then regenerate is returned to the caller. EOF is a
// rejection (never an approval).
type Reviewer struct {
	In  io.Reader
	Out io.Writer
}

func (r Reviewer) Review(request ReviewRequest) (Decision, string, error) {
	if r.In == nil || r.Out == nil {
		return Reject, "", errors.New("review input and output are required")
	}
	if err := renderPlan(r.Out, request.Plan); err != nil {
		return Reject, "", err
	}
	if _, err := io.WriteString(r.Out, "Create these commits? [y/r/N] "); err != nil {
		return Reject, "", err
	}
	scanner := bufio.NewScanner(r.In)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return Reject, "", err
		}
		return Reject, "", nil
	}
	answer := strings.TrimSpace(scanner.Text())
	switch strings.ToLower(answer) {
	case "y", "yes":
		return Approve, "", nil
	case "r", "regenerate":
		if _, err := io.WriteString(r.Out, "Why should the plan be regenerated? "); err != nil {
			return Reject, "", err
		}
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return Reject, "", err
			}
			return Reject, "", nil
		}
		return Regenerate, strings.TrimSpace(scanner.Text()), nil
	default:
		return Reject, "", nil
	}
}

func renderPlan(out io.Writer, plan planning.Plan) error {
	if _, err := fmt.Fprintf(out, "Plan (%d commits):\n", len(plan.Commits)); err != nil {
		return err
	}
	for index, commit := range plan.Commits {
		if _, err := fmt.Fprintf(out, "%d. %s files=%s\n", index+1, commit.Subject(), strings.Join(commit.FileIDs, ",")); err != nil {
			return err
		}
	}
	return nil
}

// PushTarget is a read-only resolution result. Resolved is false when the
// repository does not provide an unambiguous target; Reason is safe metadata,
// not command output.
type PushTarget struct {
	Remote   string `json:"remote"`
	Branch   string `json:"branch"`
	Resolved bool   `json:"resolved"`
	Reason   string `json:"reason,omitempty"`
}

// ResolvePushTarget prefers the configured upstream. Without one it resolves
// a target only when exactly one remote exists and HEAD is a safe local branch.
// It only invokes git read operations and never contacts or mutates a remote.
func ResolvePushTarget(root string) PushTarget {
	branch := git(root, "symbolic-ref", "--quiet", "--short", "HEAD")
	if branch == "" || !safeName(branch) {
		return PushTarget{Resolved: false, Reason: "push target cannot be resolved from detached or unsafe HEAD"}
	}
	upstreamRemote := git(root, "config", "--get", "branch."+branch+".remote")
	upstreamMerge := git(root, "config", "--get", "branch."+branch+".merge")
	if safeName(upstreamRemote) && strings.HasPrefix(upstreamMerge, "refs/heads/") && safeName(strings.TrimPrefix(upstreamMerge, "refs/heads/")) {
		return PushTarget{Remote: upstreamRemote, Branch: strings.TrimPrefix(upstreamMerge, "refs/heads/"), Resolved: true}
	}
	remotes := strings.Fields(git(root, "remote"))
	if len(remotes) == 1 && safeName(remotes[0]) {
		return PushTarget{Remote: remotes[0], Branch: branch, Resolved: true}
	}
	return PushTarget{Resolved: false, Reason: "push target is ambiguous: configure an upstream or leave exactly one remote"}
}

func git(root string, args ...string) string {
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	output, err := command.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func safeName(value string) bool {
	if value == "" || strings.ContainsAny(value, "\r\n\x00") || strings.TrimSpace(value) != value {
		return false
	}
	for _, runeValue := range value {
		if runeValue < 0x20 || runeValue == 0x7f {
			return false
		}
	}
	return true
}
