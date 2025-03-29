package butterfish

import (
	"bytes"
	"context"

	// "encoding/json" // Removed
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"

	"github.com/bakks/butterfish/prompt"
	"github.com/bakks/butterfish/util"

	// "github.com/sashabaranov/go-openai/jsonschema" // Removed

	"github.com/bakks/tiktoken-go"
	// "github.com/mitchellh/go-ps" // Removed
	"golang.org/x/term"
)

// Default model encoder if the specified one isn't found
const DEFAULT_PROMPT_ENCODER = "gpt-4-turbo"

const ESC_CUP = "\x1b[6n" // Request the cursor position
const ESC_UP = "\x1b[%dA"
const ESC_RIGHT = "\x1b[%dC"
const ESC_LEFT = "\x1b[%dD"
const ESC_CLEAR = "\x1b[0K"
const CLEAR_COLOR = "\x1b[0m"

// Special characters that we wrap the shell's command prompt in (PS1) so
// that we can detect where it starts and ends.
const PROMPT_PREFIX = "\033Q"
const PROMPT_SUFFIX = "\033R"
const PROMPT_PREFIX_ESCAPED = "\\033Q"
const PROMPT_SUFFIX_ESCAPED = "\\033R"
const EMOJI_DEFAULT = "🐠"

// Removed Goal Mode Emojis

var ps1Regex = regexp.MustCompile(" ([0-9]+)" + PROMPT_SUFFIX)
var ps1FullRegex = regexp.MustCompile(EMOJI_DEFAULT + " ([0-9]+)" + PROMPT_SUFFIX)

// Simplified Color Scheme
var DarkShellColorScheme = &ShellColorScheme{
	Prompt:          "\x1b[38;5;154m",
	Command:         "\x1b[0m",
	Answer:          "\x1b[38;5;221m", // yellow
	AnswerHighlight: "\x1b[38;5;204m", // orange
	Error:           "\x1b[38;5;196m",
}

var LightShellColorScheme = &ShellColorScheme{
	Prompt:          "\x1b[38;5;28m",
	Command:         "\x1b[0m",
	Answer:          "\x1b[38;5;18m", // Dark blue
	AnswerHighlight: "\x1b[38;5;6m",
	Error:           "\x1b[38;5;196m",
}

func RunShell(ctx context.Context, config *ButterfishConfig) error {
	envVars := []string{"BUTTERFISH_SHELL=1"}

	ptmx, ptyCleanup, err := ptyCommand(ctx, envVars, []string{config.ShellBinary})
	if err != nil {
		return err
	}
	defer ptyCleanup()

	bf, err := NewButterfish(ctx, config)
	if err != nil {
		return err
	}

	bf.ShellMultiplexer(ptmx, ptmx, os.Stdin, os.Stdout)
	return nil
}

const (
	historyTypePrompt = iota
	historyTypeShellInput
	historyTypeShellOutput
	historyTypeLLMOutput
	// Removed Goal Mode history types
)

// Turn history type enum to a string
func HistoryTypeToString(historyType int) string {
	switch historyType {
	case historyTypePrompt:
		return "Prompt"
	case historyTypeShellInput:
		return "Shell Input"
	case historyTypeShellOutput:
		return "Shell Output"
	case historyTypeLLMOutput:
		return "LLM Output"
	default:
		return "Unknown"
	}
}

type Tokenization struct {
	InputLength int    // the unprocessed length of the pretokenized plus truncated content
	NumTokens   int    // number of tokens in the data
	Data        string // tokenized and truncated content
}

// HistoryBuffer keeps a content buffer, plus an enum of the type of content
// (user prompt, shell output, etc), plus a cache of tokenizations of the
// content.
type HistoryBuffer struct {
	Type    int
	Content *ShellBuffer
	// Removed FunctionName, FunctionParams

	// This is to cache tokenization plus truncation of the content
	Tokenizations map[string]Tokenization
}

func (this *HistoryBuffer) SetTokenization(encoding string, inputLength int, numTokens int, data string) {
	if this.Tokenizations == nil {
		this.Tokenizations = make(map[string]Tokenization)
	}
	this.Tokenizations[encoding] = Tokenization{
		InputLength: inputLength,
		NumTokens:   numTokens,
		Data:        data,
	}
}

func (this *HistoryBuffer) GetTokenization(encoding string, length int) (string, int, bool) {
	if this.Tokenizations == nil {
		this.Tokenizations = make(map[string]Tokenization)
	}

	tokenization, ok := this.Tokenizations[encoding]
	if !ok {
		return "", 0, false
	}
	if tokenization.InputLength != length {
		return "", 0, false
	}
	return tokenization.Data, tokenization.NumTokens, true
}

