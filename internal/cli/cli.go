package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/natsuki0413/commiter-cli/internal/config"
	"github.com/natsuki0413/commiter-cli/internal/exitcode"
	"github.com/natsuki0413/commiter-cli/internal/ollama"
	"github.com/natsuki0413/commiter-cli/internal/output"
	"github.com/natsuki0413/commiter-cli/internal/repository"
	"github.com/natsuki0413/commiter-cli/internal/trust"
)

var Version = "dev"

var lookPath = exec.LookPath
var commandFactory = exec.Command

type options struct {
	json            bool
	dryRun          bool
	noPush          bool
	noConfirmCommit bool
	noConfirmPush   bool
	recordMetrics   bool
	language        *string
	model           *string
	command         string
	args            []string
	pathspecs       []string
	help            bool
}

var commands = map[string]bool{
	"setup": true, "doctor": true, "config": true, "trust": true, "version": true,
}

func Run(args []string, stdout, stderr io.Writer) int {
	opts, err := parse(args)
	printer := output.New(stdout, stderr, opts.json)
	if err != nil {
		return fail(printer, exitcode.New(exitcode.Usage, err.Error()))
	}
	if opts.help {
		if err := printHelp(printer); err != nil {
			return fail(printer, exitcode.New(exitcode.Internal, "cannot write output"))
		}
		return exitcode.Success
	}
	if err := validateJSONMode(opts); err != nil {
		return fail(printer, err)
	}

	switch opts.command {
	case "version":
		return runVersion(opts, printer)
	case "config":
		return runConfig(opts, printer)
	case "trust":
		return runTrust(opts, printer)
	case "setup":
		return runSetup(opts.args, printer)
	case "doctor":
		return runDoctor(opts.args, printer)
	case "":
		return runMain(opts, printer)
	default:
		return fail(printer, exitcode.New(exitcode.Usage, "unknown command"))
	}
}

func runSetup(args []string, printer *output.Printer) int {
	if len(args) > 1 || (len(args) == 1 && args[0] != "--update-model") {
		return fail(printer, exitcode.New(exitcode.Usage, "setup accepts only --update-model"))
	}
	update := len(args) == 1
	root, err := repository.Root()
	if err != nil {
		return fail(printer, exitcode.New(exitcode.Usage, err.Error()))
	}
	paths, err := config.DefaultPaths(root)
	if err != nil {
		return fail(printer, exitcode.New(exitcode.Usage, err.Error()))
	}
	effective, err := config.Resolve(paths.GlobalConfig, paths.RepoConfig, root, config.CLIOverrides{})
	if err != nil {
		return fail(printer, exitcode.New(exitcode.Usage, err.Error()))
	}
	if _, err := lookPath("ollama"); err != nil {
		if _, brewErr := lookPath("brew"); brewErr != nil {
			return fail(printer, exitcode.New(exitcode.LLM, "Ollama is not installed and Homebrew is unavailable"))
		}
		if !confirm("Ollama is not installed. Install it with Homebrew? [y/N] ") {
			return fail(printer, exitcode.New(exitcode.Canceled, "setup canceled"))
		}
		command := commandFactory("brew", "install", "ollama")
		if err := command.Run(); err != nil {
			return fail(printer, exitcode.New(exitcode.LLM, "Ollama installation failed"))
		}
	}
	client, err := ollama.New(effective.Values)
	if err != nil {
		return fail(printer, err)
	}
	probeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	probeErr := client.Compatibility(probeCtx)
	cancel()
	if probeErr != nil && !confirm("Ollama daemon is stopped. Start it temporarily? [y/N] ") {
		return fail(printer, exitcode.New(exitcode.Canceled, "setup canceled"))
	}
	runtime, err := ollama.OpenForSetup(context.Background(), effective.Values)
	if err != nil {
		return fail(printer, err)
	}
	defer runtime.Close()
	present, err := runtime.Client.HasModel(context.Background())
	if err != nil {
		return fail(printer, err)
	}
	if present && !update {
		return finishSetup(printer, "Ollama is ready; model is already installed")
	}
	if !present {
		update = true
	}
	if update {
		if !confirm(fmt.Sprintf("Pull/update model %s? [y/N] ", effective.Values.Model)) {
			return finishSetup(printer, "setup canceled; model was not changed")
		}
		if err := runtime.Client.Pull(context.Background()); err != nil {
			return fail(printer, err)
		}
		return finishSetup(printer, "Ollama setup completed")
	}
	return finishSetup(printer, "Ollama is ready")
}

