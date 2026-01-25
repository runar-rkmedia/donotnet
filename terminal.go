package main

import (
	"fmt"
	"io"
	"os"
	"time"
)

// ANSI color codes
const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorCyan   = "\033[36m"
	colorDim    = "\033[2m"
)

// Terminal provides colored output helpers
type Terminal struct {
	w       io.Writer
	verbose bool
}

// NewTerminal creates a Terminal that writes to stderr
func NewTerminal() *Terminal {
	return &Terminal{w: os.Stderr}
}

// SetVerbose enables or disables verbose output
func (t *Terminal) SetVerbose(v bool) {
	t.verbose = v
}

// Info prints an informational message in cyan (with newline)
func (t *Terminal) Info(format string, args ...any) {
	fmt.Fprintf(t.w, "%s"+format+"%s\n", append([]any{colorCyan}, append(args, colorReset)...)...)
}

// Dim prints a subdued message (with newline)
func (t *Terminal) Dim(format string, args ...any) {
	fmt.Fprintf(t.w, "%s"+format+"%s\n", append([]any{colorDim}, append(args, colorReset)...)...)
}

// Success prints a success message in green (with newline)
func (t *Terminal) Success(format string, args ...any) {
	fmt.Fprintf(t.w, "%s"+format+"%s\n", append([]any{colorGreen}, append(args, colorReset)...)...)
}

// Error prints an error message in red (with newline)
func (t *Terminal) Error(format string, args ...any) {
	fmt.Fprintf(t.w, "%s"+format+"%s\n", append([]any{colorRed}, append(args, colorReset)...)...)
}

// Errorf prints "error: " prefix followed by message in red (with newline)
func (t *Terminal) Errorf(format string, args ...any) {
	fmt.Fprintf(t.w, "%serror: "+format+"%s\n", append([]any{colorRed}, append(args, colorReset)...)...)
}

// Warn prints a warning message in yellow (with newline)
func (t *Terminal) Warn(format string, args ...any) {
	fmt.Fprintf(t.w, "%s"+format+"%s\n", append([]any{colorYellow}, append(args, colorReset)...)...)
}

// Warnf prints "warning: " prefix followed by message in yellow (with newline)
func (t *Terminal) Warnf(format string, args ...any) {
	fmt.Fprintf(t.w, "%swarning: "+format+"%s\n", append([]any{colorYellow}, append(args, colorReset)...)...)
}

// Verbose prints a dim message only if verbose mode is enabled (with newline)
func (t *Terminal) Verbose(format string, args ...any) {
	if t.verbose {
		fmt.Fprintf(t.w, "%s"+format+"%s\n", append([]any{colorDim}, append(args, colorReset)...)...)
	}
}

// Printf prints without color formatting (no automatic newline)
func (t *Terminal) Printf(format string, args ...any) {
	fmt.Fprintf(t.w, format, args...)
}

// Write writes raw bytes to the terminal
func (t *Terminal) Write(p []byte) (n int, err error) {
	return t.w.Write(p)
}

// Println prints without color formatting (with newline)
func (t *Terminal) Println(args ...any) {
	fmt.Fprintln(t.w, args...)
}

// ClearLine clears the current line
func (t *Terminal) ClearLine() {
	fmt.Fprintf(t.w, "\r\033[K")
}

// Status prints a status message that overwrites the current line
func (t *Terminal) Status(format string, args ...any) {
	fmt.Fprintf(t.w, "\r\033[K"+format, args...)
}

// ResultLine prints a test/build result line with checkmark/x and optional stats
func (t *Terminal) ResultLine(success bool, skipIndicator, paddedName, durationStr, stats, filterInfo string) {
	if success {
		if stats != "" {
			fmt.Fprintf(t.w, "  %s✓%s%s %s %s  %s%s\n", colorGreen, colorReset, skipIndicator, paddedName, durationStr, stats, filterInfo)
		} else {
			fmt.Fprintf(t.w, "  %s✓%s%s %s %s%s\n", colorGreen, colorReset, skipIndicator, paddedName, durationStr, filterInfo)
		}
	} else {
		if stats != "" {
			fmt.Fprintf(t.w, "  %s✗%s%s %s %s  %s\n", colorRed, colorReset, skipIndicator, paddedName, durationStr, stats)
		} else {
			fmt.Fprintf(t.w, "  %s✗%s%s %s %s\n", colorRed, colorReset, skipIndicator, paddedName, durationStr)
		}
	}
}

// CachedLine prints a cached project line (dim circle)
func (t *Terminal) CachedLine(name string) {
	fmt.Fprintf(t.w, "  %s○ %s (cached)%s\n", colorDim, name, colorReset)
}

// Summary prints the final summary line
func (t *Terminal) Summary(succeeded, total, cached int, duration time.Duration, success bool) {
	color := colorGreen
	if !success {
		color = colorRed
	}
	if cached > 0 {
		fmt.Fprintf(t.w, "\n%s%d/%d succeeded%s, %s%d cached%s (%s)\n", color, succeeded, total, colorReset, colorCyan, cached, colorReset, duration)
	} else {
		fmt.Fprintf(t.w, "\n%s%d/%d succeeded%s (%s)\n", color, succeeded, total, colorReset, duration)
	}
}

// SkipIndicator returns a formatted skip indicator string
func (t *Terminal) SkipIndicator(skippedBuild, skippedRestore bool) string {
	if skippedBuild {
		return fmt.Sprintf(" %s⚡%s", colorYellow, colorReset)
	} else if skippedRestore {
		return fmt.Sprintf(" %s↻%s", colorCyan, colorReset)
	}
	return "   " // three spaces to align with emoji (2-wide) + space
}

// term is the global terminal instance
var term = NewTerminal()
