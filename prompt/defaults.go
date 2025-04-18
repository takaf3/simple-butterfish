package prompt

const (
	// Removed PromptFixCommand
	// Removed PromptSummarize
	// Removed PromptSummarizeFacts
	// Removed PromptSummarizeListOfFacts
	// Removed PromptGenerateCommand
	// Removed PromptQuestion
	PromptSystemMessage = "prompt_system_message"
	// Removed ShellAutosuggestCommand
	// Removed ShellAutosuggestNewCommand
	// Removed ShellAutosuggestPrompt
	ShellSystemMessage = "shell_system_message"
	// Removed GoalModeSystemMessage
)

// These are the default prompts used for Butterfish, they will be written
// to the prompts.yaml file every time Butterfish is loaded, unless the
// OkToReplace field (in the yaml file) is false.

var DefaultPrompts []Prompt = []Prompt{

	{
		Name:        PromptSystemMessage, // This might be unused now, consider removing later if appropriate
		Prompt:      "You are an assistant that helps the user in a Unix shell. Make your answers technical but succinct.",
		OkToReplace: true,
	},

	{
		Name:        ShellSystemMessage,
		Prompt:      "You are an helpful ssistant lives in a Unix shell. Your response should be accurate and concise. You don't need to explain in details unless asked so. System info about the local machine: '{sysinfo}'",
		OkToReplace: true,
	},

	// Removed GoalModeSystemMessage prompt
	// Removed ShellAutosuggestCommand prompt
	// Removed ShellAutosuggestNewCommand prompt
	// Removed ShellAutosuggestPrompt prompt
	// Removed PromptFixCommand prompt
	// Removed PromptSummarize prompt
	// Removed PromptSummarizeFacts prompt
	// Removed PromptSummarizeListOfFacts prompt
	// Removed PromptGenerateCommand prompt
	// Removed PromptQuestion prompt
}
