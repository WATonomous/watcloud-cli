package du

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// Run browses home storage using the supplied interactive terminal streams.
func Run(ctx context.Context, in io.Reader, out io.Writer) (err error) {
	defer func() {
		if err != nil {
			err = printableError{err}
		}
	}()
	if err := requireTerminal(in, out); err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("find home directory: %w", err)
	}
	fs, err := openStorage(home, getAttribute)
	if err != nil {
		return err
	}
	return runBrowser(ctx, fs, in, out)
}

func runBrowser(ctx context.Context, fs *filesystem, in io.Reader, out io.Writer) error {
	m := newBrowser(ctx, fs)
	final, err := tea.NewProgram(m, tea.WithContext(ctx), tea.WithInput(in),
		tea.WithOutput(out), tea.WithAltScreen()).Run()
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	if err != nil {
		return err
	}
	return final.(browser).fatal
}

type position struct {
	path, selected string
}

type loaded struct {
	view            directory
	quota, homeUsed measurement
	parents         []position
	selected        string
	err             error
}

type browser struct {
	ctx             context.Context
	fs              *filesystem
	view            directory
	quota, homeUsed measurement
	parents         []position
	cursor, offset  int
	width, height   int
	loading         bool
	colored         bool
	status          string
	fatal           error
}

func newBrowser(ctx context.Context, fs *filesystem) browser {
	_, noColor := os.LookupEnv("NO_COLOR")
	return browser{ctx: ctx, fs: fs, width: 80, height: 24,
		loading: true, colored: !noColor && os.Getenv("TERM") != "dumb"}
}

func (m browser) Init() tea.Cmd { return m.load(position{path: "."}, nil) }

func (m browser) load(target position, parents []position) tea.Cmd {
	return func() tea.Msg {
		result := loaded{}
		for {
			var err error
			result.view, err = m.fs.load(m.ctx, target.path)
			if err == nil {
				break
			}
			if result.err == nil {
				result.err = err // Preserve the original failure after recovering.
			}
			if len(parents) == 0 {
				return result
			}
			target = parents[len(parents)-1]
			parents = parents[:len(parents)-1]
		}
		result.parents, result.selected = parents, target.selected
		result.quota = m.fs.quota()
		result.homeUsed = result.view.size
		if target.path != "." {
			result.homeUsed = m.fs.homeUsage()
		}
		return result
	}
}

func (m browser) position() position {
	return position{path: m.view.path, selected: m.selected().name}
}

func (m browser) selected() entry {
	if len(m.view.entries) == 0 {
		return entry{}
	}
	return m.view.entries[m.cursor]
}

func (m browser) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		if msg.Width > 0 && msg.Height > 0 {
			m.width, m.height = msg.Width, msg.Height
		}
	case loaded:
		m.loading = false
		m.status = ""
		if msg.err != nil {
			if msg.view.path == "" {
				m.fatal = msg.err
				return m, tea.Quit
			}
			var pathErr *os.PathError
			if errors.As(msg.err, &pathErr) {
				// Keep the cause visible even when the path exceeds the terminal width.
				m.status = fmt.Sprintf("Couldn't open folder: %s (%s)", safeText(pathErr.Err.Error()), safeText(pathErr.Path))
			} else {
				m.status = "Couldn't open folder: " + safeText(msg.err.Error())
			}
		}
		m.parents = msg.parents
		m.view, m.quota, m.homeUsed = msg.view, msg.quota, msg.homeUsed
		m.cursor, m.offset = 0, 0
		for i, item := range m.view.entries {
			if item.name == msg.selected {
				m.cursor = i
				break
			}
		}
	case tea.KeyMsg:
		key := msg.String()
		if key == "q" || key == "esc" || key == "ctrl+c" {
			return m, tea.Quit
		}
		if m.loading || m.view.path == "" {
			return m, nil
		}
		m.status = ""
		var target position
		parents := m.parents
		switch key {
		case "up", "k":
			m.cursor--
		case "down", "j":
			m.cursor++
		case "pgup":
			m.cursor -= m.rows()
		case "pgdown":
			m.cursor += m.rows()
		case "home":
			m.cursor = 0
		case "end":
			m.cursor = len(m.view.entries) - 1
		case "enter", "right":
			item := m.selected()
			if item.navigable() {
				parents = append(parents, m.position())
				target = position{path: filepath.Join(m.view.path, item.name)}
			}
		case "left", "backspace":
			if len(parents) > 0 {
				target = parents[len(parents)-1]
				parents = parents[:len(parents)-1]
			}
		case "r":
			target = m.position()
		}
		if target.path != "" {
			m.loading = true
			return m, m.load(target, parents)
		}
	}
	m.cursor = max(0, min(m.cursor, len(m.view.entries)-1))
	m.offset = max(0, min(m.offset, m.cursor, max(0, len(m.view.entries)-m.rows())))
	if m.cursor >= m.offset+m.rows() {
		m.offset = m.cursor - m.rows() + 1
	}
	return m, nil
}

func (m browser) rows() int { return max(1, m.height-11) }

type printableError struct{ cause error }

func (err printableError) Error() string {
	return ansi.Truncate(safeText(err.cause.Error()), 500, "…")
}
func (err printableError) Unwrap() error { return err.cause }
