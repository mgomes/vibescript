package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/urfave/cli/v3"
)

var (
	cliReader    = os.Stdin
	cliWriter    = os.Stdout
	cliErrWriter = os.Stderr
)

const rawCommandArgsMetadataKey = "vibes.raw-command-args"

type cliFlagKind struct {
	boolean    bool
	takesValue bool
	typeName   string
}

func runCLI(args []string) error {
	return runCLIContext(context.Background(), args)
}

func runCLIContext(ctx context.Context, args []string) error {
	return runCLIContextWithIO(ctx, args, cliReader, cliWriter, cliErrWriter)
}

func runCLIContextWithIO(ctx context.Context, args []string, reader io.Reader, writer, errWriter io.Writer) error {
	if len(args) == 0 {
		args = []string{"vibes"}
	}

	command := newCLI()
	if len(args) > 1 {
		switch {
		case args[1] == "-h" || args[1] == "--help":
			args = args[:2]
		case args[1] == "--" || args[1] != strings.TrimSpace(args[1]) || unsupportedRootHelp(args[1]):
			command = newCLIWithRootError(fmt.Errorf("unknown command %q", args[1]))
			args = args[:1]
		case command.Command(args[1]) == nil:
			// Root dispatch is decided entirely by the first token. Truncating an
			// unknown command's tail prevents urfave's parser from resuming at a
			// later empty argument or help flag and changing that decision.
			args = args[:2]
		}
	}
	command.Reader = reader
	command.Writer = writer
	command.ErrWriter = errWriter
	selectedCommand, selectedArgs := commandAndArgs(command, args)
	rememberCommandArgs(selectedCommand, selectedArgs)
	if selectedCommand != command {
		normalizedArgs, err := normalizeCommandArgs(selectedCommand, selectedArgs)
		if err != nil {
			return err
		}
		args = append(append([]string(nil), args[:2]...), normalizedArgs...)
	}
	return command.Run(ctx, args)
}

func newCLI() *cli.Command {
	return newCLIWithRootError(nil)
}

func newCLIWithRootError(rootError error) *cli.Command {
	stopAfterCommand := 1
	return configureCLICommand(&cli.Command{
		Usage:        "run Vibescript programs and development tools",
		HideVersion:  true,
		StopOnNthArg: &stopAfterCommand,
		Commands: []*cli.Command{
			newRunCommand(),
			newCheckCommand(),
			newFmtCommand(),
			newAnalyzeCommand(),
			newTestCommand(),
			newLSPCommand(),
			newREPLCommand(),
			newHelpCommand(),
		},
		Action: func(_ context.Context, command *cli.Command) error {
			if err := showRootUsageOnError(command); err != nil {
				return fmt.Errorf("show command usage: %w", err)
			}
			if rootError != nil {
				return rootError
			}
			if command.NArg() == 0 {
				return errors.New("command required")
			}
			return fmt.Errorf("unknown command %q", command.Args().First())
		},
	})
}

func newHelpCommand() *cli.Command {
	stopAfterTopic := 1
	return configureCLICommand(&cli.Command{
		Name:         "help",
		Aliases:      []string{"h"},
		Usage:        "Shows a list of commands or help for one command",
		ArgsUsage:    "[command]",
		StopOnNthArg: &stopAfterTopic,
		Action: func(ctx context.Context, command *cli.Command) error {
			args := commandPositionalArgs(command)
			switch len(args) {
			case 0:
				return cli.ShowRootCommandHelp(command.Root())
			case 1:
				return cli.ShowCommandHelp(ctx, command.Root(), args[0])
			default:
				return errors.New("vibes help: expected at most one command")
			}
		},
	})
}

func configureCLICommand(command *cli.Command) *cli.Command {
	command.HideHelpCommand = true
	command.OnUsageError = func(_ context.Context, _ *cli.Command, err error, _ bool) error {
		return cli.Exit(err, 1)
	}
	command.ExitErrHandler = func(context.Context, *cli.Command, error) {
		// main owns process error rendering and exit status.
	}
	return command
}

func runStandaloneCommand(ctx context.Context, command *cli.Command, args []string) error {
	rememberCommandArgs(command, args)
	normalizedArgs, err := normalizeCommandArgs(command, args)
	if err != nil {
		return err
	}
	command.Reader = cliReader
	command.Writer = cliWriter
	command.ErrWriter = cliErrWriter
	argv := append([]string{command.Name}, normalizedArgs...)
	return command.Run(ctx, argv)
}

func commandAndArgs(root *cli.Command, args []string) (*cli.Command, []string) {
	if len(args) < 2 {
		return root, nil
	}
	if command := root.Command(args[1]); command != nil {
		return command, args[2:]
	}
	return root, args[1:]
}

func commandFlagKinds(command *cli.Command) map[string]cliFlagKind {
	flags := append([]cli.Flag(nil), command.Flags...)
	if cli.HelpFlag != nil && !command.HideHelp {
		flags = append(flags, cli.HelpFlag)
	}
	kinds := make(map[string]cliFlagKind, len(flags))
	for _, flag := range flags {
		documented, ok := flag.(cli.DocGenerationFlag)
		if !ok {
			continue
		}
		typeName := documented.TypeName()
		kind := cliFlagKind{
			boolean:    typeName == "bool",
			takesValue: documented.TakesValue(),
			typeName:   typeName,
		}
		for _, name := range flag.Names() {
			kinds[name] = kind
		}
	}
	return kinds
}

