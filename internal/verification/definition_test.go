package verification

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/natsuki0413/commiter-cli/internal/config"
)

func TestResolveExplicitCommandsTakePriorityAndNormalizeCWD(t *testing.T) {
	root := t.TempDir()
	work := filepath.Join(root, "work")
	if err := os.Mkdir(work, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(work, filepath.Join(root, "work-link")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "package.json"), `{"packageManager":"npm@10","scripts":{"lint":"ignored"}}`)
	definition, err := Resolve(root, config.Values{
		Autodetect:  true,
		CommandsSet: true,
		Commands: []config.VerificationCommand{
			{Name: "unit", Argv: []string{"go", "test", "./..."}, CWD: "work-link"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if definition.SourceType != SourceRepoConfig || len(definition.Commands) != 1 {
		t.Fatalf("definition = %#v", definition)
	}
	want := Command{Name: "unit", CWD: "work", Argv: []string{"go", "test", "./..."}}
	if !reflect.DeepEqual(definition.Commands[0], want) {
		t.Fatalf("command = %#v, want %#v", definition.Commands[0], want)
	}
}

func TestResolveUsesOneRootLockfileAsPackageManagerEvidence(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "package.json"), `{"scripts":{"test":"vitest"}}`)
	writeFile(t, filepath.Join(root, "yarn.lock"), "")
	definition, err := Resolve(root, config.Values{Autodetect: true})
	if err != nil {
		t.Fatal(err)
	}
	if definition == nil || !reflect.DeepEqual(definition.Commands[0].Argv, []string{"yarn", "run", "test"}) {
		t.Fatalf("definition = %#v", definition)
	}
}

func TestResolveAutodetectsOnlyOrderedRootScripts(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "package.json"), `{
  "packageManager": "pnpm@9.1.0",
  "scripts": {
    "build": "next build",
    "format": "prettier --write .",
    "test": "vitest run",
    "lint": "eslint .",
    "typecheck": "tsc --noEmit"
  }
}`)
	definition, err := Resolve(root, config.Values{Autodetect: true})
	if err != nil {
		t.Fatal(err)
	}
	if definition == nil || definition.SourceType != SourcePackageJSONAutodetect {
		t.Fatalf("definition = %#v", definition)
	}
	wantNames := []string{"lint", "typecheck", "test", "build"}
	for index, wantName := range wantNames {
		command := definition.Commands[index]
		if command.Name != wantName || !reflect.DeepEqual(command.Argv, []string{"pnpm", "--config.enable-pre-post-scripts=false", "run", wantName}) {
			t.Fatalf("command[%d] = %#v", index, command)
		}
		if command.CWD != "." || command.ManifestPath != "package.json" || command.ScriptName != wantName || command.ScriptBody == "" {
			t.Fatalf("autodetect metadata[%d] = %#v", index, command)
		}
	}
}

func TestResolveAutodetectDisablesImplicitLifecycleScripts(t *testing.T) {
	tests := []struct {
		name        string
		manager     string
		wantArgv    []string
		wantCommand bool
	}{
		{name: "npm", manager: "npm@10", wantArgv: []string{"npm", "run", "--ignore-scripts", "test"}, wantCommand: true},
		{name: "pnpm", manager: "pnpm@9", wantArgv: []string{"pnpm", "--config.enable-pre-post-scripts=false", "run", "test"}, wantCommand: true},
		{name: "yarn", manager: "yarn@1", wantCommand: false},
		{name: "bun", manager: "bun@1", wantCommand: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, filepath.Join(root, "package.json"), `{"packageManager":"`+test.manager+`","scripts":{"pretest":"unapproved","test":"vitest","posttest":"unapproved"}}`)
			definition, err := Resolve(root, config.Values{Autodetect: true})
			if err != nil {
				t.Fatal(err)
			}
			if !test.wantCommand {
				if definition != nil {
					t.Fatalf("definition = %#v, want nil", definition)
				}
				return
			}
			if definition == nil || len(definition.Commands) != 1 || !reflect.DeepEqual(definition.Commands[0].Argv, test.wantArgv) {
				t.Fatalf("definition = %#v, want argv %#v", definition, test.wantArgv)
			}
		})
	}
}

