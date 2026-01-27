// Package term provides colored terminal output helpers.
package term

import (
	"fmt"
	"io"
	"os"
	"time"

	goterm "golang.org/x/term"
)

// ANSI color codes
const (
	ColorReset  = "\033[0m"
	ColorRed    = "\033[31m"
	ColorGreen  = "\033[32m"
	ColorYellow = "\033[33m"
	ColorCyan   = "\033[36m"
	ColorDim    = "\033[2m"
)

// Terminal provides colored output helpers
type Terminal struct {
	w        io.Writer
	Verbose  bool // exported for checking verbose state
	plain    bool // when true, disable all ANSI codes
	progress bool // when true, show progress indicators
	isTTY    bool // true if stderr is a terminal
}

// New creates a Terminal that writes to stderr
func New() *Terminal {
	isTTY := goterm.IsTerminal(int(os.Stderr.Fd()))
	return &Terminal{
		w:        os.Stderr,
		isTTY:    isTTY,
		plain:    !isTTY, // default to plain mode if not a TTY
		progress: isTTY,  // default to progress only if TTY
	}
}

// SetPlain enables or disables plain mode (no ANSI codes)
func (t *Terminal) SetPlain(p bool) {
	t.plain = p
}

// SetProgress enables or disables progress indicators
func (t *Terminal) SetProgress(p bool) {
	t.progress = p
}

// IsTTY returns whether the terminal is interactive
func (t *Terminal) IsTTY() bool {
	return t.isTTY
}

// IsPlain returns whether plain mode is enabled
func (t *Terminal) IsPlain() bool {
	return t.plain
}

// ShowProgress returns whether progress indicators should be shown
func (t *Terminal) ShowProgress() bool {
	return t.progress
}

// SetVerbose enables or disables verbose output
func (t *Terminal) SetVerbose(v bool) {
	t.Verbose = v
}

// Info prints an informational message in cyan (with newline)
func (t *Terminal) Info(format string, args ...any) {
	if t.plain {
		fmt.Fprintf(t.w, format+"\n", args...)
	} else {
		fmt.Fprintf(t.w, "%s"+format+"%s\n", append([]any{ColorCyan}, append(args, ColorReset)...)...)
	}
}

// Dim prints a subdued message (with newline)
func (t *Terminal) Dim(format string, args ...any) {
	if t.plain {
		fmt.Fprintf(t.w, format+"\n", args...)
	} else {
		fmt.Fprintf(t.w, "%s"+format+"%s\n", append([]any{ColorDim}, append(args, ColorReset)...)...)
	}
}

// Success prints a success message in green (with newline)
func (t *Terminal) Success(format string, args ...any) {
	if t.plain {
		fmt.Fprintf(t.w, format+"\n", args...)
	} else {
		fmt.Fprintf(t.w, "%s"+format+"%s\n", append([]any{ColorGreen}, append(args, ColorReset)...)...)
	}
}

// Error prints an error message in red (with newline)
func (t *Terminal) Error(format string, args ...any) {
	if t.plain {
		fmt.Fprintf(t.w, format+"\n", args...)
	} else {
		fmt.Fprintf(t.w, "%s"+format+"%s\n", append([]any{ColorRed}, append(args, ColorReset)...)...)
	}
}

// Errorf prints "error: " prefix followed by message in red (with newline)
func (t *Terminal) Errorf(format string, args ...any) {
	if t.plain {
		fmt.Fprintf(t.w, "error: "+format+"\n", args...)
	} else {
		fmt.Fprintf(t.w, "%serror: "+format+"%s\n", append([]any{ColorRed}, append(args, ColorReset)...)...)
	}
}

// Warn prints a warning message in yellow (with newline)
func (t *Terminal) Warn(format string, args ...any) {
	if t.plain {
		fmt.Fprintf(t.w, format+"\n", args...)
	} else {
		fmt.Fprintf(t.w, "%s"+format+"%s\n", append([]any{ColorYellow}, append(args, ColorReset)...)...)
	}
}

// Warnf prints "warning: " prefix followed by message in yellow (with newline)
func (t *Terminal) Warnf(format string, args ...any) {
	if t.plain {
		fmt.Fprintf(t.w, "warning: "+format+"\n", args...)
	} else {
		fmt.Fprintf(t.w, "%swarning: "+format+"%s\n", append([]any{ColorYellow}, append(args, ColorReset)...)...)
	}
}

// VerboseLog prints a dim message only if verbose mode is enabled (with newline)
func (t *Terminal) VerboseLog(format string, args ...any) {
	if t.Verbose {
		if t.plain {
			fmt.Fprintf(t.w, format+"\n", args...)
		} else {
			fmt.Fprintf(t.w, "%s"+format+"%s\n", append([]any{ColorDim}, append(args, ColorReset)...)...)
		}
	}
}

// Printf prints without color formatting (no automatic newline)
func (t *Terminal) Printf(format string, args ...any) {
	fmt.Fprintf(t.w, format, args...)
}

// Color returns the color code if not in plain mode, empty string otherwise
func (t *Terminal) Color(code string) string {
	if t.plain {
		return ""
	}
	return code
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
	if !t.plain {
		fmt.Fprintf(t.w, "\r\033[K")
	}
}

// Status prints a status message that overwrites the current line
func (t *Terminal) Status(format string, args ...any) {
	if !t.progress {
		return // progress disabled, skip status updates
	}
	if t.plain {
		// In plain mode, don't overwrite - just print with newline
		fmt.Fprintf(t.w, format+"\n", args...)
	} else {
		fmt.Fprintf(t.w, "\r\033[K"+format, args...)
	}
}