func runDoctor(args []string, printer *output.Printer) int {
	if len(args) != 0 {
		return fail(printer, exitcode.New(exitcode.Usage, "doctor does not accept arguments"))
	}
	checks := map[string]any{}
	_, ollamaErr := lookPath("ollama")
	checks["ollama_binary"] = check(ollamaErr == nil, message(ollamaErr, "Ollama executable available"))
	root, rootErr := repository.Root()
	checks["git"] = check(rootErr == nil, message(rootErr, "repository detected"))
	if rootErr == nil {
		paths, err := config.DefaultPaths(root)
		if err == nil {
			effective, resolveErr := config.Resolve(paths.GlobalConfig, paths.RepoConfig, root, config.CLIOverrides{})
			checks["config"] = check(resolveErr == nil, message(resolveErr, "configuration valid"))
			checks["trust"] = checkTrust(paths.StateDir, root)
			if resolveErr == nil {
				ollamaChecks := doctorOllama(effective.Values)
				checks["ollama"] = ollamaChecks["ollama"]
				checks["structured_output"] = ollamaChecks["structured_output"]
				checks["thinking"] = ollamaChecks["thinking"]
			}
		} else {
			checks["config"] = check(false, "configuration paths unavailable")
		}
	}
	checks["git_identity"] = doctorGitIdentity()
	if checks["structured_output"] == nil {
		checks["structured_output"] = check(false, "Ollama compatibility could not be verified")
	}
	if checks["thinking"] == nil {
		checks["thinking"] = check(false, "Ollama compatibility could not be verified")
	}
	ok := true
	for _, value := range checks {
		if item, yes := value.(map[string]any); yes && item["ok"] == false {
			ok = false
		}
	}
	if printer.JSON() {
		if err := printer.Value(map[string]any{"doctor": checks, "ok": ok, "read_only": true}); err != nil {
			return fail(printer, exitcode.New(exitcode.Internal, "cannot write output"))
		}
	} else {
		lines := []string{"Doctor (read-only)"}
		keys := make([]string, 0, len(checks))
		for key := range checks {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			item := checks[key].(map[string]any)
			lines = append(lines, fmt.Sprintf("%s: %s", key, item["message"]))
		}
		if err := printer.Lines(lines...); err != nil {
			return fail(printer, exitcode.New(exitcode.Internal, "cannot write output"))
		}
	}
	if !ok {
		return exitcode.LLM
	}
	return exitcode.Success
}

var confirmFunc = confirmFromStdin

func confirm(prompt string) bool { return confirmFunc(prompt) }

