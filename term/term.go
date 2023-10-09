package term

import (
	"github.com/gdamore/tcell/v2"
	"github.com/golang/glog"

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

	activeSearch *activeSearch
}

type activeSearch struct {
	request       wrapper.SearchRequest
	startFromLine int
	searchDown    bool
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
	m.w, m.h = s.Size()

	glog.Infof("Window size set to %d x %d", m.w, m.h)

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
				glog.Infof("driver.Events closed; breaking Run")
				break outer
			}
			glog.V(1).Infof("Run() driver event %v", ev)
			m.handleDataEvent(ev)
		case ev := <-m.eventC:
			glog.V(1).Infof("Run() eventC event %v", ev)
			m.handleTermEvent(ev)
		}
	}
}

func (m *Meno) handleDataEvent(event wrapper.Event) {
	if line := event.Line; line != nil {
		row := line.Number - m.firstLine
		//glog.Infof("Writing %q to row %d", line.Line, row)
		col := 0
		for _, r := range []rune(line.Line) {
			m.screen.SetContent(col, row, r, nil, m.style)
			col++
		}
		for ; col < m.w; col++ {
			m.screen.SetContent(col, row, ' ', nil, m.style)
		}
		m.showScreen()
		return
	}
	if status := event.Search; status != nil {
		if !status.Complete {
			return
		}
		glog.Infof("Search status %v", status)
		m.mode = ModePaging
		m.showScreen()
		return
	}
	glog.Errorf("handleDataEvent unhandled %v", event)
}

func (m *Meno) handleTermEvent(event tcell.Event) {
	switch ev := event.(type) {
	case *tcell.EventResize:
		m.w, m.h = ev.Size()
		glog.Infof("EventResize %d x %d", m.w, m.h)
		m.resized()
		m.showScreen()
	case *tcell.EventKey:
		glog.Infof("EventKey %v for mode %v", ev, m.mode)
		switch m.mode {
		case ModePaging:
			m.keyDownPaging(ev)
		case ModeSearchUp, ModeSearchDown:
			m.keyDownSearch(ev)
		case ModeSearchActive:
			m.keyDownSearchActive(ev)
		default:
			glog.Errorf("EventKey %v for mode %v not handled", ev, m.mode)
		}
	}
}

/*
func (m *Meno) handleSearchResult(r searchResult) {
	glog.Infof("handleSearchResult %q", r)
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
	/*
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			for _ = range m.eventC {
			}
			wg.Done()
		}()
	*/

	glog.Infof("stopping driver")
	m.driver.Stop()
	//wg.Wait()

	glog.Infof("calling Fini")
	m.screen.Fini()
	glog.Infof("meno finished!")
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
			glog.Errorf("keyDownPaging unhandled rune %q", ev.Rune())
		}
	default:
		glog.Errorf("keyDownPaging unhandled EventKey %v", ev.Key())
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
		glog.Errorf("keyDownSearch unhandled EventKey %v", ev.Key())
	}
}

func (m *Meno) keyDownSearchActive(ev *tcell.EventKey) {
	switch ev.Key() {
	case tcell.KeyEscape, tcell.KeyCtrlC:
		m.changeMode(ModePaging)
	default:
		glog.Errorf("keyDownSearching unhandled EventKey %v", ev.Key())
	}
}

func (m *Meno) startSearch(oppositeDirection bool) {
	if len(m.searchInput) == 0 {
		glog.Errorf("startSearch called without searchInput set")
		return
	}
	mode := m.mode

	m.lastSearchInput = m.searchInput
	m.lastSearchMode = mode

	m.mode = ModeSearchActive
	m.showScreen()

	m.activeSearch = &activeSearch{
		request: wrapper.SearchRequest{
			Query: string(m.lastSearchInput),
		},
	}

	if oppositeDirection {
		if mode == ModeSearchUp {
			m.activeSearch.searchDown = true
			glog.Infof("Search was up but flipped to down")
		} else {
			m.activeSearch.searchDown = false
			glog.Infof("Search was down but flipped to up")
		}
	}

	if m.activeSearch.searchDown {
		m.activeSearch.startFromLine = m.firstLine + 1
	} else {
		m.activeSearch.startFromLine = m.firstLine - 1
	}

	if err := m.driver.Search(m.activeSearch.request); err != nil {
		glog.Fatal(err)
	}
}

/*

	resultC := make(chan searchResult)

	req := SearchRequest{
		Query:         string(m.searchInput),
		ResultC:       resultC,
		QuitC:         m.quitActiveSearchC,
		StartFromLine: startFromLine,
		SearchUp:      mode == ModeSearchUp,
		MaxResults:    1,
		Logf:          glog.Infof,
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
	return m.driver.TotalLines() - m.h + 1
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
	glog.Infof("jumpToLine %d", newPos)
	if newPos == m.firstLine {
		return
	}
	m.firstLine = newPos
	m.driver.WatchLines(m.firstLine, m.h-1)
}

func (m *Meno) jumpToLastLine() {
	m.jumpToLine(m.maxFirstLine())
}

func (m *Meno) resized() {
	// Update every visible cell.
	m.screen.Sync()

	m.driver.ResizeWindow(m.w)
	m.driver.WatchLines(m.firstLine, m.h-1)
	glog.Infof("Window resized (%d x %d)", m.w, m.h)

	// TODO: Adjust first line so that the first character of the previously
	// visible first line is still in the visible first line (somewhere, not
	// necessarily in at 0,0).

	/*
		if m.data == nil {
			glog.Infof("Starting scan of input file")
			m.data = NewIndexedData(m.inFile, m.w, m.maxQuery, glog.Infof)
			glog.Infof("Have %d visible lines", m.data.VisibleLines())
		} else {
			glog.Infof("Window resized to %dx%d - (re)building data", m.w, m.h)
			m.data.Resize(m.w)
			m.firstLine = 0
			glog.Infof("Have %d visible lines", m.data.VisibleLines())
		}
	*/
}

func (m *Meno) showScreen() {
	// Show only the last row
	row := m.h - 1

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
