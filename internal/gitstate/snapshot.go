package gitstate

import (
	"crypto/sha1" // #nosec G505 -- Git SHA-1 object identity is a repository format, not a security primitive.
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/go-enry/go-enry/v2"
)

type hashRecord struct {
	SchemaVersion    int     `json:"schema_version"`
	Status           string  `json:"status"`
	OldPath          *string `json:"old_path"`
	NewPath          *string `json:"new_path"`
	OldMode          *string `json:"old_mode"`
	NewMode          *string `json:"new_mode"`
	HeadIdentity     *string `json:"head_identity"`
	WorktreeKind     string  `json:"working_tree_kind"`
	WorktreeIdentity *string `json:"working_tree_identity"`
}

func buildChange(root string, raw rawChange, files FileReader, approvedSensitive bool, objectFormat string) (Change, bool, error) {
	change := Change{
		Status:       raw.status,
		OldPath:      raw.oldPath,
		NewPath:      raw.newPath,
		OldMode:      raw.oldMode,
		HeadIdentity: raw.headIdentity,
		Staged:       raw.staged,
		Unstaged:     raw.unstaged,
		Sensitive:    approvedSensitive,
	}
	if raw.status == "deleted" {
		if raw.oldPath != nil {
			change.Vendor = enry.IsVendor(*raw.oldPath)
			if raw.oldMode == nil || *raw.oldMode != "160000" {
				if raw.headIdentity != nil {
					content, err := gitBytes(root, "cat-file", "blob", *raw.headIdentity)
					if err != nil {
						return Change{}, false, internal("cannot classify deleted file")
					}
					change.Size = int64(len(content))
					change.Binary = enry.IsBinary(content)
					if !change.Binary {
						change.Language = enry.GetLanguage(*raw.oldPath, content)
					}
					change.Opaque = change.Binary
				}
			}
		}
		change.WorktreeKind = "deleted"
		if err := setChangeHash(&change); err != nil {
			return Change{}, false, err
		}
		return change, true, nil
	}

	repoPath := raw.displayPath()
	absolute := filepath.Join(root, filepath.FromSlash(repoPath))
	info, err := files.Lstat(absolute)
	if err != nil {
		if raw.status == "added" && errors.Is(err, fs.ErrNotExist) {
			return Change{}, false, nil
		}
		return Change{}, false, internal("cannot inspect selected file")
	}
	change.Size = info.Size()
	change.NewMode = modeFromInfo(info, raw)
	change.Vendor = enry.IsVendor(repoPath)
	var gitIdentity *string

	switch {
	case change.NewMode != nil && *change.NewMode == "160000":
		identity := raw.indexID
		if info.IsDir() {
			if current, currentErr := gitText(absolute, "rev-parse", "--verify", "HEAD"); currentErr == nil {
				identity = &current
			}
		}
		if identity == nil {
			return Change{}, false, internal("cannot determine submodule identity")
		}
		change.WorktreeKind = "gitlink"
		change.WorktreeID = identity
		gitIdentity = identity
	case info.Mode()&fs.ModeSymlink != 0:
		target, err := files.Readlink(absolute)
		if err != nil {
			return Change{}, false, internal("cannot read selected symlink")
		}
		identity := sha256Hex([]byte(target))
		change.WorktreeKind = "symlink"
		change.WorktreeID = &identity
		change.Size = int64(len([]byte(target)))
		gitOID := gitBlobOID([]byte(target), objectFormat)
		gitIdentity = &gitOID
	default:
		if !info.Mode().IsRegular() {
			return Change{}, false, safety("selected path has an unsupported file type")
		}
		content, err := files.ReadFile(absolute)
		if err != nil {
			return Change{}, false, internal("cannot read selected file")
		}
		identity := sha256Hex(content)
		change.WorktreeKind = "file"
		change.WorktreeID = &identity
		change.Size = int64(len(content))
		change.Binary = enry.IsBinary(content)
		if !change.Binary {
			change.Language = enry.GetLanguage(repoPath, content)
		}
		change.Opaque = change.Binary || (raw.untracked && change.Size >= LargeUntrackedSize)
		gitOID := gitBlobOID(content, objectFormat)
		gitIdentity = &gitOID
	}
	if change.Language == "" && !change.Binary && change.WorktreeKind != "gitlink" {
		change.Language = enry.GetLanguage(repoPath, nil)
	}
	if unchangedFromHead(change, gitIdentity) {
		return Change{}, false, nil
	}
	if err := setChangeHash(&change); err != nil {
		return Change{}, false, err
	}
	return change, true, nil
}