// ShellHistory keeps a record of past shell history and LLM interaction.
type ShellHistory struct {
	Blocks []*HistoryBuffer
	mutex  sync.Mutex
}

func NewShellHistory() *ShellHistory {
	return &ShellHistory{
		Blocks: make([]*HistoryBuffer, 0),
	}
}

func (this *ShellHistory) add(historyType int, block string) {
	buffer := NewShellBuffer()
	buffer.Write(block)
	this.Blocks = append(this.Blocks, &HistoryBuffer{
		Type:    historyType,
		Content: buffer,
	})
}

func (this *ShellHistory) Append(historyType int, data string) {
	// if data is empty, we don't want to add a new block
	if len(data) == 0 {
		return
	}

	this.mutex.Lock()
	defer this.mutex.Unlock()

	numBlocks := len(this.Blocks)
	// if we have a block already, and it matches the type, append to it
	if numBlocks > 0 {
		lastBlock := this.Blocks[numBlocks-1]

		if lastBlock.Type == historyType {
			lastBlock.Content.Write(data)
			return
		}
	}

	// if the history type doesn't match we fall through and add a new block
	this.add(historyType, data)
}

// Removed AddFunctionCall
// Removed AppendFunctionOutput

// Go back in history for a certain number of bytes.
func (this *ShellHistory) GetLastNBytes(numBytes int, truncateLength int) []util.HistoryBlock {
	this.mutex.Lock()
	defer this.mutex.Unlock()

	var blocks []util.HistoryBlock

	for i := len(this.Blocks) - 1; i >= 0 && numBytes > 0; i-- {
		block := this.Blocks[i]
		content := sanitizeTTYString(block.Content.String())
		if len(content) > truncateLength {
			content = content[:truncateLength]
		}
		if len(content) > numBytes {
			break // we don't want a weird partial line so we bail out here
		}
		blocks = append(blocks, util.HistoryBlock{
			Type:    block.Type,
			Content: content,
		})
		numBytes -= len(content)
	}

	// reverse the blocks slice
	for i := len(blocks)/2 - 1; i >= 0; i-- {
		opp := len(blocks) - 1 - i
		blocks[i], blocks[opp] = blocks[opp], blocks[i]
	}

	return blocks
}

func (this *ShellHistory) IterateBlocks(cb func(block *HistoryBuffer) bool) {
	this.mutex.Lock()
	defer this.mutex.Unlock()

	for i := len(this.Blocks) - 1; i >= 0; i-- {
		cont := cb(this.Blocks[i])
		if !cont {
			break
		}
	}
}

// This is not thread safe
func (this *ShellHistory) LogRecentHistory() {
	blocks := this.GetLastNBytes(2000, 512)
	log.Printf("Recent history: =======================================")
	builder := strings.Builder{}
	for _, block := range blocks {
		builder.WriteString(fmt.Sprintf("%s: %s\n", HistoryTypeToString(block.Type), block.Content))
	}
	log.Printf(builder.String())
	log.Printf("=======================================")
}

// Removed HistoryBlocksToString (unused)

const (
	stateNormal = iota
	stateShell
	statePrompting
	statePromptResponse
)

var stateNames = []string{
	"Normal",
	"Shell",
	"Prompting",
	"PromptResponse",
}

// Removed AutosuggestResult

// Simplified ShellColorScheme
type ShellColorScheme struct {
	Prompt          string
	Error           string
	Command         string
	Answer          string
	AnswerHighlight string
}

// Simplified ShellState
type ShellState struct {
	Butterfish *ButterfishCtx
	ParentOut  io.Writer
	ChildIn    io.Writer
	Sigwinch   chan os.Signal

	// set based on model
	PromptMaxTokens int

	// The current state of the shell
	State                int
	PromptSuffixCounter  int // Still needed for PS1 parsing
	ChildOutReader       chan *byteMsg
	ParentInReader       chan *byteMsg
	CursorPosChan        chan *cursorPosition
	PromptOutputChan     chan *util.CompletionResponse
	PrintErrorChan       chan error
	History              *ShellHistory
	PromptAnswerWriter   io.Writer
	StyleWriter          *util.StyleCodeblocksWriter
	Prompt               *ShellBuffer
	PromptResponseCancel context.CancelFunc
	Command              *ShellBuffer
	TerminalWidth        int
	Color                *ShellColorScheme
	parentInBuffer       []byte
	PromptEncoder        *tiktoken.Tiktoken

	// Removed Goal Mode fields
	// Removed Autosuggest fields
}

