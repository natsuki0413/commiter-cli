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

	"github.com/natsuki0413/commiter-cli/internal/output"
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
	Plan         planning.Plan
	Files        map[string]string
	Excluded     []Excluded
	Verification []VerificationCommand
	PushTarget   PushTarget
}

type Excluded struct{ Path, Reason string }
type VerificationCommand struct {
	Name, CWD string
	Argv      []string
}

// Reviewer presents a plan once and obtains y/r/N.  A short supplement is
// read only after r, then regenerate is returned to the caller. EOF is a
// rejection (never an approval).
type Reviewer struct {
	In      io.Reader
	Printer *output.Printer
}

func (r Reviewer) Review(request ReviewRequest) (Decision, string, error) {
	if r.In == nil || r.Printer == nil {
		return Reject, "", errors.New("review input and output are required")
	}
	if err := Render(r.Printer, request); err != nil {
		return Reject, "", err
	}
	if err := r.Printer.Lines(fmt.Sprintf("Create these %d commits? [y/r/N]", len(request.Plan.Commits))); err != nil {
		return Reject, "", err
	}
	reader := buffered(r.In)
	answer, err := readLine(reader)
	if err != nil {
		if errors.Is(err, io.EOF) {
			return Reject, "", nil
		}
		return Reject, "", err
	}
	if answer == "" {
		return Reject, "", nil
	}
	switch strings.ToLower(answer) {
	case "y", "yes":
		return Approve, "", nil
	case "r", "regenerate":
		if err := r.Printer.Lines("Why should the plan be regenerated?"); err != nil {
			return Reject, "", err
		}
		supplement, err := readLine(reader)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return Reject, "", nil
			}
			return Reject, "", err
		}
		if supplement == "" {
			return Reject, "", nil
		}
		return Regenerate, supplement, nil
	default:
		return Reject, "", nil
	}
}

func Render(printer *output.Printer, request ReviewRequest) error {
	lines := []string{fmt.Sprintf("Plan (%d commits):", len(request.Plan.Commits))}
	for index, commit := range request.Plan.Commits {
		files := make([]string, 0, len(commit.FileIDs))
		for _, id := range commit.FileIDs {
			files = append(files, id+"="+request.Files[id])
		}
		lines = append(lines, fmt.Sprintf("%d. %s files=%s", index+1, commit.Subject(), strings.Join(files, ",")))
	}
	if len(request.Excluded) == 0 {
		lines = append(lines, "Excluded: none")
	} else {
		for _, excluded := range request.Excluded {
			lines = append(lines, "Excluded: "+excluded.Path+" ("+excluded.Reason+")")
		}
	}
	if len(request.Verification) == 0 {
		lines = append(lines, "Verification: none")
	} else {
		for _, command := range request.Verification {
			lines = append(lines, fmt.Sprintf("Verification: %s cwd=%s argv=%s", command.Name, command.CWD, strings.Join(command.Argv, " ")))
		}
	}
	if request.PushTarget.Resolved {
		lines = append(lines, fmt.Sprintf("Push target: %s/%s", request.PushTarget.Remote, request.PushTarget.Branch))
	} else {
		lines = append(lines, "Push target: unresolved ("+request.PushTarget.Reason+")")
	}
	return printer.Lines(lines...)
}

func buffered(input io.Reader) *bufio.Reader {
	if reader, ok := input.(*bufio.Reader); ok {
		return reader
	}
	return bufio.NewReader(input)
}

func readLine(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	line = strings.TrimSpace(line)
	if err != nil && !(errors.Is(err, io.EOF) && line != "") {
		return "", err
	}
	return line, nil
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
	if !validBranch(root, branch) {
		return PushTarget{Resolved: false, Reason: "push target cannot be resolved from detached or unsafe HEAD"}
	}
	remotes := strings.Fields(git(root, "remote"))
	upstreamRemote := git(root, "config", "--get", "branch."+branch+".remote")
	upstreamMerge := git(root, "config", "--get", "branch."+branch+".merge")
	upstreamBranch := strings.TrimPrefix(upstreamMerge, "refs/heads/")
	if contains(remotes, upstreamRemote) && validBranch(root, upstreamBranch) && upstreamMerge == "refs/heads/"+upstreamBranch {
		return PushTarget{Remote: upstreamRemote, Branch: strings.TrimPrefix(upstreamMerge, "refs/heads/"), Resolved: true}
	}
	if len(remotes) == 1 && safeRemote(remotes[0]) {
		return PushTarget{Remote: remotes[0], Branch: branch, Resolved: true}
	}
	return PushTarget{Resolved: false, Reason: "push target is ambiguous: configure an upstream or leave exactly one remote"}
}

func validBranch(root, branch string) bool {
	if !safeName(branch) || strings.HasPrefix(branch, "-") {
		return false
	}
	command := exec.Command("git", "-C", root, "check-ref-format", "--branch", branch)
	return command.Run() == nil
}

func safeRemote(remote string) bool { return safeName(remote) && !strings.HasPrefix(remote, "-") }
func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target && safeRemote(value) {
			return true
		}
	}
	return false
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