func confirmFromStdin(prompt string) bool {
	fmt.Fprint(os.Stderr, prompt)
	answer := strings.ToLower(strings.TrimSpace(readLine()))
	return answer == "y" || answer == "yes"
}
func readLine() string                         { return readLineFrom(bufio.NewReader(os.Stdin)) }
func readLineFrom(reader *bufio.Reader) string { line, _ := reader.ReadString('\n'); return line }
func finishSetup(printer *output.Printer, message string) int {
	if err := printer.Lines(message); err != nil {
		return fail(printer, exitcode.New(exitcode.Internal, "cannot write output"))
	}
	return exitcode.Success
}
func check(ok bool, message string) map[string]any {
	return map[string]any{"ok": ok, "message": message}
}
func message(err error, success string) string {
	if err == nil {
		return success
	}
	return err.Error()
}
func doctorOllama(values config.Values) map[string]map[string]any {
	client, err := ollama.New(values)
	if err != nil {
		return doctorOllamaFailure(err.Error())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.Compatibility(ctx); err != nil {
		return doctorOllamaFailure(err.Error())
	}
	present, err := client.HasModel(ctx)
	if err != nil {
		return doctorOllamaFailure(err.Error())
	}
	if !present {
		return map[string]map[string]any{
			"ollama":            check(false, "configured Ollama model is not installed"),
			"structured_output": check(true, "Ollama compatibility verified; JSON Schema output enabled"),
			"thinking":          check(true, "Ollama compatibility verified; thinking disabled"),
		}
	}
	return map[string]map[string]any{
		"ollama":            check(true, "loopback API and configured model are ready"),
		"structured_output": check(true, "Ollama compatibility verified; JSON Schema output enabled"),
		"thinking":          check(true, "Ollama compatibility verified; thinking disabled"),
	}
}

func doctorOllamaFailure(message string) map[string]map[string]any {
	return map[string]map[string]any{
		"ollama":            check(false, message),
		"structured_output": check(false, "Ollama compatibility could not be verified"),
		"thinking":          check(false, "Ollama compatibility could not be verified"),
	}
}
func doctorGitIdentity() map[string]any {
	name := exec.Command("git", "config", "--get", "user.name")
	email := exec.Command("git", "config", "--get", "user.email")
	nerr, eerr := name.Run(), email.Run()
	if nerr != nil || eerr != nil {
		return check(false, "Git user.name and user.email must be configured")
	}
	return check(true, "Git identity configured")
}
func checkTrust(stateDir, root string) map[string]any {
	entries, err := trust.New(stateDir).List()
	if err != nil {
		return check(false, err.Error())
	}
	for _, entry := range entries {
		if entry.RepoPath == root {
			return check(true, "trust record present")
		}
	}
	return check(true, "no trust record (approval will be requested when needed)")
}

func parse(args []string) (options, error) {
	var opts options
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			opts.pathspecs = append(opts.pathspecs, args[i+1:]...)
			break
		}
		if commands[arg] {
			opts.command = arg
			opts.args = append([]string{}, args[i+1:]...)
			break
		}
		switch arg {
		case "-h", "--help":
			opts.help = true
		case "--json":
			opts.json = true
		case "--dry-run":
			opts.dryRun = true
		case "--no-push":
			opts.noPush = true
		case "--no-confirm-commit":
			opts.noConfirmCommit = true
		case "--no-confirm-push":
			opts.noConfirmPush = true
		case "--record-metrics":
			opts.recordMetrics = true
		case "--language", "--model":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("%s requires a value", arg)
			}
			i++
			if arg == "--language" {
				value := args[i]
				opts.language = &value
			} else {
				value := args[i]
				opts.model = &value
			}
		default:
			if strings.HasPrefix(arg, "--language=") {
				value := strings.TrimPrefix(arg, "--language=")
				opts.language = &value
			} else if strings.HasPrefix(arg, "--model=") {
				value := strings.TrimPrefix(arg, "--model=")
				opts.model = &value
			} else if strings.HasPrefix(arg, "-") {
				return opts, fmt.Errorf("unknown flag")
			} else {
				opts.pathspecs = append(opts.pathspecs, arg)
			}
		}
	}
	return opts, nil
}

func validateJSONMode(opts options) error {
	if !opts.json {
		return nil
	}
	if opts.command == "version" {
		return nil
	}
	if opts.command == "doctor" {
		return nil
	}
	if opts.command == "config" && len(opts.args) > 0 && (opts.args[0] == "show" || opts.args[0] == "path") {
		return nil
	}
	if opts.command == "trust" && len(opts.args) > 0 && opts.args[0] == "list" {
		return nil
	}
	if opts.command == "" && opts.dryRun {
		return nil
	}
	return exitcode.New(exitcode.Usage, "--json requires --dry-run or a read-only command")
}

func runVersion(opts options, printer *output.Printer) int {
	if len(opts.args) != 0 {
		return fail(printer, exitcode.New(exitcode.Usage, "version does not accept arguments"))
	}
	var err error
	if printer.JSON() {
		err = printer.Value(map[string]string{"version": Version})
	} else {
		err = printer.Lines("commiter " + Version)
	}
	if err != nil {
		return fail(printer, exitcode.New(exitcode.Internal, "cannot write output"))
	}
	return exitcode.Success
}

