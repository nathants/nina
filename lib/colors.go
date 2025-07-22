package lib

import (
	"fmt"
	"os"
	"regexp"
)

const (
	ColorRed     = "\033[31m"
	ColorGreen   = "\033[32m"
	ColorYellow  = "\033[33m"
	ColorBlue    = "\033[34m"
	ColorMagenta = "\033[35m"
	ColorCyan    = "\033[36m"
	ColorReset   = "\033[0m"
)

// ColoredStderr writes colored output to stderr
func ColoredStderr(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(os.Stderr, "%s%s%s\n", ColorCyan, msg, ColorReset)
}

// PrintYellowSeparator prints yellow equal signs separator (2 rows, 80 chars)
func PrintYellowSeparator() {
	separator := "================================================================================"
	fmt.Fprintf(os.Stderr, "%s%s%s\n", ColorYellow, separator, ColorReset)
}

// HighlightAllXMLTags highlights all XML tags in green
func HighlightAllXMLTags(text string) string {
	// Pattern to match all opening and closing XML tags like <something>, </something>
	pattern := `(</?[^>]+>)`
	re := regexp.MustCompile(pattern)

	return re.ReplaceAllStringFunc(text, func(match string) string {
		return ColorGreen + match + ColorReset
	})
}

// HighlightNinaTags highlights all XML tags in green first, then Nina tags in blue
func HighlightNinaTags(text string) string {
	// Pattern to match all XML tags
	allTagsPattern := `(</?[^>]+>)`
	allTagsRe := regexp.MustCompile(allTagsPattern)

	// Pattern to match Nina tags specifically
	ninaTagsPattern := `(</?Nina[^>]*>)`
	ninaTagsRe := regexp.MustCompile(ninaTagsPattern)

	return allTagsRe.ReplaceAllStringFunc(text, func(match string) string {
		// Check if this is a Nina tag
		if ninaTagsRe.MatchString(match) {
			return ColorBlue + match + ColorReset
		}
		// Otherwise, it's a regular XML tag - color it green
		return ColorGreen + match + ColorReset
	})
}
