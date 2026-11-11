package du

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

func (m browser) View() string {
	if m.width < 48 || m.height < 12 {
		return m.fit("Home storage\nMake the terminal larger to browse.\nq Quit")
	}
	title := m.paint("Home storage", "1;38;2;16;128;255")
	if m.view.path == "" {
		return m.fit(title + "\n\nLoading your home folder…\n\nq Quit")
	}
	var lines []string
	lines = append(lines, "  "+title, "  "+m.paint(strings.Repeat("─", m.width-4), "2"))
	home := "Home: " + sizeText(m.homeUsed) + " used"
	switch {
	case m.quota.err != nil:
		home += " · limit unavailable"
	case m.quota.bytes == 0:
		home += " · no storage limit"
	default:
		home = fmt.Sprintf("Home: %s / %s used", sizeText(m.homeUsed), formatBytes(m.quota.bytes))
		if m.homeUsed.err == nil {
			home += " · " + percent(m.homeUsed.bytes, m.quota.bytes)
		}
	}
	path := filepath.Join("~", m.view.path)
	showPercent := m.quota.err == nil && m.quota.bytes > 0
	folder := "This folder: " + sizeText(m.view.size)
	if showPercent && m.view.size.err == nil {
		folder += " · " + percent(m.view.size.bytes, m.quota.bytes) + " of home limit"
	}
	lines = append(lines, "  "+home, "  "+m.paint("↳ "+safeText(path), "1;38;2;16;128;255"),
		"  "+folder, "")
	nameWidth := m.width - 16
	if showPercent {
		nameWidth -= 9
	}
	row := func(marker, name, size, pct string) string {
		name = ansi.Truncate(name, nameWidth, "…")
		name += strings.Repeat(" ", max(0, nameWidth-ansi.StringWidth(name)))
		text := fmt.Sprintf("  %s %s  %10s", marker, name, size)
		if showPercent {
			text += fmt.Sprintf("  %7s", pct)
		}
		return text
	}
	lines = append(lines, m.paint(row(" ", "Name", "Size", "Home %"), "2"))
	end := min(len(m.view.entries), m.offset+m.rows())
	for i := m.offset; i < end; i++ {
		item := m.view.entries[i]
		name := safeText(item.name)
		if item.kind == "directory" {
			name += "/"
		} else if item.kind == "symlink" {
			name += "@"
		}
		pct := "—"
		if showPercent && item.size.err == nil {
			pct = percent(item.size.bytes, m.quota.bytes)
		}
		marker := " "
		if i == m.cursor {
			marker = "›"
		}
		text := row(marker, name, sizeText(item.size), pct)
		if i == m.cursor {
			text = m.paint(text, "1;38;2;16;128;255")
		}
		lines = append(lines, text)
	}
	if len(m.view.entries) == 0 {
		lines = append(lines, "    This folder is empty.")
	}
	status := m.status
	if status == "" {
		status = m.detail()
	}
	footer := []string{
		"Scroll/↑/↓ Move  Enter/→ Open  Backspace/← Back",
		"r Refresh  q/Esc/Ctrl+C Quit",
	}
	if m.loading {
		status, footer = "Loading folder…", []string{"q/Esc/Ctrl+C Quit"}
	}
	lines = append(lines, "", "  "+status)
	for _, hint := range footer {
		lines = append(lines, "  "+m.paint(hint, "2"))
	}
	return m.fit(strings.Join(lines, "\n"))
}

func (m browser) detail() string {
	item := m.selected()
	switch {
	case item.note != "" && (item.kind == "directory" || item.kind == "file" || item.kind == "symlink"):
		return "Other or unverified storage · not included in home usage"
	case item.note != "" && item.kind == "fifo":
		return "Pipe · contents are not opened"
	case item.note != "" && item.kind == "socket":
		return "Socket · contents are not opened"
	case item.note != "":
		return "Device or special file · contents are not opened"
	case errors.Is(item.size.err, os.ErrNotExist):
		return "This item was removed · r to refresh"
	case errors.Is(item.size.err, os.ErrPermission):
		return "Can't read this item · r to retry"
	case errors.Is(item.size.err, context.DeadlineExceeded):
		return "Reading this size took too long · r to retry"
	case item.size.err != nil:
		return "Size unavailable · r to retry"
	case item.kind == "symlink":
		return "Link · its target is not followed"
	case m.view.warning != nil:
		return "Some items couldn't be listed · r to retry"
	case m.quota.err != nil:
		return "Home limit unavailable · r to retry"
	case m.homeUsed.err != nil || m.view.size.err != nil:
		return "Usage unavailable · r to retry"
	default:
		return fmt.Sprintf("%d items · largest first", len(m.view.entries))
	}
}

func (m browser) paint(text, code string) string {
	if !m.colored {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func (m browser) fit(text string) string {
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], max(1, m.width), "…")
	}
	return strings.Join(lines[:min(len(lines), max(1, m.height))], "\n")
}

// Keep the original names for navigation; escape only their display text.
func safeText(text string) string {
	quoted := strconv.QuoteToGraphic(text)
	return quoted[1 : len(quoted)-1]
}

func sizeText(size measurement) string {
	if size.err != nil {
		return "?"
	}
	return formatBytes(size.bytes)
}

func percent(bytes, quota uint64) string {
	if quota == 0 {
		return "—"
	}
	return fmt.Sprintf("%.1f%%", float64(bytes)/float64(quota)*100)
}

func formatBytes(bytes uint64) string {
	const units = "KMGTPE"
	value := float64(bytes)
	for i := range units {
		if value < 1024 {
			break
		}
		value /= 1024
		if value < 1024 || i == len(units)-1 {
			return fmt.Sprintf("%.2f %ciB", value, units[i])
		}
	}
	return fmt.Sprintf("%d B", bytes)
}
