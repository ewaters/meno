package data

import (
	"fmt"
	"os"
	"time"

	"github.com/gdamore/tcell/v2"
)

type Mode int

const (
	ModePaging Mode = iota
	ModeSearchDown
	ModeSearchUp
	ModeSearchActive
)

type Meno struct {
	screen tcell.Screen
	style  tcell.Style

	logFile *os.File
	inFile  *os.File

	w, h      int
	firstLine int
	maxQuery  int
	data      *IndexedData

	mode Mode

	quitC             chan struct{}
	eventC            chan tcell.Event
	searchResultC     chan searchResult
	quitActiveSearchC chan struct{}

	done            bool
	searchInput     []rune
	lastSearchInput []rune
	lastSearchMode  Mode

	activeSearch *SearchRequest
}

func (m *Meno) Close() {
	if m.logFile != nil {
		m.logFile.Close()
	}
}

func NewMeno(inFile *os.File, maxQuery int) (*Meno, error) {
	s, err := tcell.NewScreen()
	if err != nil {
		return nil, err
	}
	if err := s.Init(); err != nil {
		return nil, err
	}

	m := &Meno{
		screen: s,
		style:  tcell.StyleDefault.Background(tcell.ColorReset).Foreground(tcell.ColorReset),
		inFile: inFile,

		quitC:         make(chan struct{}),
		eventC:        make(chan tcell.Event),
		searchResultC: make(chan searchResult),

		maxQuery: maxQuery,
	}
	s.SetStyle(m.style)
	s.Clear()

	return m, nil
}

func (m *Meno) SetLogFile(path string) error {
	var err error
	if m.logFile, err = os.Create(path); err != nil {
		return err
	}
	return nil
}

func (m *Meno) logf(pattern string, args ...interface{}) {
	if m.logFile == nil {
		return
	}
	msg := fmt.Sprintf(pattern, args...)
	now := time.Now()
	fmt.Fprintf(m.logFile, "[%s] %s\n", now.Format("2006-01-02 15:04:05.000"), msg)
}

func (m *Meno) Run() error {
	go m.screen.ChannelEvents(m.eventC, m.quitC)

	for {
		select {
		case ev := <-m.eventC:
			m.handleEvent(ev)
		case r := <-m.searchResultC:
			m.handleSearchResult(r)
		}
	}
}

func (m *Meno) handleEvent(event tcell.Event) {
	switch ev := event.(type) {
	case *tcell.EventResize:
		m.w, m.h = ev.Size()
		m.resized()
		m.showScreen()
	case *tcell.EventKey:
		//m.logf("EventKey %v for mode %v", ev, m.mode)
		switch m.mode {
		case ModePaging:
			m.keyDownPaging(ev)
		case ModeSearchUp, ModeSearchDown:
			m.keyDownSearch(ev)
		case ModeSearchActive:
			m.keyDownSearchActive(ev)
		default:
			m.logf("EventKey %v for mode %v not handled", ev, m.mode)
		}
	}
}

func (m *Meno) handleSearchResult(r searchResult) {
	m.logf("handleSearchResult %q", r)
	if r.lineNumber != 0 {
		m.mode = ModePaging
		m.jumpToLine(r.lineNumber)
	}
	if r.finished {
		if m.mode == ModeSearchActive {
			m.changeMode(ModePaging)
		}
		m.quitActiveSearchC = nil
	}
}

func (m *Meno) finish() {
	m.screen.Fini()
	os.Exit(0)
	//m.quitC <- struct{}{}
	//m.done = true
}

func (m *Meno) keyDownPaging(ev *tcell.EventKey) {
	switch ev.Key() {
	case tcell.KeyEscape, tcell.KeyCtrlC:
		m.finish()
	case tcell.KeyDown:
		m.jumpLine(1)
	case tcell.KeyUp:
		m.jumpLine(-1)
	case tcell.KeyPgUp:
		m.jumpPage(-1)
	case tcell.KeyPgDn:
		m.jumpPage(1)
	case tcell.KeyRune:
		switch ev.Rune() {
		case 'q':
			m.finish()
		case 'g':
			m.jumpToLine(0)
		case 'G':
			m.jumpToLine(m.data.VisibleLines())
		case 'j':
			m.jumpLine(1)
		case 'k':
			m.jumpLine(-1)
		case ' ', 'f':
			m.jumpPage(1)
		case 'b':
			m.jumpPage(-1)
		case '/':
			m.changeMode(ModeSearchDown)
		case '?':
			m.changeMode(ModeSearchUp)
		case 'n':
			if len(m.lastSearchInput) > 0 {
				m.searchInput = m.lastSearchInput
				m.mode = m.lastSearchMode
				m.startSearch(false)
			}
		case 'N':
			if len(m.lastSearchInput) > 0 {
				m.searchInput = m.lastSearchInput
				m.mode = m.lastSearchMode
				m.startSearch(true)
			}
		default:
			m.logf("keyDownPaging unhandled rune %q", ev.Rune())
		}
	default:
		m.logf("keyDownPaging unhandled EventKey %v", ev.Key())
	}
}

