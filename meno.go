package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	tcell "github.com/gdamore/tcell/v2"
)

func main() {
	flag.Parse()
	path := flag.Arg(0)
	m, err := NewMeno(path)
	if err != nil {
		log.Fatal(err)
	}
	if err := m.SetLogFile("/tmp/meno.log"); err != nil {
		log.Fatal(err)
	}
	defer m.Close()
	if err := m.Run(); err != nil {
		log.Fatal(err)
	}
}

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
	inLines []string

	w, h         int
	wrappedLines []string
	firstLine    int

	mode Mode

	quitC             chan struct{}
	eventC            chan tcell.Event
	searchResultC     chan searchResult
	quitActiveSearchC chan struct{}

	done            bool
	searchInput     []rune
	lastSearchInput []rune
	lastSearchMode  Mode

	activeSearch *runningSearch
}

func (m *Meno) Close() {
	m.inFile.Close()
	if m.logFile != nil {
		m.logFile.Close()
	}
}

func NewMeno(filePath string) (*Meno, error) {
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

		quitC:         make(chan struct{}),
		eventC:        make(chan tcell.Event),
		searchResultC: make(chan searchResult),
	}
	s.SetStyle(m.style)

	if m.inFile, err = os.Open(filePath); err != nil {
		return nil, fmt.Errorf("Open(%q): %v", filePath, err)
	}

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
	m.readFile()

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
			m.jumpToLine(len(m.wrappedLines))
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

	m.activeSearch = &runningSearch{
		query: string(m.searchInput),
		data: &indexedData{
			lines: m.wrappedLines,
		},
		resultC:       m.searchResultC,
		quitC:         m.quitActiveSearchC,
		startFromLine: startFromLine,
		searchUp:      mode == ModeSearchUp,
		maxResults:    1,
		logf:          m.logf,
	}
	go m.activeSearch.run()
}

type indexedData struct {
	lines []string
}

type searchResult struct {
	query      string
	lineNumber int
	finished   bool
}

func (sr searchResult) String() string {
	if sr.finished && sr.lineNumber == 0 {
		return fmt.Sprintf("query: %q has no further results", sr.query)
	}
	return fmt.Sprintf("query: %q is on display line %d", sr.query, sr.lineNumber)
}

type runningSearch struct {
	query         string
	data          *indexedData
	resultC       chan searchResult
	quitC         <-chan struct{}
	maxResults    int
	searchUp      bool
	startFromLine int
	logf          func(string, ...interface{})
}

func (p *runningSearch) run() {
	returned, max := 0, p.maxResults

	matchesLine := func(i int) bool {
		line := p.data.lines[i]

		// If the line contains the query, great!
		if strings.Contains(line, p.query) {
			return true
		}

		// Otherwise, concatenate a suffix to the string to see if the query
		// *starts* on the lineNumber i but isn't *entirely* on that line.

		suffix := ""
		{
			j := i + 1
			for len(suffix) < len(p.query) {
				if j > len(p.data.lines)-1 {
					break
				}
				suffix = fmt.Sprintf("%s%s", suffix, p.data.lines[j])
				j++
			}
			//p.logf("doSearch fetched %d suffix lines", j-i)
		}

		// However, if this suffix entirely has the query, then we return
		// false since line 'i' doesn't contain it.
		if strings.Contains(suffix, p.query) {
			return false
		}

		final := fmt.Sprintf("%s%s", p.data.lines[i], suffix)
		return strings.Contains(final, p.query)
	}

	keepGoing := func(i int) bool {
		select {
		case <-p.quitC:
			return false
		default:
		}

		if !matchesLine(i) {
			return true
		}

		p.resultC <- searchResult{
			query:      p.query,
			lineNumber: i,
		}
		returned++
		if max > 0 && returned >= max {
			return false
		}
		return true
	}

	if p.searchUp {
		p.logf("searching up from %d to 0", p.startFromLine)
		for i := p.startFromLine; i >= 0; i-- {
			if !keepGoing(i) {
				break
			}
		}
	} else {
		p.logf("searching down from %d to %d", p.startFromLine, len(p.data.lines)-1)
		for i := p.startFromLine; i < len(p.data.lines); i++ {
			if !keepGoing(i) {
				break
			}
		}
	}

	p.resultC <- searchResult{
		query:    p.query,
		finished: true,
	}
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
	return len(m.wrappedLines) - m.h + 1
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

	m.logf("Window resized to %dx%d - (re)building wrappedLines", m.w, m.h)
	m.wrappedLines = make([]string, 0, len(m.inLines))
	for _, line := range m.inLines {
		for len(line) > m.w {
			part := line[:m.w]
			line = line[m.w:]
			m.wrappedLines = append(m.wrappedLines, part)
		}
		// TODO: Include data to indicate if the newline character follows the
		// wrapped line or not.
		m.wrappedLines = append(m.wrappedLines, line)
	}
	m.firstLine = 0
	m.logf("wrappedLines contains %d lines", len(m.wrappedLines))
}

func (m *Meno) showScreen() {
	m.screen.Clear()
	row := 0

	// Leave the last line for the prompt.
	lastRow := m.h - 1

	for i := m.firstLine; i < len(m.wrappedLines); i++ {
		line := m.wrappedLines[i]
		col := 0
		for _, r := range []rune(line) {
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
	m.logf("Starting scan of input file")
	scanner := bufio.NewScanner(m.inFile)
	scanner.Split(bufio.ScanLines)

	for scanner.Scan() {
		m.inLines = append(m.inLines, scanner.Text())
	}
	m.logf("Read %d lines", len(m.inLines))
	return nil
}