func runConfig(opts options, printer *output.Printer) int {
	if len(opts.args) == 0 {
		return fail(printer, exitcode.New(exitcode.Usage, "config requires init, show, or path"))
	}
	subcommand, args := opts.args[0], opts.args[1:]
	switch subcommand {
	case "init":
		return configInit(args, printer)
	case "show":
		return configShow(args, opts, printer)
	case "path":
		return configPath(args, printer)
	default:
		return fail(printer, exitcode.New(exitcode.Usage, "unknown config command"))
	}
}

func configInit(args []string, printer *output.Printer) int {
	target, err := exactTarget(args)
	if err != nil {
		return fail(printer, exitcode.New(exitcode.Usage, err.Error()))
	}
	root := ""
	if target == "repo" {
		root, err = repository.Root()
		if err != nil {
			return fail(printer, exitcode.New(exitcode.Usage, err.Error()))
		}
	}
	paths, err := config.DefaultPaths(root)
	if err != nil {
		return fail(printer, exitcode.New(exitcode.Usage, err.Error()))
	}
	path, template, mode := paths.GlobalConfig, config.GlobalTemplate, os.FileMode(0o600)
	if target == "repo" {
		path, template, mode = paths.RepoConfig, config.RepoTemplate, 0o644
	}
	if err := config.Init(path, template, mode); err != nil {
		return fail(printer, exitcode.New(exitcode.Usage, err.Error()))
	}
	if err := printer.Lines("created " + path); err != nil {
		return fail(printer, exitcode.New(exitcode.Internal, "cannot write output"))
	}
	return exitcode.Success
}

func configShow(args []string, opts options, printer *output.Printer) int {
	if len(args) > 1 || (len(args) == 1 && args[0] != "--effective") {
		return fail(printer, exitcode.New(exitcode.Usage, "config show accepts only --effective"))
	}
	root, _ := repository.Root()
	paths, err := config.DefaultPaths(root)
	if err != nil {
		return fail(printer, exitcode.New(exitcode.Usage, err.Error()))
	}
	effective, err := config.Resolve(paths.GlobalConfig, paths.RepoConfig, root, overrides(opts))
	if err != nil {
		return fail(printer, exitcode.New(exitcode.Usage, err.Error()))
	}
	entries := effective.Entries()
	if printer.JSON() {
		if err := printer.Value(map[string]any{"config": entries}); err != nil {
			return fail(printer, exitcode.New(exitcode.Internal, "cannot write output"))
		}
		return exitcode.Success
	}
	keys := config.SortedKeys()
	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		entry := entries[key]
		lines = append(lines, fmt.Sprintf("%s = %v (source: %s)", key, entry.Value, entry.Source))
	}
	if err := printer.Lines(lines...); err != nil {
		return fail(printer, exitcode.New(exitcode.Internal, "cannot write output"))
	}
	return exitcode.Success
}

func configPath(args []string, printer *output.Printer) int {
	target, err := exactTarget(args)
	if err != nil {
		return fail(printer, exitcode.New(exitcode.Usage, err.Error()))
	}
	root := ""
	if target == "repo" {
		root, err = repository.Root()
		if err != nil {
			return fail(printer, exitcode.New(exitcode.Usage, err.Error()))
		}
	}
	paths, err := config.DefaultPaths(root)
	if err != nil {
		return fail(printer, exitcode.New(exitcode.Usage, err.Error()))
	}
	path := paths.GlobalConfig
	if target == "repo" {
		path = paths.RepoConfig
	}
	if printer.JSON() {
		err = printer.Value(map[string]string{"scope": target, "path": path})
	} else {
		err = printer.Lines(path)
	}
	if err != nil {
		return fail(printer, exitcode.New(exitcode.Internal, "cannot write output"))
	}
	return exitcode.Success
}

