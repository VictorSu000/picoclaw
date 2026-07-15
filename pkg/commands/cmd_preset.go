package commands

func presetCommand() Definition {
	return Definition{
		Name:        "preset",
		Description: "Select or run an agent preset",
		Usage:       "/preset [list|show <name>|use <name>|reset|run <name> <message>]",
	}
}
