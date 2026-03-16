package main

// wire_completion.go — thin wrapper over internal/completion.

import (
	"tetora/internal/completion"
)

func cmdCompletion(args []string) {
	completion.Run(args)
}

// Forwarding functions for tests (completion_test.go).
var (
	completionSubcommands              = completion.Subcommands
	completionSubActions               = completion.SubActions
	completionSubcommandDescriptions   = completion.SubcommandDescriptions
	completionSubActionDescriptions    = completion.SubActionDescriptions
	generateBashCompletion             = completion.GenerateBash
	generateZshCompletion              = completion.GenerateZsh
	generateFishCompletion             = completion.GenerateFish
)