func (this *ShellState) setState(state int) {
	if this.State == state {
		return
	}

	if this.Butterfish.Config.Verbose > 1 {
		log.Printf("State change: %s -> %s", stateNames[this.State], stateNames[state])
	}

	this.State = state
}

func clearByteChan(r <-chan *byteMsg, timeout time.Duration) {
	// then wait for timeout
	target := 2
	seen := 0

	for {
		select {
		case <-time.After(timeout):
			return
		case msg := <-r:
			// if msg.Data includes \n we break
			if bytes.Contains(msg.Data, []byte("\n")) {
				seen++
				if seen >= target {
					return
				}
			}
			continue
		}
	}
}

func (this *ShellState) GetCursorPosition() (int, int) {
	// send the cursor position request
	this.ParentOut.Write([]byte(ESC_CUP))
	// we wait 5s, if we haven't gotten a response by then we likely have a bug
	timeout := time.After(5000 * time.Millisecond)
	var pos *cursorPosition

	// the parent in reader watches for these responses, set timeout and
	// panic if we don't get a response
	select {
	case <-timeout:
		panic(`Timeout waiting for cursor position response, this means that either:
- Butterfish has frozen due to a bug.
- You're using a terminal emulator that doesn't work well with butterfish.
Please submit an issue to https://github.com/bakks/butterfish.`)

	case pos = <-this.CursorPosChan:
	}

	// it's possible that we have a stale response, so we loop on the channel
	// until we get the most recent one
	for {
		select {
		case pos = <-this.CursorPosChan:
			continue
		default:
			return pos.Row, pos.Column
		}
	}
}

// This sets the PS1 shell variable.
func (this *ButterfishCtx) SetPS1(childIn io.Writer) {
	shell := this.Config.ParseShell()
	var ps1 string

	switch shell {
	case "bash", "sh":
		ps1 = "PS1=$'\\[%s\\]'$PS1$'%s\\[ $?%s\\] '\n"
	case "zsh":
		ps1 = "PS1=$'%%{%s%%}'$PS1$'%s%%{ %%?%s%%} '\n"
	default:
		log.Printf("Unknown shell %s, Butterfish is going to leave the PS1 alone. This means that you won't get a custom prompt in Butterfish, and Butterfish won't be able to parse the exit code of the previous command. Create an issue at https://github.com/bakks/butterfish.", shell)
		return
	}

	promptIcon := ""
	if !this.Config.ShellLeavePromptAlone {
		promptIcon = EMOJI_DEFAULT
	}

	fmt.Fprintf(childIn,
		ps1,
		PROMPT_PREFIX_ESCAPED,
		promptIcon,
		PROMPT_SUFFIX_ESCAPED)
}

// Given a string of terminal output, identify terminal prompts based on the
// custom PS1 escape sequences we set.
func ParsePS1(data string, regex *regexp.Regexp, currIcon string) (int, int, string) {
	matches := regex.FindAllStringSubmatch(data, -1)

	if len(matches) == 0 {
		return 0, 0, data
	}

	lastStatus := 0
	prompts := 0

	for _, match := range matches {
		var err error
		lastStatus, err = strconv.Atoi(match[1])
		if err != nil {
			log.Printf("Error parsing PS1 match: %s", err)
		}
		prompts++
	}

	// Remove matches of suffix
	cleaned := regex.ReplaceAllString(data, currIcon)
	// Remove the prefix
	cleaned = strings.ReplaceAll(cleaned, PROMPT_PREFIX, "")

	return lastStatus, prompts, cleaned
}

func (this *ShellState) ParsePS1(data string) (int, int, string) {
	var regex *regexp.Regexp
	if this.Butterfish.Config.ShellLeavePromptAlone {
		regex = ps1Regex
	} else {
		regex = ps1FullRegex
	}

	currIcon := ""
	if !this.Butterfish.Config.ShellLeavePromptAlone {
		// Removed Goal Mode icon logic
		currIcon = EMOJI_DEFAULT
	}

	return ParsePS1(data, regex, currIcon)
}

// zsh appears to use this sequence to clear formatting and the rest of the line
// before printing a prompt
var ZSH_CLEAR_REGEX = regexp.MustCompile("^\x1b\\[1m\x1b\\[3m%\x1b\\[23m\x1b\\[1m\x1b\\[0m\x20+\x0d\x20\x0d")

