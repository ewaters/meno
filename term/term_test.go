package term

import (
	"fmt"
	"io"
	"log"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ewaters/meno/blocks"
	"github.com/gdamore/tcell/v2"
)

func init() {
	log.SetFlags(log.Lmicroseconds | log.Lshortfile)
}

type lineMatch struct {
	number int
	match  string
}

func (lm lineMatch) String() string {
	return fmt.Sprintf("[%d] /^%s *$/", lm.number, lm.match)
}

type lineMatches []lineMatch

func (lm lineMatches) String() string {
	var sb strings.Builder
	for _, l := range lm {
		sb.WriteString(l.String())
		sb.WriteRune('\n')
	}
	return sb.String()
}

type lineState struct {
	number int
	cells  []tcell.SimCell
}

func (ls lineState) asString() string {
	var sb strings.Builder
	for _, cell := range ls.cells {
		for _, r := range cell.Runes {
			sb.WriteRune(r)
		}
	}
	str := sb.String()
	str = strings.TrimRight(str, " ")
	return str
}

func (ls lineState) String() string {
	return fmt.Sprintf("[%d] %q", ls.number, ls.asString())
}

func (ls lineState) matches(t *testing.T, match lineMatch) bool {
	if match.match != "" {
		// Compile this as a regex and match it against the string of the line.
		re, err := regexp.Compile(match.match)
		if err != nil {
			t.Fatalf("Match %v failed to compile: %v", match, err)
		}
		return re.MatchString(ls.asString())
	}
	return true
}

type screenState struct {
	lines []lineState
}

func (ss screenState) String() string {
	var sb strings.Builder
	for _, l := range ss.lines {
		sb.WriteString(l.String())
		sb.WriteRune('\n')
	}
	return sb.String()
}

func (ss screenState) matches(t *testing.T, want lineMatches) bool {
	t.Helper()
	for _, match := range want {
		if match.number > len(ss.lines)-1 {
			t.Fatalf("lineMatch #%d is out of bounds of screenState.lines %d", match.number, len(ss.lines))
		}
		line := ss.lines[match.number]
		if !line.matches(t, match) {
			return false
		}
	}
	return true
}

func getScreenState(screen tcell.SimulationScreen) screenState {
	cells, w, h := screen.GetContents()

	var lines []lineState
	for y := 0; y < h; y++ {
		line := lineState{}
		for x := 0; x < w; x++ {
			idx := x + (y * w)
			line.cells = append(line.cells, cells[idx])
			line.number = y
		}
		lines = append(lines, line)
	}
	return screenState{
		lines: lines,
	}
}

func assertScreen(t *testing.T, screen tcell.SimulationScreen, want lineMatches) {
	t.Helper()

	const (
		totalDelay = 1 * time.Second
		loopDelay  = 10 * time.Millisecond
	)
	remainingDelay := totalDelay
	for {
		state := getScreenState(screen)
		if state.matches(t, want) {
			break
		}
		remainingDelay -= loopDelay
		if remainingDelay <= 0 {
			t.Fatalf("Screen didn't match after %v:\n got:\n%v\nwant:\n%v", totalDelay, state, want)
		}
		time.Sleep(loopDelay)
	}
}

func TestTerm(t *testing.T) {
	const (
		w         = 80
		h         = 25
		blockSize = 10
	)
	reader, writer := io.Pipe()

	config := MenoConfig{
		Config: blocks.Config{
			Source: blocks.ConfigSource{
				Input: reader,
			},
			BlockSize:      blockSize,
			IndexNextBytes: 2,
		},
		LineSeperator: []byte("\n"),
	}

	screen := tcell.NewSimulationScreen("")
	meno, err := NewMeno(config, screen)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		meno.Run()
		wg.Done()
	}()

	// Write 2h numbered lines.
	for i := 0; i < h*2; i++ {
		var sb strings.Builder
		fmt.Fprintf(&sb, "%03d: ", i)
		// Make each line take up exactly blockSize so it will flush immediately
		// to the line wrapper.
		for sb.Len() < blockSize-1 {
			sb.WriteRune('a')
		}
		sb.WriteRune('\n')
		writer.Write([]byte(sb.String()))
	}

	firstPage := []lineMatch{
		{0, "000: .+"},
		{1, "001: .+"},
		{23, "023: .+"},
		{24, ":"},
	}
	oneLineDown := []lineMatch{
		{0, "001: .+"},
		{23, "024: .+"},
		{24, ":"},
	}
	secondPage := []lineMatch{
		{0, "024: .+"},
		{23, "047: .+"},
		{24, ":"},
	}
	lastPage := []lineMatch{
		{0, "024: .+"},
		{23, "047: .+"},
		{24, ":"},
	}

	assertScreen(t, screen, firstPage)

	// Down one line.
	screen.InjectKeyBytes([]byte("j"))
	assertScreen(t, screen, oneLineDown)

	// Up one line.
	screen.InjectKeyBytes([]byte("k"))
	assertScreen(t, screen, firstPage)

	// Can't go up above the first line.
	screen.InjectKeyBytes([]byte("kkkkkkkkk"))
	assertScreen(t, screen, firstPage)

	// Down one page.
	screen.InjectKeyBytes([]byte(" "))
	assertScreen(t, screen, secondPage)

	// Up one page.
	screen.InjectKeyBytes([]byte("b"))
	assertScreen(t, screen, firstPage)

	// Down one page.
	screen.InjectKeyBytes([]byte("f"))
	assertScreen(t, screen, secondPage)

	// Jump to top.
	screen.InjectKeyBytes([]byte("g"))
	assertScreen(t, screen, firstPage)

	// Page down.
	screen.InjectKey(tcell.KeyPgDn, ' ', tcell.ModNone)
	assertScreen(t, screen, secondPage)

	// Jump to bottom.
	screen.InjectKeyBytes([]byte("G"))
	assertScreen(t, screen, lastPage)

	screen.InjectKeyBytes([]byte("q"))

	wg.Wait()
}
