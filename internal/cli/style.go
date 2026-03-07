package cli

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Style struct {
	enabled bool
}

func NewStyle(mode string, out *os.File) Style {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "auto"
	}
	switch mode {
	case "always":
		return Style{enabled: true}
	case "never":
		return Style{enabled: false}
	default:
		info, err := out.Stat()
		if err != nil {
			return Style{enabled: false}
		}
		return Style{enabled: (info.Mode() & os.ModeCharDevice) != 0}
	}
}

func (s Style) Header(text string) string {
	return s.paint(text, "95", true)
}

func (s Style) Label(text string) string {
	return s.paint(text, "94", true)
}

func (s Style) Muted(text string) string {
	return s.paint(text, "90", false)
}

func (s Style) Warn(text string) string {
	return s.paint(text, "33", true)
}

func (s Style) paint(text, colorCode string, bold bool) string {
	if !s.enabled {
		return text
	}
	if bold {
		return "\033[" + colorCode + ";1m" + text + "\033[0m"
	}
	return "\033[" + colorCode + "m" + text + "\033[0m"
}

func FormatInt(n int64) string {
	s := strconv.FormatInt(n, 10)
	if len(s) <= 3 {
		return s
	}
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	out := make([]byte, 0, len(s)+len(s)/3)
	for i, ch := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, ch)
	}
	if neg {
		return "-" + string(out)
	}
	return string(out)
}

func FormatUSD(v float64) string {
	return fmt.Sprintf("$%.4f", v)
}
