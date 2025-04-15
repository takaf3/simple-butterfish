# 🤖 Simple Butterfish

A simple shell wrapper for interacting with LLMs.

*(Note: This version of Butterfish was significantly refactored and simplified using Roo Code with Gemini 2.5 Pro.)*

## What is this thing?

Butterfish is for people who work from the command line. It wraps your existing shell (e.g., bash, zsh) and allows you to easily send prompts to an LLM (like GPT-4.1-mini or other OpenAI-compatible models) directly from your command line.

Here's how it works: use your shell as normal for regular commands. To prompt the AI, simply start your command line input with an uppercase letter. Butterfish sends your prompt along with recent conversation history (your uppercase prompts, the AI's answers, and the regular shell commands you ran) to the LLM. Shell command *output* is excluded from the context sent to the LLM for privacy.

This provides a simple way to get AI assistance within your shell workflow without copy/pasting.

### What can you do with Butterfish Shell?

Once you run `butterfish` (or `butterfish shell`), you can:

-   Run standard shell commands as usual (e.g., `ls -l`).
-   Ask the AI questions or give instructions by starting your input with an uppercase letter (e.g., `How do I recursively find local .py files?` or `Explain the last command I ran`).
-   Have a contextual conversation with the AI, as it remembers previous prompts, answers, and commands run.

<img src="https://github.com/takaf3/simple-butterfish/raw/main/vhs/gif/shell3.gif" alt="Demo of Butterfish Shell" width="500px" height="250px" />

Feedback and external contribution are very welcome! Butterfish is open source under the MIT license.

### Prompt Transparency

Butterfish aims for transparency. The system prompts used are configurable.

To see the raw AI requests/responses, you can run Butterfish in verbose mode (`butterfish -v`) and watch the log file (`/var/tmp/butterfish.log` on macOS). For more verbosity, use `-vv`.

To configure the prompts, you can edit `~/.config/butterfish/prompts.yaml`.

<img src="https://github.com/takaf3/simple-butterfish/raw/main/assets/verbose.png" alt="The verbose output of Butterfish Shell showing raw AI prompts" height="400px" />

## Installation & Authentication

Butterfish works on macOS and Linux. Ensure you have Go installed (version 1.21 or later recommended).

### Install from Source

Clone and build locally:

```sh
git clone https://github.com/takaf3/simple-butterfish.git
cd simple-butterfish
go build -o butterfish ./cmd/butterfish
./butterfish
```

Or install globally (adds to your Go bin dir):

```sh
go install ./cmd/butterfish
# Ensure $(go env GOPATH)/bin or $HOME/go/bin is in your PATH
```

### Install via Go Install (latest release)

```bash
go install github.com/takaf3/simple-butterfish/cmd/butterfish@latest
# Ensure $(go env GOPATH)/bin is in your PATH
butterfish
Is this thing working? # Type this literally into the CLI
```

