//go:build windows

package main

import (
	"os"
	"strconv"

	goterm "golang.org/x/term"
)

func getTerminalWidth() int {
	width, _, err := goterm.GetSize(int(os.Stderr.Fd()))
	if err == nil && width > 0 {
		return width
	}

	// Fallback to COLUMNS env
	if cols := os.Getenv("COLUMNS"); cols != "" {
		if n, err := strconv.Atoi(cols); err == nil && n > 0 {
			return n
		}
	}

	return 80 // default
}
