package du

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
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
	return runBrowser(ctx, fs, in, out.(term.File))
}

func runBrowser(ctx context.Context, fs *filesystem, in io.Reader, out term.File) error {
	ctx, cancel := context.WithCancelCause(ctx)
	m := newBrowser(ctx, fs)
	// A kernel read cannot be interrupted by context cancellation. Let it finish
	// before closing its descriptors, without delaying terminal restoration or
	// process exit. Queued commands check cancellation under the same lock.
	defer func() {
		cancel(nil)
		go func() {
			m.work.Lock()
			defer m.work.Unlock()
			fs.Close()
		}()
	}()
	output := &terminalOutput{File: out, cancel: cancel}
	final, err := tea.NewProgram(m, tea.WithContext(ctx), tea.WithInput(in),
		tea.WithOutput(output), tea.WithAltScreen()).Run()
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
	id             *identity
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
	work            *sync.Mutex
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
	return browser{ctx: ctx, fs: fs, work: &sync.Mutex{}, width: 80, height: 24,
		loading: true, colored: !noColor && os.Getenv("TERM") != "dumb"}
}

func (m browser) Init() tea.Cmd { return m.load(position{path: "."}, nil) }

func (m browser) load(target position, parents []position) tea.Cmd {
	return func() tea.Msg {
		m.work.Lock()
		defer m.work.Unlock()
		result := loaded{parents: parents, selected: target.selected}
		if result.err = m.ctx.Err(); result.err != nil {
			return result
		}
		result.view, result.err = m.fs.load(m.ctx, target.path, target.id)
		if result.err == nil {
			result.quota = m.fs.quota(m.ctx)
			result.homeUsed = result.view.size
			if target.path != "." {
				result.homeUsed = m.fs.homeUsage(m.ctx)
			}
			result.err = m.ctx.Err()
		}
		return result
	}
}

func (m browser) position() position {
	id := m.view.id
	return position{path: m.view.path, id: &id, selected: m.selected().name}
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
		if msg.err != nil {
			if m.view.path == "" {
				m.fatal = msg.err
				return m, tea.Quit
			}
			m.status = "Couldn't open this folder. Refresh or go back to try again."
			return m, nil
		}
		m.parents = msg.parents
		m.view, m.quota, m.homeUsed = msg.view, msg.quota, msg.homeUsed
		m.status, m.cursor, m.offset = "", 0, 0
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
				target = position{path: filepath.Join(m.view.path, item.name), id: &item.id}
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

// Bubble Tea ignores renderer write errors. Preserve the first failure and stop
// the program so Cobra can report a nonzero exit status.
type terminalOutput struct {
	term.File
	cancel context.CancelCauseFunc
}

var _ term.File = (*terminalOutput)(nil)

func (out *terminalOutput) Write(p []byte) (int, error) {
	n, err := out.File.Write(p)
	if err == nil && n < len(p) {
		err = io.ErrShortWrite
	}
	if err != nil {
		out.cancel(fmt.Errorf("write terminal output: %w", err))
	}
	return n, err
}