func runTrust(opts options, printer *output.Printer) int {
	if len(opts.args) == 0 {
		return fail(printer, exitcode.New(exitcode.Usage, "trust requires list or revoke"))
	}
	paths, err := config.DefaultPaths("")
	if err != nil {
		return fail(printer, exitcode.New(exitcode.Usage, err.Error()))
	}
	store := trust.New(paths.StateDir)
	switch opts.args[0] {
	case "list":
		if len(opts.args) != 1 {
			return fail(printer, exitcode.New(exitcode.Usage, "trust list does not accept arguments"))
		}
		entries, err := store.List()
		if err != nil {
			return fail(printer, exitcode.New(exitcode.Internal, err.Error()))
		}
		if printer.JSON() {
			err = printer.Value(map[string]any{"trust": entries})
		} else if len(entries) == 0 {
			err = printer.Lines("No trusted verification definitions.")
		} else {
			lines := make([]string, 0, len(entries))
			for _, entry := range entries {
				argv, err := json.Marshal(entry.Commands)
				if err != nil {
					return fail(printer, exitcode.New(exitcode.Internal, "cannot format trust entry"))
				}
				lines = append(lines, fmt.Sprintf("%s hash=%s source=%s argv=%s", entry.RepoPath, entry.DefinitionHash, entry.SourceType, argv))
			}
			err = printer.Lines(lines...)
		}
		if err != nil {
			return fail(printer, exitcode.New(exitcode.Internal, "cannot write output"))
		}
		return exitcode.Success
	case "revoke":
		if len(opts.args) != 2 {
			return fail(printer, exitcode.New(exitcode.Usage, "trust revoke requires one repository path"))
		}
		revoked, err := store.Revoke(opts.args[1])
		if err != nil {
			return fail(printer, exitcode.New(exitcode.Internal, err.Error()))
		}
		message := "trust entry not found"
		if revoked {
			message = "trust entry revoked"
		}
		if err := printer.Lines(message); err != nil {
			return fail(printer, exitcode.New(exitcode.Internal, "cannot write output"))
		}
		return exitcode.Success
	default:
		return fail(printer, exitcode.New(exitcode.Usage, "unknown trust command"))
	}
}

func runMain(opts options, printer *output.Printer) int {
	root, err := repository.Root()
	if err != nil {
		return fail(printer, exitcode.New(exitcode.Usage, err.Error()))
	}
	paths, err := config.DefaultPaths(root)
	if err != nil {
		return fail(printer, exitcode.New(exitcode.Usage, err.Error()))
	}
	effective, err := config.Resolve(paths.GlobalConfig, paths.RepoConfig, root, overrides(opts))
	if err != nil {
		return fail(printer, exitcode.New(exitcode.Usage, err.Error()))
	}
	return runCollection(opts, root, effective.Values, printer)
}

func overrides(opts options) config.CLIOverrides {
	overrides := config.CLIOverrides{Language: opts.language, Model: opts.model}
	if opts.noConfirmCommit {
		value := false
		overrides.CommitConfirm = &value
	}
	if opts.noPush {
		value := false
		overrides.PushEnabled = &value
	}
	if opts.noConfirmPush {
		value := false
		overrides.PushConfirm = &value
	}
	if opts.recordMetrics {
		value := true
		overrides.MetricsPersist = &value
	}
	return overrides
}

func exactTarget(args []string) (string, error) {
	if len(args) != 1 || (args[0] != "--global" && args[0] != "--repo") {
		return "", fmt.Errorf("exactly one of --global or --repo is required")
	}
	return strings.TrimPrefix(args[0], "--"), nil
}

func printHelp(printer *output.Printer) error {
	lines := []string{
		"Usage: commiter [flags] [--] [pathspec...]",
		"Commands: setup, doctor, config, trust, version",
		"Flags: --dry-run --no-push --no-confirm-commit --no-confirm-push",
		"       --language en|ja --model NAME --record-metrics --json",
	}
	if printer.JSON() {
		sort.Strings(lines)
		return printer.Value(map[string]any{"help": lines})
	}
	return printer.Lines(lines...)
}

func fail(printer *output.Printer, err error) int {
	code := exitcode.Code(err)
	printer.Error(err.Error(), code)
	return code
}
