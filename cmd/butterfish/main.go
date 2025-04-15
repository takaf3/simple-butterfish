package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/alecthomas/kong"
	"github.com/joho/godotenv"
	"github.com/mitchellh/go-homedir"

	//_ "net/http/pprof"

	bf "github.com/bakks/butterfish/butterfish"
	"github.com/bakks/butterfish/util"
)

var ( // these are filled in at build time
	BuildVersion   string
	BuildCommit    string
	BuildTimestamp string
)

const description = `A simple shell wrapper to chat with an LLM.

Butterfish wraps your local shell. Start a command with a capital letter to send it as a prompt to the configured LLM, using your shell history as context.

Butterfish looks for an API key in OPENAI_API_KEY, or alternatively stores an OpenAI auth token at ~/.config/butterfish/butterfish.env.

Prompts are stored in ~/.config/butterfish/prompts.yaml. Butterfish logs to the system temp dir, usually to /var/tmp/butterfish.log. To print the full prompts and responses from the OpenAI API, use the --verbose flag. Support can be found at https://github.com/bakks/butterfish.
`
const license = "MIT License - Copyright (c) 2023 Peter Bakkum"
const defaultEnvPath = "~/.config/butterfish/butterfish.env"
const defaultPromptPath = "~/.config/butterfish/prompts.yaml"

const shell_help = `Start the Butterfish shell wrapper. This wraps your existing shell, giving you access to LLM prompting by starting your command with a capital letter. LLM calls include prior shell context.

Use:
  - Type a normal command, like 'ls -l' and press enter to execute it
  - Start a command with a capital letter to send it to GPT, like 'How do I recursively find local .py files?'
  - GPT will be able to see your shell history, so you can ask contextual questions like 'why didnt my last command work?'
`

type VerboseFlag bool

var verboseCount int

// This is a hook to count how many times the verbose flag is set, e.g. -vvv,
// but apparently it's always called at least once even if no flag is set
func (v *VerboseFlag) BeforeResolve() error {
	verboseCount++
	return nil
}

// Kong configuration for shell arguments
type CliConfig struct {
	Verbose      VerboseFlag      `short:"v" default:"false" help:"Verbose mode, prints full LLM prompts (sometimes to log file). Use multiple times for more verbosity, e.g. -vv."`
	Version      kong.VersionFlag `short:"V" help:"Print version information and exit."`
	BaseURL      string           `short:"u" default:"https://api.openai.com/v1" help:"Base URL for OpenAI-compatible API. Enables local models with a compatible interface."`
	TokenTimeout int              `short:"z" default:"10000" help:"Timeout before first prompt token is received and between individual tokens. In milliseconds."`
	ApiKey       string           `short:"k" help:"OpenAI API key. Overrides environment variables and config file."`
	LightColor   bool             `short:"l" default:"false" help:"Light color mode, appropriate for a terminal with a white(ish) background"`

	Shell struct {
		Bin                   string `short:"b" help:"Shell to use (e.g. /bin/zsh), defaults to $SHELL."`
		Model                 string `short:"m" default:"gpt-4.1-mini" help:"Model for when the user manually enters a prompt."`
		NoCommandPrompt       bool   `short:"p" default:"false" help:"Don't change command prompt (shell PS1 variable). If not set, an emoji will be added to the prompt as a reminder you're in Shell Mode."`
		MaxPromptTokens       int    `short:"P" default:"16384" help:"Maximum number of tokens, we restrict calls to this size regardless of model capabilities."`
		MaxHistoryBlockTokens int    `short:"H" default:"1024" help:"Maximum number of tokens of each block of history. For example, if a command has a very long output, it will be truncated to this length when sending the shell's history."`
		MaxResponseTokens     int    `short:"R" default:"2048" help:"Maximum number of tokens in a response when prompting."`
	} `cmd:"" help:"${shell_help}" default:"withargs"` // Make shell the default command
}