// ResultLine prints a test/build result line with checkmark/x and optional stats
func (t *Terminal) ResultLine(success bool, skipIndicator, paddedName, durationStr, stats, filterInfo string) {
	if t.plain {
		// Plain text version
		status := "PASS"
		if !success {
			status = "FAIL"
		}
		if stats != "" {
			fmt.Fprintf(t.w, "  %s %s %s  %s%s\n", status, paddedName, durationStr, stats, filterInfo)
		} else {
			fmt.Fprintf(t.w, "  %s %s %s%s\n", status, paddedName, durationStr, filterInfo)
		}
	} else {
		if success {
			if stats != "" {
				fmt.Fprintf(t.w, "  %s✓%s%s %s %s  %s%s\n", ColorGreen, ColorReset, skipIndicator, paddedName, durationStr, stats, filterInfo)
			} else {
				fmt.Fprintf(t.w, "  %s✓%s%s %s %s%s\n", ColorGreen, ColorReset, skipIndicator, paddedName, durationStr, filterInfo)
			}
		} else {
			if stats != "" {
				fmt.Fprintf(t.w, "  %s✗%s%s %s %s  %s\n", ColorRed, ColorReset, skipIndicator, paddedName, durationStr, stats)
			} else {
				fmt.Fprintf(t.w, "  %s✗%s%s %s %s\n", ColorRed, ColorReset, skipIndicator, paddedName, durationStr)
			}
		}
	}
}

// CachedLine prints a cached project line (dim circle)
func (t *Terminal) CachedLine(name string) {
	if t.plain {
		fmt.Fprintf(t.w, "  CACHED %s\n", name)
	} else {
		fmt.Fprintf(t.w, "  %s○ %s (cached)%s\n", ColorDim, name, ColorReset)
	}
}

// Summary prints the final summary line
func (t *Terminal) Summary(succeeded, total, cached int, duration time.Duration, success bool) {
	if t.plain {
		if cached > 0 {
			fmt.Fprintf(t.w, "\n%d/%d succeeded, %d cached (%s)\n", succeeded, total, cached, duration)
		} else {
			fmt.Fprintf(t.w, "\n%d/%d succeeded (%s)\n", succeeded, total, duration)
		}
	} else {
		color := ColorGreen
		if !success {
			color = ColorRed
		}
		if cached > 0 {
			fmt.Fprintf(t.w, "\n%s%d/%d succeeded%s, %s%d cached%s (%s)\n", color, succeeded, total, ColorReset, ColorCyan, cached, ColorReset, duration)
		} else {
			fmt.Fprintf(t.w, "\n%s%d/%d succeeded%s (%s)\n", color, succeeded, total, ColorReset, duration)
		}
	}
}

// SkipIndicator returns a formatted skip indicator string
// Note: ⚡ renders wider than ↻ in most terminal fonts, so we add a space after ↻ to align
func (t *Terminal) SkipIndicator(skippedBuild, skippedRestore bool) string {
	if t.plain {
		if skippedBuild {
			return " [no-build]"
		} else if skippedRestore {
			return " [no-restore]"
		}
		return ""
	}
	if skippedBuild {
		return fmt.Sprintf(" %s⚡%s", ColorYellow, ColorReset)
	} else if skippedRestore {
		return fmt.Sprintf(" %s↻%s ", ColorCyan, ColorReset) // extra space to match ⚡ width
	}
	return "   " // three spaces to align with " ⚡" (space + wide emoji)
}

// Default is the default terminal instance used by package-level functions
var Default = New()

// Package-level functions that delegate to Default

func SetPlain(p bool)                  { Default.SetPlain(p) }
func SetProgress(p bool)               { Default.SetProgress(p) }
func SetVerbose(v bool)                { Default.SetVerbose(v) }
func IsVerbose() bool                  { return Default.Verbose }
func IsTTY() bool                      { return Default.IsTTY() }
func IsPlain() bool                    { return Default.IsPlain() }
func ShowProgress() bool               { return Default.ShowProgress() }
func Info(format string, args ...any)  { Default.Info(format, args...) }
func Dim(format string, args ...any)   { Default.Dim(format, args...) }
func Success(format string, args ...any) { Default.Success(format, args...) }
func Error(format string, args ...any) { Default.Error(format, args...) }
func Errorf(format string, args ...any) { Default.Errorf(format, args...) }
func Warn(format string, args ...any)  { Default.Warn(format, args...) }
func Warnf(format string, args ...any) { Default.Warnf(format, args...) }
func Verbose(format string, args ...any) { Default.VerboseLog(format, args...) }
func Printf(format string, args ...any) { Default.Printf(format, args...) }
func Color(code string) string         { return Default.Color(code) }
func Write(p []byte) (int, error)      { return Default.Write(p) }
func Println(args ...any)              { Default.Println(args...) }
func ClearLine()                       { Default.ClearLine() }
func Status(format string, args ...any) { Default.Status(format, args...) }
func ResultLine(success bool, skipIndicator, paddedName, durationStr, stats, filterInfo string) {
	Default.ResultLine(success, skipIndicator, paddedName, durationStr, stats, filterInfo)
}
func CachedLine(name string) { Default.CachedLine(name) }
func Summary(succeeded, total, cached int, duration time.Duration, success bool) {
	Default.Summary(succeeded, total, cached, duration, success)
}
func SkipIndicator(skippedBuild, skippedRestore bool) string {
	return Default.SkipIndicator(skippedBuild, skippedRestore)
}