func (this *ShellState) FilterChildOut(data string) bool {
	if len(data) > 0 && strings.HasPrefix(data, "\x1b[1m") && ZSH_CLEAR_REGEX.MatchString(data) {
		return true
	}

	return false
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (this *ButterfishCtx) ShellMultiplexer(
	childIn io.Writer, childOut io.Reader,
	parentIn io.Reader, parentOut io.Writer) {

	this.SetPS1(childIn)

	colorScheme := DarkShellColorScheme
	if !this.Config.ColorDark {
		colorScheme = LightShellColorScheme
	}

	log.Printf("Starting shell multiplexer")

	childOutReader := make(chan *byteMsg, 8)
	parentInReader := make(chan *byteMsg, 8)
	parentPositionChan := make(chan *cursorPosition, 128)

	termWidth, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		panic(err)
	}

	carriageReturnWriter := util.NewReplaceWriter(parentOut, "\n", "\r\n")
	codeblocksColorScheme := "monokai"
	if !this.Config.ColorDark {
		codeblocksColorScheme = "monokailight"
	}
	styleCodeblocksWriter := util.NewStyleCodeblocksWriter(
		carriageReturnWriter,
		termWidth,
		colorScheme.Answer,
		colorScheme.AnswerHighlight,
		codeblocksColorScheme)
	// Removed Goal Mode writer

	sigwinch := make(chan os.Signal, 1)
	signal.Notify(sigwinch, syscall.SIGWINCH)

	promptMaxTokens := min(
		NumTokensForModel(this.Config.ShellPromptModel),
		this.Config.ShellMaxPromptTokens)
	// Removed AutosuggestMaxTokens calculation

	shellState := &ShellState{
		Butterfish:         this,
		ParentOut:          parentOut,
		ChildIn:            childIn,
		Sigwinch:           sigwinch,
		State:              stateNormal,
		ChildOutReader:     childOutReader,
		ParentInReader:     parentInReader,
		CursorPosChan:      parentPositionChan,
		PrintErrorChan:     make(chan error, 8),
		History:            NewShellHistory(),
		PromptOutputChan:   make(chan *util.CompletionResponse),
		PromptAnswerWriter: styleCodeblocksWriter,
		// Removed PromptGoalAnswerWriter
		StyleWriter:     styleCodeblocksWriter,
		Command:         NewShellBuffer(),
		Prompt:          NewShellBuffer(),
		TerminalWidth:   termWidth,
		Color:           colorScheme,
		parentInBuffer:  []byte{},
		PromptMaxTokens: promptMaxTokens,
		// Removed AutosuggestMaxTokens
		// Removed AutosuggestEnabled
		// Removed AutosuggestChan
	}

	shellState.Prompt.SetTerminalWidth(termWidth)
	shellState.Prompt.SetColor(colorScheme.Prompt)

	go readerToChannel(childOut, childOutReader)
	go readerToChannelWithPosition(parentIn, parentInReader, parentPositionChan)

	// clear out any existing output to hide the PS1 export stuff
	clearByteChan(childOutReader, 1000*time.Millisecond)

	// start
	shellState.Mux()
}

func (this *ShellState) Errorf(format string, args ...any) {
	this.PrintErrorChan <- fmt.Errorf(format, args...)
}

func (this *ShellState) PrintError(err error) {
	this.PrintErrorChan <- err
}

// Removed AddDoubleEscapesForJSON
// Removed CommandParams, UserInputParams, FinishParams structs and parsing functions
// Removed goalModeFunctions, goalModeFunctionsString, getGoalModeFunctionsString