func getOpenAIToken() string {
	path, err := homedir.Expand(defaultEnvPath)
	if err != nil {
		log.Fatal(err)
	}

	// We attempt to get a token from env vars plus an env file
	godotenv.Load(path)

	token := os.Getenv("OPENAI_TOKEN")
	if token != "" {
		return token
	}

	token = os.Getenv("OPENAI_API_KEY")
	if token != "" {
		return token
	}

	// If we don't have a token, we'll prompt the user to create one
	fmt.Printf("Butterfish requires an OpenAI API key, please visit https://platform.openai.com/account/api-keys to create one and paste it below (it should start with sk-):\n")

	// read in the token and validate
	fmt.Scanln(&token)
	token = strings.TrimSpace(token)
	if token == "" {
		log.Fatal("No token provided, exiting")
	}
	if !strings.HasPrefix(token, "sk-") {
		log.Fatal("Invalid token provided, exiting")
	}

	// attempt to write a .env file
	fmt.Printf("\nSaving token to %s\n", path)
	err = os.MkdirAll(filepath.Dir(path), 0755)
	if err != nil {
		fmt.Printf("Error creating directory: %s\n", err.Error())
		return token
	}

	envFile, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		fmt.Printf("Error creating file: %s\n", err.Error())
		return token
	}
	defer envFile.Close()

	content := fmt.Sprintf("OPENAI_TOKEN=%s\n", token)
	_, err = envFile.WriteString(content)
	if err != nil {
		fmt.Printf("Error writing file: %s\n", err.Error())
	}

	fmt.Printf("Token saved, you can edit it at any time at %s\n\n", path)

	return token
}

func makeButterfishConfig(options *CliConfig) *bf.ButterfishConfig {
	config := bf.MakeButterfishConfig()
	if options.ApiKey != "" {
		config.OpenAIToken = options.ApiKey
	} else {
		config.OpenAIToken = getOpenAIToken()
	}
	config.BaseURL = options.BaseURL
	config.PromptLibraryPath = defaultPromptPath
	config.TokenTimeout = time.Duration(options.TokenTimeout) * time.Millisecond

	if options.Verbose {
		config.Verbose = verboseCount
	}

	return config
}

func getBuildInfo() string {
	buildOs := runtime.GOOS
	buildArch := runtime.GOARCH
	return fmt.Sprintf("%s %s %s\n(commit %s) (built %s)\n%s\n", BuildVersion, buildOs, buildArch, BuildCommit, BuildTimestamp, license)
}

func main() {
	// start pprof server in goroutine
	// go func() {
	// 	log.Println(http.ListenAndServe("localhost:6060", nil))
	// }()

	desc := fmt.Sprintf("%s\n%s", description, getBuildInfo())
	cli := &CliConfig{}

	cliParser, err := kong.New(cli,
		kong.Name("butterfish"),
		kong.Description(desc),
		kong.UsageOnError(),
		kong.Vars{
			"shell_help": shell_help,
			"version":    getBuildInfo(),
		})

	if err != nil {
		panic(err)
	}

	// Since 'shell' is the default command, we don't need to parse subcommands explicitly
	_, err = cliParser.Parse(os.Args[1:])
	cliParser.FatalIfErrorf(err)

	config := makeButterfishConfig(cli)
	config.BuildInfo = getBuildInfo()
	ctx := context.Background()

	errorWriter := util.NewStyledWriter(os.Stderr, config.Styles.Error)

	// --- Start Shell Mode ---
	logfileName := util.InitLogging(ctx)
	fmt.Printf("Logging to %s\n", logfileName)

	alreadyRunning := os.Getenv("BUTTERFISH_SHELL")
	if alreadyRunning != "" {
		fmt.Fprintf(errorWriter, "Butterfish shell is already running, cannot wrap shell again (detected with BUTTERFISH_SHELL env var).\n")
		os.Exit(8)
	}

	shell := os.Getenv("SHELL")
	if cli.Shell.Bin != "" {
		shell = cli.Shell.Bin
	}
	if shell == "" {
		fmt.Fprintf(errorWriter, "No shell found, please specify one with -b or $SHELL\n")
		os.Exit(7)
	}

	config.ShellBinary = shell
	config.ShellPromptModel = cli.Shell.Model
	config.ColorDark = !cli.LightColor
	config.ShellMode = true // Indicate we are running in shell mode
	config.ShellLeavePromptAlone = cli.Shell.NoCommandPrompt
	config.ShellMaxPromptTokens = cli.Shell.MaxPromptTokens
	config.ShellMaxHistoryBlockTokens = cli.Shell.MaxHistoryBlockTokens
	config.ShellMaxResponseTokens = cli.Shell.MaxResponseTokens

	// Removed autosuggest config assignments

	bf.RunShell(ctx, config)
	// --- End Shell Mode ---

}
