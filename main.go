package main

import (
	"os"

	"github.com/runar-rkmedia/donotnet/cmd"
	"github.com/runar-rkmedia/donotnet/term"
)

func main() {
	if err := cmd.Execute(); err != nil {
		term.Errorf("%v", err)
		os.Exit(1)
	}
}