The first invocation will prompt you to paste in an OpenAI API secret key. You can get an OpenAI key at [https://platform.openai.com/account/api-keys](https://platform.openai.com/account/api-keys).

The key will be written to `~/.config/butterfish/butterfish.env`, which looks like:

```
OPENAI_TOKEN=sk-foobar
```

It may also be useful to alias the `butterfish` command to something shorter. If you add the following line to your `~/.zshrc` or `~/.bashrc` file then you can run it with only `bf`.

```
alias bf="butterfish"
```

## Shell Mode

How does this work? Shell mode _wraps_ your shell rather than replacing it.

-   You run `butterfish` and use your existing shell as normal (tested with zsh and bash).
-   You start a command with a capital letter to prompt the LLM (e.g., `How do I...`).
-   The LLM sees the history of your prompts, its answers, and the shell commands you ran (but not the output of those commands).

<img src="https://github.com/takaf3/simple-butterfish/raw/main/vhs/gif/shell2.gif" alt="Butterfish" width="500px" height="250px" />

Shell mode defaults to using `gpt-4.1-mini` for prompting. You can specify a different OpenAI-compatible model using the `-m` flag:

```bash
butterfish shell -m some-other-model
```

### Shell Mode Command Reference

The primary way to use Butterfish is simply by running `butterfish`. It wraps your shell and provides the AI interaction. The `shell` command is the default.

```bash
> butterfish --help
Usage: butterfish [OPTIONS] [SHELL]

A simple shell wrapper to chat with an LLM.

Butterfish wraps your local shell. Start a command with a capital letter to send it as a prompt to the configured LLM, using your shell history as context.

Butterfish looks for an API key in OPENAI_API_KEY, or alternatively stores an OpenAI auth token at ~/.config/butterfish/butterfish.env.

Prompts are stored in ~/.config/butterfish/prompts.yaml. Butterfish logs to the system temp dir, usually to /var/tmp/butterfish.log. To print the full prompts and responses from the OpenAI API, use the --verbose flag. Support can be found at https://github.com/takaf3/simple-butterfish.

v[Version Info]
MIT License - Copyright (c) 2023 Peter Bakkum

Options:
  -h, --help                       Show context-sensitive help.
  -v, --verbose                    Verbose mode, prints full LLM prompts (sometimes to log file). Use multiple times for more verbosity, e.g. -vv.
  -V, --version                    Print version information and exit.
  -u, --base-url=STRING            Base URL for OpenAI-compatible API. Enables local models with a compatible interface. (Default: "https://api.openai.com/v1")
  -z, --token-timeout=INT          Timeout before first prompt token is received and between individual tokens. In milliseconds. (Default: 10000)
  -l, --light-color                Light color mode, appropriate for a terminal with a white(ish) background (Default: false)

Arguments:
  [SHELL] Start the Butterfish shell wrapper. This wraps your existing shell, giving you access to LLM prompting by starting your command with a capital letter. LLM calls include prior shell context.

Use:
  - Type a normal command, like 'ls -l' and press enter to execute it
  - Start a command with a capital letter to send it to GPT, like 'How do I recursively find local .py files?'
  - GPT will be able to see your shell history, so you can ask contextual questions like 'why didnt my last command work?'

Flags for SHELL:
  -b, --bin=STRING                 Shell to use (e.g. /bin/zsh), defaults to $SHELL.
  -m, --model="gpt-4.1-mini"         Model for when the user manually enters a prompt.
  -p, --no-command-prompt          Don't change command prompt (shell PS1 variable). If not set, an emoji will be added to the prompt as a reminder you're in Shell Mode. (Default: false)
  -P, --max-prompt-tokens=16384    Maximum number of tokens, we restrict calls to this size regardless of model capabilities.
  -H, --max-history-block-tokens=1024
                                   Maximum number of tokens of each block of history. For example, if a command has a very long output, it will be truncated to this length when sending the shell's history.
  -R, --max-response-tokens=2048   Maximum number of tokens in a response when prompting.

```

## Local Models

Butterfish uses OpenAI models by default, but you can instead point it to any server with an OpenAI-compatible API using the `--base-url` (`-u`) flag. For example:

```bash
butterfish -u "http://localhost:5000/v1"
```

This enables using Butterfish with local or remote non-OpenAI models. Notes on this feature:

-   In practice, using hosted models is often simpler than running your own, and Butterfish's prompts have been tuned for GPT models, so you will likely get the best results using the default OpenAI models or compatible high-quality alternatives.
-   Being OpenAI-API compatible in this case means implementing the [Chat Completions endpoint](https://platform.openai.com/docs/api-reference/chat/create) with streaming results.
-   Butterfish will add your token to requests to the chat completions endpoint, so be careful about accidentally leaking credentials if you don't trust the server.
-   Options for running a local model with a compatible interface include [LM Studio](https://lmstudio.ai/) and [text-generation-webui](https://github.com/oobabooga/text-generation-webui).

## Prompt Library

A goal of Butterfish is to make prompts transparent and easily editable. Butterfish will write a default prompt library to `~/.config/butterfish/prompts.yaml` and load this every time it runs. You can edit prompts in that file to tweak them. If you edit a prompt, set `OkToReplace: false` in the YAML file to prevent Butterfish from overwriting your changes on startup.

```bash
> head ~/.config/butterfish/prompts.yaml
- name: shell_system_message
  prompt: 'You are an assistant that helps the user with a Unix shell. Give advice
    about commands that can be run and examples but keep your answers succinct. Give
    very short answers for short or easy questions, in-depth answers for complex
    questions. You don''t need to tell the user how to install commands that you
    mention. It is ok if the user asks questions not directly related to the unix
    shell. System info about the local machine: ''{sysinfo}'''
  oktoreplace: true
# ... (other prompts might exist depending on version)
```

If you want to see the exact communication between Butterfish and the OpenAI API, use the verbose flag (`-v` or `-vv`) when you run Butterfish. This will print the full prompt and response either to the terminal or to the log file (`/var/tmp/butterfish.log` on macOS).

## Dev Setup

Development has primarily been on macOS, but it should work on Linux. Ensure you have Go installed.

```bash
# Optional dependencies for some development tasks (like regenerating protobufs)
# brew install protobuf protoc-gen-go protoc-gen-go-grpc # macOS example

git clone https://github.com/takaf3/simple-butterfish.git
cd simple-butterfish
make
./bin/butterfish
Is this thing working? # Type this into the running shell
```