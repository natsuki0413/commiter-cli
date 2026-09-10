package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/natsuki0413/commiter-cli/internal/exitcode"
	"github.com/natsuki0413/commiter-cli/internal/output"
	"github.com/natsuki0413/commiter-cli/internal/trust"
	"github.com/natsuki0413/commiter-cli/internal/verification"
)

func TestAuthorizeVerificationDefinitionPrintsNoneWithoutTrust(t *testing.T) {
	stateDir := t.TempDir()
	var stdout, stderr bytes.Buffer
	err := authorizeVerificationDefinition(t.TempDir(), stateDir, nil, strings.NewReader(""), output.New(&stdout, &stderr, false))
	if err != nil || stdout.String() != "Verification: none\n" || stderr.Len() != 0 {
		t.Fatalf("error = %v, stdout = %q, stderr = %q", err, stdout.String(), stderr.String())
	}
	entries, err := trust.New(stateDir).List()
	if err != nil || len(entries) != 0 {
		t.Fatalf("entries = %#v, error = %v", entries, err)
	}
}

func TestAuthorizeVerificationDefinitionDisplaysAndPersistsFullDefinition(t *testing.T) {
	repo, stateDir := t.TempDir(), t.TempDir()
	definition := &verification.Definition{
		SchemaVersion: 1,
		SourceType:    verification.SourcePackageJSONAutodetect,
		Commands: []verification.Command{{
			Name:         "test",
			CWD:          ".",
			Argv:         []string{"npm", "run", "--ignore-scripts", "test"},
			ManifestPath: "package.json",
			ScriptName:   "test",
			ScriptBody:   "vitest run",
		}},
	}
	var stdout, stderr bytes.Buffer
	err := authorizeVerificationDefinition(repo, stateDir, definition, strings.NewReader("y\n"), output.New(&stdout, &stderr, false))
	if err != nil || stderr.Len() != 0 {
		t.Fatalf("error = %v, stdout = %q, stderr = %q", err, stdout.String(), stderr.String())
	}
	for _, want := range []string{
		"source_type: package_json_autodetect",
		"command: test",
		"cwd: .",
		`argv: [\"npm\",\"run\",\"--ignore-scripts\",\"test\"]`,
		"manifest_path: package.json",
		"script_name: test",
		"script_body: vitest run",
		"verification_definition_hash:",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, want %q", stdout.String(), want)
		}
	}
	entries, err := trust.New(stateDir).List()
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries = %#v, error = %v", entries, err)
	}
	canonical, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	if entries[0].RepoPath != canonical || entries[0].SourceType != verification.SourcePackageJSONAutodetect {
		t.Fatalf("entry = %#v", entries[0])
	}

	stdout.Reset()
	err = authorizeVerificationDefinition(repo, stateDir, definition, strings.NewReader(""), output.New(&stdout, &stderr, false))
	if err != nil || stdout.Len() != 0 {
		t.Fatalf("trusted error = %v, stdout = %q", err, stdout.String())
	}
}

func TestAuthorizeVerificationDefinitionMapsDenialToCanceled(t *testing.T) {
	repo, stateDir := t.TempDir(), t.TempDir()
	definition := &verification.Definition{
		SchemaVersion: 1,
		SourceType:    verification.SourceRepoConfig,
		Commands:      []verification.Command{{Name: "test", CWD: ".", Argv: []string{"go", "test", "./..."}}},
	}
	var stdout, stderr bytes.Buffer
	err := authorizeVerificationDefinition(repo, stateDir, definition, strings.NewReader("n\n"), output.New(&stdout, &stderr, false))
	if exitcode.Code(err) != exitcode.Canceled {
		t.Fatalf("error = %v, code = %d", err, exitcode.Code(err))
	}
	entries, listErr := trust.New(stateDir).List()
	if listErr != nil || len(entries) != 0 {
		t.Fatalf("entries = %#v, error = %v", entries, listErr)
	}
}
