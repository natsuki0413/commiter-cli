package gitstate

import (
	"fmt"
	"path"
	"strings"
	"unicode/utf8"
)

type rawChange struct {
	status       string
	oldPath      *string
	newPath      *string
	oldMode      *string
	indexMode    *string
	headIdentity *string
	indexID      *string
	staged       bool
	unstaged     bool
	untracked    bool
}

func readStatus(root string, pathspecs []string) ([]rawChange, error) {
	args := []string{"status", "--porcelain=v2", "-z", "--untracked-files=all", "--ignored=no"}
	if len(pathspecs) > 0 {
		args = append(args, "--")
		args = append(args, pathspecs...)
	}
	data, err := gitBytes(root, args...)
	if err != nil {
		if len(pathspecs) > 0 {
			return nil, usage("invalid Git pathspec")
		}
		return nil, commandFailure("read Git status")
	}
	records := splitNUL(data)
	changes := make([]rawChange, 0, len(records))
	for i := 0; i < len(records); i++ {
		record := records[i]
		if record == "" {
			continue
		}
		switch record[0] {
		case '?':
			if len(record) < 3 || record[1] != ' ' {
				return nil, internal("cannot parse Git status")
			}
			filePath, err := normalizeRepoPath(record[2:])
			if err != nil {
				return nil, err
			}
			changes = append(changes, rawChange{status: "added", newPath: &filePath, unstaged: true, untracked: true})
		case '1':
			fields := strings.SplitN(record, " ", 9)
			if len(fields) != 9 || len(fields[1]) != 2 {
				return nil, internal("cannot parse Git status")
			}
			filePath, err := normalizeRepoPath(fields[8])
			if err != nil {
				return nil, err
			}
			change := rawChange{
				status:       statusName(fields[1], fields[3], fields[4]),
				oldPath:      stringPointer(filePath),
				newPath:      stringPointer(filePath),
				oldMode:      modePointer(fields[3]),
				indexMode:    modePointer(fields[4]),
				headIdentity: oidPointer(fields[6]),
				indexID:      oidPointer(fields[7]),
				staged:       fields[1][0] != '.',
				unstaged:     fields[1][1] != '.',
			}
			if change.status == "added" {
				change.oldPath, change.oldMode, change.headIdentity = nil, nil, nil
			}
			if change.status == "deleted" {
				change.newPath = nil
			}
			changes = append(changes, change)
		case '2':
			fields := strings.SplitN(record, " ", 10)
			if len(fields) != 10 || len(fields[1]) != 2 || i+1 >= len(records) {
				return nil, internal("cannot parse Git rename status")
			}
			newPath, err := normalizeRepoPath(fields[9])
			if err != nil {
				return nil, err
			}
			i++
			oldPath, err := normalizeRepoPath(records[i])
			if err != nil {
				return nil, err
			}
			changes = append(changes, rawChange{
				status:       "renamed",
				oldPath:      &oldPath,
				newPath:      &newPath,
				oldMode:      modePointer(fields[3]),
				indexMode:    modePointer(fields[4]),
				headIdentity: oidPointer(fields[6]),
				indexID:      oidPointer(fields[7]),
				staged:       fields[1][0] != '.',
				unstaged:     fields[1][1] != '.',
			})
		case 'u':
			return nil, safety("repository has unresolved conflicts")
		case '!':
			continue
		default:
			return nil, internal("cannot parse Git status")
		}
	}
	return coalesceDeleteAndRecreate(changes), nil
}

func coalesceDeleteAndRecreate(changes []rawChange) []rawChange {
	deleted := make(map[string]int)
	for index, change := range changes {
		if change.status == "deleted" && change.oldPath != nil {
			deleted[*change.oldPath] = index
		}
	}
	skip := make(map[int]bool)
	for index, change := range changes {
		if !change.untracked || change.newPath == nil {
			continue
		}
		deletedIndex, ok := deleted[*change.newPath]
		if !ok {
			continue
		}
		merged := changes[deletedIndex]
		merged.status = "modified"
		merged.newPath = stringPointer(*change.newPath)
		merged.unstaged = true
		merged.untracked = false
		changes[deletedIndex] = merged
		skip[index] = true
	}
	result := make([]rawChange, 0, len(changes)-len(skip))
	for index, change := range changes {
		if !skip[index] {
			result = append(result, change)
		}
	}
	return result
}

func splitNUL(data []byte) []string {
	parts := strings.Split(string(data), "\x00")
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

func normalizeRepoPath(value string) (string, error) {
	if value == "" || !utf8.ValidString(value) || strings.ContainsRune(value, '\x00') {
		return "", safety("Git returned an invalid repository path")
	}
	normalized := path.Clean(value)
	if normalized == "." || normalized == ".." || strings.HasPrefix(normalized, "../") || path.IsAbs(normalized) {
		return "", safety("Git returned a path outside the repository")
	}
	return normalized, nil
}

func statusName(xy, headMode, indexMode string) string {
	if xy[0] == 'A' || headMode == "000000" {
		return "added"
	}
	if xy[0] == 'D' || xy[1] == 'D' {
		return "deleted"
	}
	if xy[0] == 'T' || xy[1] == 'T' || headMode != indexMode {
		return "type_changed"
	}
	return "modified"
}

func stringPointer(value string) *string { return &value }

func modePointer(value string) *string {
	if value == "000000" {
		return nil
	}
	return &value
}

func oidPointer(value string) *string {
	if value == "" || strings.Trim(value, "0") == "" {
		return nil
	}
	return &value
}

func (change rawChange) displayPath() string {
	if change.newPath != nil {
		return *change.newPath
	}
	if change.oldPath != nil {
		return *change.oldPath
	}
	panic(fmt.Sprintf("change without path: %#v", change))
}