func (m *Meno) keyDownSearch(ev *tcell.EventKey) {
	switch ev.Key() {
	case tcell.KeyEscape, tcell.KeyCtrlC:
		m.changeMode(ModePaging)
	case tcell.KeyEnter:
		m.startSearch(false)
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		l := len(m.searchInput)
		if l == 0 {
			return
		}
		m.updateSearch(m.searchInput[:l-1])
	case tcell.KeyRune:
		newInput := append([]rune{}, m.searchInput...)
		m.updateSearch(append(newInput, ev.Rune()))
	default:
		m.logf("keyDownSearch unhandled EventKey %v", ev.Key())
	}
}

func (m *Meno) keyDownSearchActive(ev *tcell.EventKey) {
	switch ev.Key() {
	case tcell.KeyEscape, tcell.KeyCtrlC:
		m.changeMode(ModePaging)
	default:
		m.logf("keyDownSearching unhandled EventKey %v", ev.Key())
	}
}

func (m *Meno) startSearch(oppositeDirection bool) {
	if len(m.searchInput) == 0 {
		m.logf("ERROR: startSearch called without searchInput set")
		return
	}
	mode := m.mode

	m.lastSearchInput = m.searchInput
	m.lastSearchMode = mode

	m.mode = ModeSearchActive
	m.showScreen()

	if m.quitActiveSearchC != nil {
		m.quitActiveSearchC <- struct{}{}
	}
	m.quitActiveSearchC = make(chan struct{})

	if oppositeDirection {
		if mode == ModeSearchUp {
			mode = ModeSearchDown
			m.logf("Search was up but flipped to down")
		} else {
			mode = ModeSearchUp
			m.logf("Search was down but flipped to up")
		}
	}

	startFromLine := m.firstLine + 1
	if mode == ModeSearchUp {
		startFromLine = m.firstLine - 1
	}

	resultC := make(chan searchResult)

	req := SearchRequest{
		Query:         string(m.searchInput),
		ResultC:       resultC,
		QuitC:         m.quitActiveSearchC,
		StartFromLine: startFromLine,
		SearchUp:      mode == ModeSearchUp,
		MaxResults:    1,
		Logf:          m.logf,
	}
	go m.data.Search(req)
	m.activeSearch = &req

	// TODO: Remove this once we have a proper event loop for running
	// operations.
	go func() {
		for result := range resultC {
			m.searchResultC <- result
		}
		m.searchResultC <- searchResult{
			finished: true,
		}
	}()
}

func (m *Meno) updateSearch(input []rune) {
	m.searchInput = input
	m.showScreen()
}

func (m *Meno) changeMode(mode Mode) {
	if m.mode == mode {
		return
	}
	switch m.mode {
	case ModeSearchActive:
		// The search is no longer active - forget what we were searching.
		m.searchInput = nil
	}
	m.mode = mode
	m.showScreen()
}

func (m *Meno) maxFirstLine() int {
	return m.data.VisibleLines() - m.h + 1
}

func (m *Meno) pageSize() int {
	return m.h - 1
}

func (m *Meno) jumpPage(distance int) {
	m.jumpLine(distance * m.pageSize())
}

func (m *Meno) jumpLine(distance int) {
	m.jumpToLine(m.firstLine + distance)
}

func (m *Meno) jumpToLine(newPos int) {
	if newPos < 0 {
		newPos = 0
	} else if newPos > m.maxFirstLine() {
		newPos = m.maxFirstLine()
	}
	if newPos == m.firstLine {
		return
	}
	m.firstLine = newPos
	m.showScreen()
}

func (m *Meno) resized() {
	// Update every visible cell.
	m.screen.Sync()

	if m.data == nil {
		m.logf("Starting scan of input file")
		m.data = NewIndexedData(m.inFile, m.w, m.maxQuery, m.logf)
		m.logf("Have %d visible lines", m.data.VisibleLines())
	} else {
		m.logf("Window resized to %dx%d - (re)building data", m.w, m.h)
		m.data.Resize(m.w)
		m.firstLine = 0
		m.logf("Have %d visible lines", m.data.VisibleLines())
	}
}

func (m *Meno) showScreen() {
	m.screen.Clear()
	row := 0

	// Leave the last line for the prompt.
	lastRow := m.h - 1

	for i := m.firstLine; i < m.data.VisibleLines(); i++ {
		vline := m.data.lines.Line(i)
		col := 0
		for _, r := range []rune(vline.line) {
			m.screen.SetContent(col, row, r, nil, m.style)
			col++
		}
		row++
		if row >= lastRow {
			break
		}
	}

	operator := ':'
	showOperator := true
	switch m.mode {
	case ModeSearchDown:
		operator = '/'
	case ModeSearchUp:
		operator = '?'
	case ModeSearchActive:
		showOperator = false
	}

	col := 0
	if showOperator {
		m.screen.SetContent(col, row, operator, nil, m.style)
		col++
	}

	switch m.mode {
	case ModeSearchDown, ModeSearchUp:
		for _, r := range m.searchInput {
			m.screen.SetContent(col, row, r, nil, m.style)
			col++
		}
	case ModeSearchActive:
		for _, r := range "Searching..." {
			m.screen.SetContent(col, row, r, nil, m.style)
			col++
		}
	}

	m.screen.ShowCursor(col, row)
	m.screen.Show()
}

func (m *Meno) readFile() error {
	return nil
}
