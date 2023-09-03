package term

import (
	"log"

	"github.com/gdamore/tcell/v2"

	"github.com/ewaters/meno/blocks"
	"github.com/ewaters/meno/wrapper"
)

type Mode int

const (
	ModePaging Mode = iota
	ModeSearchDown
	ModeSearchUp
	ModeSearchActive
)

type Meno struct {
	config MenoConfig
	screen tcell.Screen
	style  tcell.Style
	driver *wrapper.Driver

	w, h      int
	firstLine int

	mode Mode

	quitC  chan struct{}
	eventC chan tcell.Event

	done            bool
	searchInput     []rune
	lastSearchInput []rune
	lastSearchMode  Mode
}

func (m *Meno) Close() {
}

type MenoConfig struct {
	blocks.Config
	LineSeperator []byte
}

func NewMeno(config MenoConfig, s tcell.Screen) (*Meno, error) {
	if err := s.Init(); err != nil {
		return nil, err
	}

	reader, err := blocks.NewReader(config.Config)
	if err != nil {
		return nil, err
	}

	driver, err := wrapper.NewDriver(reader, config.LineSeperator)
	if err != nil {
		return nil, err
	}

	m := &Meno{
		config: config,
		screen: s,
		style:  tcell.StyleDefault.Background(tcell.ColorReset).Foreground(tcell.ColorReset),
		driver: driver,

		quitC:  make(chan struct{}),
		eventC: make(chan tcell.Event),
	}
	s.SetStyle(m.style)
	s.Clear()

	m.w, m.h = m.screen.Size()
	m.resized()

	return m, nil
}

func (m *Meno) Run() {
	go m.screen.ChannelEvents(m.eventC, m.quitC)
	go m.driver.Run()

outer:
	for {
		select {
		case ev, ok := <-m.driver.Events():
			if !ok {
				log.Printf("driver.Events closed; breaking Run")
				break outer
			}
			m.handleDataEvent(ev)
		case ev := <-m.eventC:
			m.handleTermEvent(ev)
		}
	}
}

func (m *Meno) handleDataEvent(event wrapper.Event) {
	log.Printf("handleDataEvent %v", event)
	// TODO
}

func (m *Meno) handleTermEvent(event tcell.Event) {
	switch ev := event.(type) {
	case *tcell.EventResize:
		m.w, m.h = ev.Size()
		m.resized()
		m.showScreen()
	case *tcell.EventKey:
		log.Printf("EventKey %v for mode %v", ev, m.mode)
		switch m.mode {
		case ModePaging:
			m.keyDownPaging(ev)
		case ModeSearchUp, ModeSearchDown:
			m.keyDownSearch(ev)
		case ModeSearchActive:
			m.keyDownSearchActive(ev)
		default:
			log.Printf("EventKey %v for mode %v not handled", ev, m.mode)
		}
	}
}

/*
func (m *Meno) handleSearchResult(r searchResult) {
	log.Printf("handleSearchResult %q", r)
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
*/

func (m *Meno) finish() {
	log.Printf("calling Fini")
	m.screen.Fini()
	log.Printf("stopping driver")
	m.driver.Stop()
	log.Printf("meno finished!")
	//os.Exit(0)
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
			m.jumpToLastLine()
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
			log.Printf("keyDownPaging unhandled rune %q", ev.Rune())
		}
	default:
		log.Printf("keyDownPaging unhandled EventKey %v", ev.Key())
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
		log.Printf("keyDownSearch unhandled EventKey %v", ev.Key())
	}
}

func (m *Meno) keyDownSearchActive(ev *tcell.EventKey) {
	switch ev.Key() {
	case tcell.KeyEscape, tcell.KeyCtrlC:
		m.changeMode(ModePaging)
	default:
		log.Printf("keyDownSearching unhandled EventKey %v", ev.Key())
	}
}

func (m *Meno) startSearch(oppositeDirection bool) {
	// TODO
}

/*
	if len(m.searchInput) == 0 {
		log.Printf("ERROR: startSearch called without searchInput set")
		return
	}
	mode := m.mode

	m.lastSearchInput = m.searchInput
	m.lastSearchMode = mode

	m.mode = ModeSearchActive
	m.showScreen()

	if oppositeDirection {
		if mode == ModeSearchUp {
			mode = ModeSearchDown
			log.Printf("Search was up but flipped to down")
		} else {
			mode = ModeSearchUp
			log.Printf("Search was down but flipped to up")
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
		Logf:          log.Printf,
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
*/

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
	// TODO
	return -1
	//return m.data.VisibleLines() - m.h + 1
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

func (m *Meno) jumpToLastLine() {
	// TODO
}

func (m *Meno) resized() {
	// Update every visible cell.
	m.screen.Sync()

	m.driver.ResizeWindow(m.w)
	m.driver.WatchLines(m.firstLine, m.h)
	log.Printf("Window resized")

	// TODO: Adjust first line so that the first character of the previously
	// visible first line is still in the visible first line (somewhere, not
	// necessarily in at 0,0).

	/*
		if m.data == nil {
			log.Printf("Starting scan of input file")
			m.data = NewIndexedData(m.inFile, m.w, m.maxQuery, log.Printf)
			log.Printf("Have %d visible lines", m.data.VisibleLines())
		} else {
			log.Printf("Window resized to %dx%d - (re)building data", m.w, m.h)
			m.data.Resize(m.w)
			m.firstLine = 0
			log.Printf("Have %d visible lines", m.data.VisibleLines())
		}
	*/
}

func (m *Meno) showScreen() {
	m.screen.Clear()
	row := 0

	/*
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
	*/

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
