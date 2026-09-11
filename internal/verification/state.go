package verification

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type fileState struct {
	Record  string
	Mode    os.FileMode
	Size    int64
	ModTime int64
}

// RepositoryState records Git-visible state without reading working-tree file
// contents. Exact hashes for selected files are compared separately by the CLI.
type RepositoryState struct {
	Head      string
	IndexPath string
	IndexMode os.FileMode
	IndexData []byte
	Files     map[string]fileState
}

func CaptureRepositoryState(root string) (RepositoryState, error) {
	head, err := gitOutput(root, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return RepositoryState{}, fmt.Errorf("cannot inspect HEAD before commit")
	}
	indexName, err := gitOutput(root, "rev-parse", "--git-path", "index")
	if err != nil {
		return RepositoryState{}, fmt.Errorf("cannot resolve Git index before commit")
	}
	indexPath := strings.TrimSuffix(string(indexName), "\n")
	if !filepath.IsAbs(indexPath) {
		indexPath = filepath.Join(root, indexPath)
	}
	indexInfo, err := os.Stat(indexPath)
	if err != nil {
		return RepositoryState{}, fmt.Errorf("cannot inspect Git index before commit")
	}
	indexData, err := os.ReadFile(indexPath)
	if err != nil {
		return RepositoryState{}, fmt.Errorf("cannot preserve Git index before commit")
	}
	status, err := gitOutput(root, "status", "--porcelain=v2", "-z", "--untracked-files=all", "--ignored=no")
	if err != nil {
		return RepositoryState{}, fmt.Errorf("cannot inspect Git-visible state before commit")
	}
	files, err := statusFileStates(root, status)
	if err != nil {
		return RepositoryState{}, err
	}
	return RepositoryState{
		Head:      strings.TrimSuffix(string(head), "\n"),
		IndexPath: filepath.Clean(indexPath),
		IndexMode: indexInfo.Mode().Perm(),
		IndexData: indexData,
		Files:     files,
	}, nil
}

func (s RepositoryState) RestoreIndex() error {
	if s.IndexPath == "" || s.IndexData == nil {
		return fmt.Errorf("initial Git index is unavailable")
	}
	return os.WriteFile(s.IndexPath, s.IndexData, s.IndexMode)
}

func ChangedPaths(before, after RepositoryState) []string {
	changed := map[string]bool{}
	for path, prior := range before.Files {
		current, ok := after.Files[path]
		if !ok || current != prior {
			changed[path] = true
		}
	}
	for path, current := range after.Files {
		prior, ok := before.Files[path]
		if !ok || current != prior {
			changed[path] = true
		}
	}
	if before.Head != after.Head {
		changed["HEAD"] = true
	}
	if !bytes.Equal(before.IndexData, after.IndexData) && len(changed) == 0 {
		changed["<index>"] = true
	}
	result := make([]string, 0, len(changed))
	for path := range changed {
		result = append(result, path)
	}
	sort.Strings(result)
	return result
}

func statusFileStates(root string, status []byte) (map[string]fileState, error) {
	records := bytes.Split(bytes.TrimSuffix(status, []byte{0}), []byte{0})
	result := map[string]fileState{}
	for index := 0; index < len(records); index++ {
		record := string(records[index])
		if record == "" {
			continue
		}
		path := ""
		switch record[0] {
		case '?':
			if len(record) < 3 {
				return nil, fmt.Errorf("cannot parse Git-visible state")
			}
			path = record[2:]
		case '1':
			fields := strings.SplitN(record, " ", 9)
			if len(fields) != 9 {
				return nil, fmt.Errorf("cannot parse Git-visible state")
			}
			path = fields[8]
		case '2':
			fields := strings.SplitN(record, " ", 10)
			if len(fields) != 10 || index+1 >= len(records) {
				return nil, fmt.Errorf("cannot parse Git-visible state")
			}
			path = fields[9]
			index++
			oldPath := string(records[index])
			result[oldPath] = fileMetadata(root, oldPath, record+"\x00"+oldPath)
		case 'u':
			return nil, fmt.Errorf("repository has unresolved conflicts")
		default:
			return nil, fmt.Errorf("cannot parse Git-visible state")
		}
		result[path] = fileMetadata(root, path, record)
	}
	return result, nil
}

func fileMetadata(root, path, record string) fileState {
	state := fileState{Record: record}
	info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		return state
	}
	state.Mode = info.Mode()
	state.Size = info.Size()
	state.ModTime = info.ModTime().UnixNano()
	return state
}

func gitOutput(root string, args ...string) ([]byte, error) {
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	command.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C")
	return command.Output()
}