// Simplified Mux loop
func (this *ShellState) Mux() {
	log.Printf("Started shell mux")
	childOutBuffer := []byte{}

	for {
		select {
		case <-this.Butterfish.Ctx.Done():
			return

		case err := <-this.PrintErrorChan:
			log.Printf("Error: %s", err.Error())
			this.History.Append(historyTypeShellOutput, err.Error())
			fmt.Fprintf(this.ParentOut, "%s%s", this.Color.Error, err.Error())
			this.setState(stateNormal)
			fmt.Fprintf(this.ChildIn, "\n")

		case pos := <-this.CursorPosChan:
			fmt.Fprintf(this.ChildIn, "\x1b[%d;%dR", pos.Row, pos.Column)

		case <-this.Sigwinch:
			termWidth, _, err := term.GetSize(int(os.Stdout.Fd()))
			if err != nil {
				log.Printf("Error getting terminal size after SIGWINCH: %s", err)
			}
			if this.Butterfish.Config.Verbose > 0 {
				log.Printf("Got SIGWINCH with new width %d", termWidth)
			}
			this.TerminalWidth = termWidth
			this.Prompt.SetTerminalWidth(termWidth)
			this.StyleWriter.SetTerminalWidth(termWidth)
			// Removed AutosuggestBuffer width update
			if this.Command != nil {
				this.Command.SetTerminalWidth(termWidth)
			}

		// Removed AutosuggestChan case

		case output := <-this.PromptOutputChan:
			historyData := output.Completion
			if historyData != "" {
				this.History.Append(historyTypeLLMOutput, historyData)
			}
			// Removed function call history logging

			// If there is child output waiting to be printed, print that now
			if len(childOutBuffer) > 0 {
				this.ParentOut.Write(childOutBuffer)
				this.History.Append(historyTypeShellOutput, string(childOutBuffer))
				childOutBuffer = []byte{}
			}

			// Get a new prompt
			this.ChildIn.Write([]byte("\n"))

			// Removed Goal Mode handling
			this.setState(stateNormal)
			this.ParentInputLoop([]byte{}) // Process any buffered input

		case childOutMsg := <-this.ChildOutReader:
			if childOutMsg == nil {
				log.Println("Child out reader closed")
				this.Butterfish.Cancel()
				return
			}

			if this.Butterfish.Config.Verbose > 2 {
				log.Printf("Child out: %x", string(childOutMsg.Data))
			}

			_, prompts, childOutStr := this.ParsePS1(string(childOutMsg.Data))
			this.PromptSuffixCounter += prompts // Still needed to detect prompt end

			// Removed autosuggest request on new prompt

			// If we're actively printing a response we buffer child output
			if this.State == statePromptResponse {
				// Removed Goal Mode check (always buffer if responding)
				childOutBuffer = append(childOutBuffer, childOutStr...)
				continue
			}

			// Removed Goal Mode function call end detection

			// If we're getting child output while typing in a shell command, this
			// could mean the user is paging through old commands, or doing a tab
			// completion, or something unknown, so we don't want to add to history.
			if this.State != stateShell && !this.FilterChildOut(string(childOutMsg.Data)) {
				// Removed ActiveFunction check
				this.History.Append(historyTypeShellOutput, childOutStr)
			}

			// Removed Tab completion handling for shell output

			this.ParentOut.Write([]byte(childOutStr))

			// Removed Goal Mode function response trigger

		case parentInMsg := <-this.ParentInReader:
			if parentInMsg == nil {
				log.Println("Parent in reader closed")
				this.Butterfish.Cancel()
				return
			}

			this.ParentInputLoop(parentInMsg.Data)
		}
	}
}

func (this *ShellState) ParentInputLoop(data []byte) {
	if this.Butterfish.Config.Verbose > 2 {
		log.Printf("Parent in: %x", data)
	}

	// include any cached data
	if len(this.parentInBuffer) > 0 {
		data = append(this.parentInBuffer, data...)
		this.parentInBuffer = []byte{}
	}

	if len(data) == 0 {
		return
	}

	// If we've started an ANSI escape sequence, it might not be complete
	// yet, so we need to cache it and wait for the next message
	if incompleteAnsiSequence(data) {
		this.parentInBuffer = append(this.parentInBuffer, data...)
		return
	}

	for {
		leftover := this.ParentInput(this.Butterfish.Ctx, data)

		if leftover == nil || len(leftover) == 0 {
			break
		}
		if len(leftover) == len(data) {
			// nothing was consumed, we buffer and try again later
			this.parentInBuffer = append(this.parentInBuffer, leftover...)
			break
		}
		data = leftover
	}
}