func TestResolveReturnsNoneWithoutCommands(t *testing.T) {
	tests := []struct {
		name       string
		autodetect bool
		manifest   string
		markers    []string
	}{
		{name: "disabled", autodetect: false},
		{name: "missing manifest", autodetect: true},
		{name: "no supported scripts", autodetect: true, manifest: `{"packageManager":"npm@10","scripts":{"format":"prettier ."}}`},
		{name: "unknown manager", autodetect: true, manifest: `{"scripts":{"test":"test"}}`},
		{name: "ambiguous managers", autodetect: true, manifest: `{"scripts":{"test":"test"}}`, markers: []string{"package-lock.json", "yarn.lock"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if test.manifest != "" {
				writeFile(t, filepath.Join(root, "package.json"), test.manifest)
			}
			for _, marker := range test.markers {
				writeFile(t, filepath.Join(root, marker), "")
			}
			definition, err := Resolve(root, config.Values{Autodetect: test.autodetect})
			if err != nil || definition != nil {
				t.Fatalf("Resolve() = %#v, %v", definition, err)
			}
		})
	}
}

func TestResolveRejectsCWDOutsideCanonicalRoot(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "outside")); err != nil {
		t.Fatal(err)
	}
	_, err := Resolve(root, config.Values{
		CommandsSet: true,
		Commands:    []config.VerificationCommand{{Name: "unsafe", Argv: []string{"true"}, CWD: "outside"}},
	})
	if err == nil || !strings.Contains(err.Error(), "inside the repo root") {
		t.Fatalf("Resolve() error = %v", err)
	}
}

func TestHashIncludesOnlyCanonicalDefinition(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "package.json"), `{"name":"one","packageManager":"npm@10","scripts":{"lint":"eslint .","unused":"one"}}`)
	base, err := Resolve(root, config.Values{Autodetect: true})
	if err != nil {
		t.Fatal(err)
	}
	baseHash := mustHash(t, base)

	writeFile(t, filepath.Join(root, "package.json"), `{"name":"two","private":true,"packageManager":"npm@10","scripts":{"lint":"eslint .","unused":"two"}}`)
	writeFile(t, filepath.Join(root, "package-lock.json"), `{"lockfileVersion":3}`)
	writeFile(t, filepath.Join(root, "source.js"), "changed")
	unchanged, err := Resolve(root, config.Values{Autodetect: true, Model: "unrelated"})
	if err != nil {
		t.Fatal(err)
	}
	if got := mustHash(t, unchanged); got != baseHash {
		t.Fatalf("unrelated changes altered hash: %s != %s", got, baseHash)
	}

	writeFile(t, filepath.Join(root, "package.json"), `{"packageManager":"npm@10","scripts":{"lint":"eslint --max-warnings=0 ."}}`)
	changed, err := Resolve(root, config.Values{Autodetect: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := mustHash(t, changed); got == baseHash {
		t.Fatal("adopted script body did not alter hash")
	}
}

func TestHashChangesWithEveryAdoptedDefinitionField(t *testing.T) {
	base := Definition{
		SchemaVersion: 1,
		SourceType:    SourcePackageJSONAutodetect,
		Commands: []Command{
			{
				Name:         "lint",
				CWD:          ".",
				Argv:         []string{"npm", "run", "--ignore-scripts", "lint"},
				ManifestPath: "package.json",
				ScriptName:   "lint",
				ScriptBody:   "eslint .",
			},
			{
				Name:         "test",
				CWD:          ".",
				Argv:         []string{"npm", "run", "--ignore-scripts", "test"},
				ManifestPath: "package.json",
				ScriptName:   "test",
				ScriptBody:   "vitest",
			},
		},
	}
	baseHash, err := base.Hash()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*Definition)
	}{
		{"command name", func(d *Definition) { d.Commands[0].Name = "check" }},
		{"cwd", func(d *Definition) { d.Commands[0].CWD = "web" }},
		{"argv", func(d *Definition) { d.Commands[0].Argv[2] = "check" }},
		{"manifest path", func(d *Definition) { d.Commands[0].ManifestPath = "web/package.json" }},
		{"script name", func(d *Definition) { d.Commands[0].ScriptName = "check" }},
		{"script body", func(d *Definition) { d.Commands[0].ScriptBody = "eslint src" }},
		{"command order", func(d *Definition) { d.Commands[0], d.Commands[1] = d.Commands[1], d.Commands[0] }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changed := cloneDefinition(base)
			test.mutate(&changed)
			hash, err := changed.Hash()
			if err != nil {
				t.Fatal(err)
			}
			if hash == baseHash {
				t.Fatalf("%s did not alter hash", test.name)
			}
		})
	}
	repoDefinition := Definition{
		SchemaVersion: 1,
		SourceType:    SourceRepoConfig,
		Commands: []Command{
			{Name: "lint", CWD: ".", Argv: []string{"npm", "run", "lint"}},
			{Name: "test", CWD: ".", Argv: []string{"npm", "run", "test"}},
		},
	}
	if repoHash, err := repoDefinition.Hash(); err != nil || repoHash == baseHash {
		t.Fatalf("source type did not alter hash: hash=%s error=%v", repoHash, err)
	}
}