func normalizeCommandArgs(command *cli.Command, args []string) ([]string, error) {
	// The standard flag package stops parsing at the first positional argument
	// and preserves every later token verbatim. urfave/cli trims and classifies
	// several tokens before applying StopOnNthArg, so feed it a parser-safe copy
	// while commandPositionalArgs exposes the untouched arguments to actions.
	kinds := commandFlagKinds(command)
	normalized := make([]string, 0, len(args)+1)
	for i := 0; i < len(args); i++ {
		argument := args[i]
		switch {
		case argument == "--":
			return append(normalized, args[i:]...), nil
		case argument == "-" || argument == "" || !strings.HasPrefix(argument, "-"):
			if strings.TrimSpace(argument) == "" || strings.HasPrefix(strings.TrimSpace(argument), "-") {
				argument = "\x00" + argument
			}
			normalized = append(normalized, argument)
			return append(normalized, normalizeOpaqueCommandArgs(args[i+1:])...), nil
		}

		nameValue := strings.TrimPrefix(argument, "-")
		nameValue = strings.TrimPrefix(nameValue, "-")
		if strings.HasPrefix(nameValue, "-") || strings.HasPrefix(nameValue, "=") {
			return nil, fmt.Errorf("bad flag syntax: %s", argument)
		}
		name, value, hasValue := strings.Cut(nameValue, "=")
		kind, ok := kinds[name]
		if !ok {
			return nil, fmt.Errorf("flag provided but not defined: -%s", name)
		}

		if kind.boolean {
			if name == "help" || name == "h" {
				if hasValue {
					return nil, errors.New("help flag does not accept a value")
				}
				return append(normalized, argument), nil
			}
			if hasValue {
				if _, err := strconv.ParseBool(value); err != nil {
					return nil, fmt.Errorf("invalid boolean value %q for -%s: parse error", value, name)
				}
			}
			normalized = append(normalized, argument)
			continue
		}
		if kind.takesValue && !hasValue && i+1 >= len(args) {
			return nil, fmt.Errorf("flag needs an argument: -%s", name)
		}
		if kind.typeName == "int" {
			switch {
			case hasValue:
				if err := validateIntFlagValue(name, value); err != nil {
					return nil, err
				}
			case i+1 < len(args):
				if err := validateIntFlagValue(name, args[i+1]); err != nil {
					return nil, err
				}
			}
		}

		if hasValue && argument != strings.TrimSpace(argument) {
			prefix := "-"
			if strings.HasPrefix(argument, "--") {
				prefix = "--"
			}
			normalized = append(normalized, prefix+name, value)
			continue
		}

		normalized = append(normalized, argument)
		if kind.takesValue && !hasValue && i+1 < len(args) {
			i++
			normalized = append(normalized, args[i])
		}
	}
	return normalized, nil
}

func validateIntFlagValue(name, value string) error {
	if _, err := strconv.ParseInt(value, 0, strconv.IntSize); err != nil {
		reason := err.Error()
		switch {
		case errors.Is(err, strconv.ErrSyntax):
			reason = "parse error"
		case errors.Is(err, strconv.ErrRange):
			reason = "value out of range"
		}
		return fmt.Errorf("invalid value %q for flag -%s: %s", value, name, reason)
	}
	return nil
}

func unsupportedRootHelp(argument string) bool {
	if argument == "-help" || argument == "--h" {
		return true
	}
	for _, prefix := range []string{"-h=", "--h=", "-help=", "--help="} {
		if strings.HasPrefix(argument, prefix) {
			return true
		}
	}
	return false
}

func normalizeOpaqueCommandArgs(args []string) []string {
	normalized := append([]string(nil), args...)
	for i, argument := range normalized {
		if strings.TrimSpace(argument) == "" {
			normalized[i] = "\x00" + argument
		}
	}
	return normalized
}

func rememberCommandArgs(command *cli.Command, args []string) {
	if command.Metadata == nil {
		command.Metadata = make(map[string]any)
	}
	command.Metadata[rawCommandArgsMetadataKey] = append([]string(nil), args...)
}

func commandPositionalArgs(command *cli.Command) []string {
	rawArgs, ok := command.Metadata[rawCommandArgsMetadataKey].([]string)
	if !ok {
		return command.Args().Slice()
	}

	kinds := commandFlagKinds(command)
	for i := 0; i < len(rawArgs); i++ {
		argument := rawArgs[i]
		if argument == "--" {
			return append([]string(nil), rawArgs[i+1:]...)
		}
		if argument == "-" || argument == "" || !strings.HasPrefix(argument, "-") {
			return append([]string(nil), rawArgs[i:]...)
		}

		nameValue := strings.TrimPrefix(argument, "-")
		nameValue = strings.TrimPrefix(nameValue, "-")
		name, _, hasValue := strings.Cut(nameValue, "=")
		kind, ok := kinds[name]
		if !ok {
			return command.Args().Slice()
		}
		if kind.takesValue && !hasValue {
			i++
		}
	}
	return nil
}

func showRootUsageOnError(command *cli.Command) error {
	root := command.Root()
	writer := root.Writer
	root.Writer = root.ErrWriter
	defer func() {
		root.Writer = writer
	}()
	return cli.ShowRootCommandHelp(root)
}