// Simplified ParentInput
func (this *ShellState) ParentInput(ctx context.Context, data []byte) []byte {
	hasCarriageReturn := bytes.Contains(data, []byte{'\r'})

	switch this.State {
	case statePromptResponse:
		// Ctrl-C while receiving prompt
		if data[0] == 0x03 || data[len(data)-1] == 0x03 {
			log.Printf("Canceling prompt response")
			if this.PromptResponseCancel != nil {
				this.PromptResponseCancel()
				this.PromptResponseCancel = nil
			}
			// Removed GoalMode = false
			this.setState(stateNormal)
			if data[0] == 0x03 {
				return data[1:]
			} else {
				return data[:len(data)-1]
			}
		}
		// Ignore other input during response
		return data

	case stateNormal:
		// Removed HasRunningChildren check (simplification, assume shell is ready)

		if data[0] == 0x03 { // Ctrl-C
			// Removed Goal Mode check
			if this.Command != nil {
				this.Command.Clear()
			}
			if this.Prompt != nil {
				this.Prompt.Clear()
			}
			this.setState(stateNormal)
			this.ChildIn.Write([]byte{data[0]})
			return data[1:]
		}

		// Handle Ctrl+L (Form Feed / Clear Screen)
		if data[0] == '\f' { // ASCII 12
			// Send ANSI codes to clear screen and move cursor to top-left
			this.ParentOut.Write([]byte("\x1b[2J\x1b[H"))
			// Clear internal buffers just in case
			if this.Command != nil {
				this.Command.Clear()
			}
			if this.Prompt != nil {
				this.Prompt.Clear()
			}
			// Send a carriage return to the child shell to trigger a prompt redraw
			this.ChildIn.Write([]byte("\r"))
			// Consume the Ctrl+L character
			return data[1:]
		}

		// Check if the first character is uppercase
		if unicode.IsUpper(rune(data[0])) { // Removed '!' check for Goal Mode
			this.setState(statePrompting)
			// Removed ClearAutosuggest
			this.Prompt.Clear()
			this.Prompt.Write(string(data))

			// Write the actual prompt start
			color := this.Color.Prompt
			// Removed Goal Mode color check
			this.Prompt.SetColor(color)
			fmt.Fprintf(this.ParentOut, "%s%s", color, data)

			// Get cursor position for prompt buffer
			_, col := this.GetCursorPosition()
			this.Prompt.SetPromptLength(col - 1 - this.Prompt.Size())
			return data[1:]

		} else if data[0] == '\t' { // Tab pressed
			// Removed autosuggest handling, just forward Tab
			this.ChildIn.Write([]byte{data[0]})
			return data[1:]

		} else if data[0] == '\r' { // Enter pressed
			// Removed ClearAutosuggest
			this.ChildIn.Write(data)
			return data[1:]

		} else { // Regular shell command character
			this.Command = NewShellBuffer()
			this.Command.Write(string(data))

			if this.Command.Size() > 0 {
				// Removed RefreshAutosuggest
				this.setState(stateShell)
			} else {
				// Removed ClearAutosuggest
			}

			this.ParentOut.Write([]byte(this.Color.Command))
			this.ChildIn.Write(data)
			return nil // Consumed all data
		}
	case statePrompting:
		if hasCarriageReturn { // Enter pressed during prompt
			// Removed ClearAutosuggest
			index := bytes.Index(data, []byte{'\r'})
			toAdd := data[:index]
			toPrint := this.Prompt.Write(string(toAdd))

			this.ParentOut.Write(toPrint)
			this.ParentOut.Write([]byte("\n\r"))

			// Removed HandleLocalPrompt
			// Removed GoalModeStart/GoalModeChat
			this.SendPrompt() // Always send prompt now
			return data[index+1:]

		} else if data[0] == '\t' { // Tab pressed during prompt
			// Removed autosuggest handling, just echo Tab (or ignore?) - let's echo
			this.ParentOut.Write(data)
			return data[1:]

		} else if data[0] == 0x03 { // Ctrl-C during prompt
			if this.PromptResponseCancel != nil {
				this.PromptResponseCancel()
				this.PromptResponseCancel = nil
			}
			// Removed ClearAutosuggest
			toPrint := this.Prompt.Clear()
			this.ParentOut.Write(toPrint)
			this.ParentOut.Write([]byte(this.Color.Command))
			this.setState(stateNormal)
			return data[1:]

		} else if data[0] == '\f' { // Ctrl+L during prompt
			// Send ANSI codes to clear screen and move cursor to top-left
			this.ParentOut.Write([]byte("\x1b[2J\x1b[H"))
			// Redraw the prompt and current input
			this.ParentOut.Write([]byte(this.Color.Prompt))
			this.ParentOut.Write([]byte(this.Prompt.String()))
			// Consume the Ctrl+L character
			return data[1:]

		} else { // Typing prompt character
			toPrint := this.Prompt.Write(string(data))
			// Removed RefreshAutosuggest
			this.ParentOut.Write(toPrint)

			if this.Prompt.Size() == 0 {
				this.ParentOut.Write([]byte(this.Color.Command)) // reset color
				this.setState(stateNormal)
			}
			return nil // Consumed all data
		}

	case stateShell:
		if hasCarriageReturn { // Enter pressed during shell command
			// Removed ClearAutosuggest
			this.setState(stateNormal)

			index := bytes.Index(data, []byte{'\r'})
			this.ChildIn.Write(data[:index+1])
			this.History.Append(historyTypeShellInput, this.Command.String())
			this.Command = NewShellBuffer()

			// Removed AutosuggestCancel

			return data[index+1:]

		} else if data[0] == 0x03 { // Ctrl-C during shell command
			this.Command.Clear()
			this.setState(stateNormal)
			this.ChildIn.Write([]byte{data[0]})
			// Removed AutosuggestCancel
			return data[1:]

		} else if data[0] == '\t' { // Tab pressed during shell command
			// Removed autosuggest handling, just forward Tab
			this.ChildIn.Write([]byte{data[0]})
			return data[1:]

		} else { // Typing shell command character
			this.Command.Write(string(data))
			// Removed RefreshAutosuggest
			this.ChildIn.Write(data)
			if this.Command.Size() == 0 {
				this.setState(stateNormal)
			}
			return nil // Consumed all data
		}

	default:
		panic("Unknown state")
	}
}