func unchangedFromHead(change Change, gitIdentity *string) bool {
	if change.HeadIdentity == nil || change.OldPath == nil || change.NewPath == nil || *change.OldPath != *change.NewPath || change.OldMode == nil || change.NewMode == nil || *change.OldMode != *change.NewMode {
		return false
	}
	return gitIdentity != nil && *gitIdentity == *change.HeadIdentity
}

func gitBlobOID(content []byte, objectFormat string) string {
	header := []byte(fmt.Sprintf("blob %d\x00", len(content)))
	value := append(header, content...)
	if objectFormat == "sha256" {
		return sha256Hex(value)
	}
	sum := sha1.Sum(value)
	return hex.EncodeToString(sum[:])
}

func modeFromInfo(info fs.FileInfo, raw rawChange) *string {
	if raw.indexMode != nil && *raw.indexMode == "160000" {
		return stringPointer("160000")
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return stringPointer("120000")
	}
	if info.Mode().IsRegular() {
		if info.Mode().Perm()&0o111 != 0 {
			return stringPointer("100755")
		}
		return stringPointer("100644")
	}
	return raw.indexMode
}

func setChangeHash(change *Change) error {
	record := hashRecord{
		SchemaVersion:    HashSchemaVersion,
		Status:           change.Status,
		OldPath:          change.OldPath,
		NewPath:          change.NewPath,
		OldMode:          change.OldMode,
		NewMode:          change.NewMode,
		HeadIdentity:     change.HeadIdentity,
		WorktreeKind:     change.WorktreeKind,
		WorktreeIdentity: change.WorktreeID,
	}
	canonical, err := json.Marshal(record)
	if err != nil {
		return internal("cannot encode change identity")
	}
	change.ChangeHash = sha256Hex(canonical)
	return nil
}

func sha256Hex(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func selectedByGlobs(change rawChange, includes, excludes []string) (bool, error) {
	paths := make([]string, 0, 2)
	if change.oldPath != nil {
		paths = append(paths, *change.oldPath)
	}
	if change.newPath != nil && (change.oldPath == nil || *change.newPath != *change.oldPath) {
		paths = append(paths, *change.newPath)
	}
	if len(includes) > 0 {
		matched, err := anyGlob(paths, includes)
		if err != nil || !matched {
			return false, err
		}
	}
	matched, err := anyGlob(paths, excludes)
	return !matched, err
}

func anyGlob(paths, patterns []string) (bool, error) {
	for _, pattern := range patterns {
		for _, value := range paths {
			matched, err := doublestar.PathMatch(pattern, value)
			if err != nil {
				return false, usage("invalid analysis glob")
			}
			if matched {
				return true, nil
			}
		}
	}
	return false, nil
}

func indexIdentity(root string) (string, error) {
	staged, err := gitBytes(root, "ls-files", "--stage", "-z")
	if err != nil {
		return "", commandFailure("snapshot Git index")
	}
	flags, err := gitBytes(root, "ls-files", "-v", "-z")
	if err != nil {
		return "", commandFailure("snapshot Git index flags")
	}
	canonical := make([]byte, 0, len(staged)+len(flags)+16)
	canonical = append(canonical, []byte("stage\x00")...)
	canonical = append(canonical, staged...)
	canonical = append(canonical, []byte("flags\x00")...)
	canonical = append(canonical, flags...)
	return sha256Hex(canonical), nil
}

func pointerValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func fileID(index int) string { return fmt.Sprintf("F%03d", index+1) }