func TestCanonicalJSONIncludesEmptyAutodetectScriptBodyOnlyForAutodetect(t *testing.T) {
	autodetect := Definition{
		SchemaVersion: 1,
		SourceType:    SourcePackageJSONAutodetect,
		Commands: []Command{{
			Name: "test", CWD: ".", Argv: []string{"npm", "run", "--ignore-scripts", "test"},
			ManifestPath: "package.json", ScriptName: "test", ScriptBody: "",
		}},
	}
	canonical, err := autodetect.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(canonical), `"script_body":""`) {
		t.Fatalf("canonical JSON = %s", canonical)
	}

	repo := Definition{
		SchemaVersion: 1,
		SourceType:    SourceRepoConfig,
		Commands:      []Command{{Name: "test", CWD: ".", Argv: []string{"go", "test"}}},
	}
	canonical, err = repo.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(canonical), "manifest_path") || strings.Contains(string(canonical), "script_name") || strings.Contains(string(canonical), "script_body") {
		t.Fatalf("repo canonical JSON = %s", canonical)
	}
}

func TestHashIsIndependentOfCanonicalRepositoryScope(t *testing.T) {
	rootA, rootB := t.TempDir(), t.TempDir()
	for _, root := range []string{rootA, rootB} {
		writeFile(t, filepath.Join(root, "package.json"), `{"packageManager":"yarn@4.0.0","scripts":{"test":"vitest"}}`)
	}
	definitionA, err := Resolve(rootA, config.Values{Autodetect: true})
	if err != nil {
		t.Fatal(err)
	}
	definitionB, err := Resolve(rootB, config.Values{Autodetect: true})
	if err != nil {
		t.Fatal(err)
	}
	if mustHash(t, definitionA) != mustHash(t, definitionB) {
		t.Fatal("repository scope leaked into verification hash")
	}
}

func TestPackageManagerChangeAltersFinalArgvAndHash(t *testing.T) {
	npmRoot, pnpmRoot := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(npmRoot, "package.json"), `{"packageManager":"npm@10","scripts":{"test":"vitest"}}`)
	writeFile(t, filepath.Join(pnpmRoot, "package.json"), `{"packageManager":"pnpm@9","scripts":{"test":"vitest"}}`)
	npmDefinition, err := Resolve(npmRoot, config.Values{Autodetect: true})
	if err != nil {
		t.Fatal(err)
	}
	pnpmDefinition, err := Resolve(pnpmRoot, config.Values{Autodetect: true})
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(npmDefinition.Commands[0].Argv, pnpmDefinition.Commands[0].Argv) {
		t.Fatal("package manager did not alter final argv")
	}
	if mustHash(t, npmDefinition) == mustHash(t, pnpmDefinition) {
		t.Fatal("package manager argv change did not alter hash")
	}
}

func TestSymlinkAndRealRootProduceSameDefinition(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "package.json"), `{"packageManager":"npm@10","scripts":{"test":"npm test"}}`)
	link := filepath.Join(t.TempDir(), "repo-link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	realDefinition, err := Resolve(root, config.Values{Autodetect: true})
	if err != nil {
		t.Fatal(err)
	}
	linkDefinition, err := Resolve(link, config.Values{Autodetect: true})
	if err != nil {
		t.Fatal(err)
	}
	if mustHash(t, realDefinition) != mustHash(t, linkDefinition) {
		t.Fatal("symlink changed verification definition")
	}
}

func mustHash(t *testing.T, definition *Definition) string {
	t.Helper()
	if definition == nil {
		t.Fatal("definition is nil")
	}
	hash, err := definition.Hash()
	if err != nil {
		t.Fatal(err)
	}
	return hash
}

func cloneDefinition(definition Definition) Definition {
	cloned := definition
	cloned.Commands = make([]Command, len(definition.Commands))
	for index, command := range definition.Commands {
		cloned.Commands[index] = command
		cloned.Commands[index].Argv = append([]string{}, command.Argv...)
	}
	return cloned
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}