// Removed SendPromptResponse (no longer needed)
// Removed PrintStatus, PrintHelp, PrintHistory
// Removed GoalModeStart, GoalModeChat, GoalModeFunctionResponse, GoalModeFunction, goalModePrompt

// Simplified AssembleChat - removed functions parameter
func (this *ShellState) AssembleChat(prompt, sysMsg string, reserveForAnswer int) (string, []util.HistoryBlock, error) {
	totalTokens := this.PromptMaxTokens
	maxPromptTokens := 512
	maxHistoryBlockTokens := this.Butterfish.Config.ShellMaxHistoryBlockTokens
	maxCombinedPromptTokens := totalTokens - reserveForAnswer

	return assembleChat(prompt, sysMsg, "", this.History, // Pass empty string for functions
		this.Butterfish.Config.ShellPromptModel, this.getPromptEncoder(),
		maxPromptTokens, maxHistoryBlockTokens, maxCombinedPromptTokens)
}

// Simplified assembleChat - removed functions parameter and related logic
func assembleChat(
	prompt string,
	sysMsg string,
	functions string, // Keep signature but ignore
	history *ShellHistory,
	model string,
	encoder *tiktoken.Tiktoken,
	maxPromptTokens int,
	maxHistoryBlockTokens int,
	maxTokens int,
) (string, []util.HistoryBlock, error) {

	tokensPerMessage := NumTokensPerMessageForModel(model)
	usedTokens := 3 // baseline for chat

	// account for prompt
	numPromptTokens, prompt, truncated := countAndTruncate(prompt, encoder, maxPromptTokens)
	if truncated {
		log.Printf("WARNING: truncated the prompt to %d tokens", numPromptTokens)
	}
	usedTokens += numPromptTokens

	// account for system message
	sysMsgTokens := encoder.Encode(sysMsg, nil, nil)
	if len(sysMsgTokens) > 1028 {
		log.Printf("WARNING: the system message is very long, this may cause you to hit the token limit. Recommend you reduce the size in prompts.yaml")
	}
	usedTokens += len(sysMsgTokens) + tokensPerMessage // Add tokens for sys msg role
	if usedTokens > maxTokens {
		return "", nil, fmt.Errorf("System message too long, %d tokens, max is %d", usedTokens, maxTokens)
	}

	// Removed function token accounting

	blocks, historyTokens := getHistoryBlocksByTokens(
		history,
		encoder,
		maxHistoryBlockTokens,
		maxTokens-usedTokens,
		tokensPerMessage)
	usedTokens += historyTokens

	if usedTokens > maxTokens {
		// This might happen if history alone exceeds limit after accounting for prompt/sysmsg
		log.Printf("Warning: History truncated significantly due to token limits. Used: %d, Max: %d", usedTokens, maxTokens)
		// Allow proceeding with truncated history
	}

	return prompt, blocks, nil
}

// Simplified getHistoryBlocksByTokens - removed function call handling
func getHistoryBlocksByTokens(
	history *ShellHistory,
	encoder *tiktoken.Tiktoken,
	maxHistoryBlockTokens,
	maxTokens,
	tokensPerMessage int,
) ([]util.HistoryBlock, int) {

	blocks := []util.HistoryBlock{}
	usedTokens := 0

	history.IterateBlocks(func(block *HistoryBuffer) bool {
		// --- Filter out shell command output ---
		if block.Type == historyTypeShellOutput {
			return true // Skip shell output blocks
		}
		// --- End Filter ---

		if block.Content.Size() == 0 { // Removed FunctionName check
			return true // empty block, skip
		}
		msgTokens := tokensPerMessage
		roleString := ShellHistoryTypeToRole(block.Type)

		// add tokens for role
		msgTokens += len(encoder.Encode(roleString, nil, nil))

		// Removed function name/params token counting

		// check existing block tokenizations
		contentLen := block.Content.Size()
		content, contentTokens, ok := block.GetTokenization(encoder.EncoderName(), contentLen)

		if !ok { // cache miss
			contentStr := block.Content.String()
			ceiling := maxHistoryBlockTokens * 4
			if contentLen > ceiling {
				contentStr = contentStr[:ceiling]
			}
			historyContent := sanitizeTTYString(contentStr)
			contentTokens, content, _ = countAndTruncate(historyContent, encoder, maxHistoryBlockTokens)
			block.SetTokenization(encoder.EncoderName(), contentLen, contentTokens, content)
		}
		msgTokens += contentTokens

		if usedTokens+msgTokens > maxTokens {
			return false // we're done adding blocks
		}

		usedTokens += msgTokens
		newBlock := util.HistoryBlock{
			Type:    block.Type,
			Content: content,
			// Removed FunctionName, FunctionParams
		}

		blocks = append([]util.HistoryBlock{newBlock}, blocks...)
		return true
	})

	return blocks, usedTokens
}

