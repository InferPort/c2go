package console

import (
	"fmt"
	"time"
)

// ANSI colors
const (
	ColorReset = "\033[0m"
	ColorRed   = "\033[31m"
	ColorGreen = "\033[32m"
	ColorCyan  = "\033[36m"
)

// PrintBanner prints a generic banner
func PrintBanner(title string) {
	fmt.Println("==================================================")
	fmt.Printf("  %s\n", title)
	fmt.Println("==================================================")
	fmt.Println()
}

// PrintSection prints a section title
func PrintSection(title string) {
	fmt.Printf("\n[%s]\n", title)
}

// PrintPrompt prints a prompt label in Cyan
func PrintPrompt(label string) {
	fmt.Printf("> %s%s%s: ", ColorCyan, label, ColorReset)
}

// OK prints an OK status
func OK() {
	fmt.Printf("[%s OK %s]\n", ColorGreen, ColorReset)
}

// Fail prints a FAIL status
func Fail() {
	fmt.Printf("[%s FAIL %s]\n", ColorRed, ColorReset)
}

// LogInfo prints an info message with a timestamp
func LogInfo(format string, a ...interface{}) {
	msg := fmt.Sprintf(format, a...)
	fmt.Printf("%s [ INFO ] %s\n", time.Now().Format("2006-01-02 15:04:05"), msg)
}

// LogSuccess prints a success message with a timestamp
func LogSuccess(format string, a ...interface{}) {
	msg := fmt.Sprintf(format, a...)
	fmt.Printf("%s [%s OK %s] %s\n", time.Now().Format("2006-01-02 15:04:05"), ColorGreen, ColorReset, msg)
}

// LogError prints an error message with a timestamp
func LogError(format string, a ...interface{}) {
	msg := fmt.Sprintf(format, a...)
	fmt.Printf("%s [%s FAIL %s] %s\n", time.Now().Format("2006-01-02 15:04:05"), ColorRed, ColorReset, msg)
}

// LogWait prints a waiting status message
func LogWait(format string, a ...interface{}) {
	msg := fmt.Sprintf(format, a...)
	fmt.Printf("%s [%s WAIT %s] %s\n", time.Now().Format("2006-01-02 15:04:05"), ColorCyan, ColorReset, msg)
}
