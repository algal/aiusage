package cli

import (
	"strings"
)

func RenderTable(headers []string, rows [][]string) string {
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, row := range rows {
		for i := range headers {
			if i >= len(row) {
				continue
			}
			if len(row[i]) > widths[i] {
				widths[i] = len(row[i])
			}
		}
	}

	var b strings.Builder
	b.WriteString(renderRow(headers, widths))
	b.WriteByte('\n')
	b.WriteString(renderDivider(widths))
	for _, row := range rows {
		b.WriteByte('\n')
		b.WriteString(renderRow(row, widths))
	}
	return b.String()
}

func renderRow(cells []string, widths []int) string {
	parts := make([]string, len(widths))
	for i := range widths {
		value := ""
		if i < len(cells) {
			value = cells[i]
		}
		pad := widths[i] - len(value)
		if pad < 0 {
			pad = 0
		}
		parts[i] = value + strings.Repeat(" ", pad)
	}
	return strings.Join(parts, "  ")
}

func renderDivider(widths []int) string {
	parts := make([]string, len(widths))
	for i, w := range widths {
		parts[i] = strings.Repeat("-", w)
	}
	return strings.Join(parts, "  ")
}