// Simplified SendPrompt
func (this *ShellState) SendPrompt() {
	this.setState(statePromptResponse)

	requestCtx, cancel := context.WithCancel(context.Background())
	this.PromptResponseCancel = cancel

	sysMsg, err := this.Butterfish.PromptLibrary.GetPrompt(
		prompt.ShellSystemMessage, "sysinfo", GetSystemInfo())
	if err != nil {
		msg := fmt.Errorf("Could not retrieve prompting system message: %s", err)
		this.PrintError(msg)
		return
	}

	promptStr := this.Prompt.String()
	tokensReservedForAnswer := this.Butterfish.Config.ShellMaxResponseTokens
	promptStr, historyBlocks, err := this.AssembleChat(promptStr, sysMsg, tokensReservedForAnswer)
	if err != nil {
		this.PrintError(err)
		return
	}

	request := &util.CompletionRequest{
		Ctx:           requestCtx,
		Prompt:        promptStr,
		Model:         this.Butterfish.Config.ShellPromptModel,
		MaxTokens:     tokensReservedForAnswer,
		Temperature:   0.7,
		HistoryBlocks: historyBlocks,
		SystemMessage: sysMsg,
		Verbose:       this.Butterfish.Config.Verbose > 0,
		TokenTimeout:  this.Butterfish.Config.TokenTimeout,
		// Removed Functions
	}

	this.History.Append(historyTypePrompt, this.Prompt.String())

	go CompletionRoutine(request, this.Butterfish.LLMClient,
		this.PromptAnswerWriter, this.PromptOutputChan,
		this.Color.Answer, this.Color.Error, this.StyleWriter)

	this.Prompt.Clear()
}

// Simplified CompletionRoutine - removed goal mode color logic
func CompletionRoutine(
	request *util.CompletionRequest,
	client LLM,
	writer io.Writer,
	outputChan chan *util.CompletionResponse,
	normalColor,
	errorColor string,
	styleWriter *util.StyleCodeblocksWriter,
) {
	writer.Write([]byte(normalColor))
	output, err := client.CompletionStream(request, writer)

	if err != nil {
		errStr := fmt.Sprintf("Error prompting LLM: %s\n", err)
		log.Printf("%s", errStr)
		if !strings.Contains(errStr, "context canceled") {
			fmt.Fprintf(writer, "%s%s", errorColor, errStr)
		}
	}

	if output == nil && err != nil {
		output = &util.CompletionResponse{Completion: err.Error()}
	}

	if styleWriter != nil {
		styleWriter.Reset()
	}

	outputChan <- output
}

// Removed RealizeAutosuggest, ShowAutosuggest, RefreshAutosuggest, ClearAutosuggest
// Removed getAutosuggestEncoder
// Removed RequestAutosuggest, RequestCancelableAutosuggest

func (this *ShellState) getPromptEncoder() *tiktoken.Tiktoken {
	if this.PromptEncoder == nil {
		modelName := this.Butterfish.Config.ShellPromptModel
		encoder, err := tiktoken.EncodingForModel(modelName)
		if err != nil {
			log.Printf("Warning: Error getting encoder for prompt model %s: %s", modelName, err)
			encoder, err = tiktoken.EncodingForModel(DEFAULT_PROMPT_ENCODER)
			if err != nil {
				panic(fmt.Sprintf("Error getting encoder for fallback prompt model %s: %s", modelName, err))
			}
		}
		this.PromptEncoder = encoder
	}
	return this.PromptEncoder
}

// Given an encoder, a string, and a maximum number of takens, we count the
// number of tokens in the string and truncate to the max tokens if the would
// exceed it. Returns the number of tokens, the truncated string, and a bool
// indicating whether the string was truncated.
func countAndTruncate(data string,
	encoder *tiktoken.Tiktoken,
	maxTokens int) (int, string, bool) {
	tokens := encoder.Encode(data, nil, nil)
	truncated := false
	if len(tokens) >= maxTokens {
		tokens = tokens[:maxTokens]
		data = encoder.Decode(tokens)
		truncated = true
	}

	return len(tokens), data, truncated
}

// Removed countChildPids and HasRunningChildren (simplifying state management)
