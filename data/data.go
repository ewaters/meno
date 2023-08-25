package data

import (
	"io"
	"log"
	"strings"

	"github.com/ewaters/meno/trigram"
)

// For testing.
var (
	overrideBufSize = 0
	enableLogger    = false
)

type IndexedLines struct {
	vl       []visibleLine
	index    *trigram.Index
	maxQuery int
}

// NewIndexedLines takes a max query value which indicates how long of a query
// should we optimize the index for. Queries that are longer than this value
// will not be able to benefit from the index and will need to brute force
// search.
func NewIndexedLines(maxQuery int) *IndexedLines {
	return &IndexedLines{
		index:    trigram.NewIndex(),
		maxQuery: maxQuery,
	}
}

// AddLine will add and index the passed line. You must call FinishAddLine() once
// you're done adding lines.
func (il *IndexedLines) AddLine(vl visibleLine) {
	il.vl = append(il.vl, vl)

	i := len(il.vl)
	il.index.AddWithID(vl.line, uint64(i))
}

// FinishAddLine will complete any remaining processing.
func (il *IndexedLines) FinishAddLine() {
}

func (il *IndexedLines) Size() int {
	return len(il.vl)
}

func (il *IndexedLines) Line(idx int) visibleLine {
	return il.vl[idx]
}

func (il *IndexedLines) Clear(idx int) {
	// We don't clear the index since this is only done during a rebuild.
	il.vl[idx] = visibleLine{}
}

type IndexedData struct {
	lines *IndexedLines
	width int
}

func NewIndexedData(inFile io.Reader, width int, maxQuery int) *IndexedData {
	id := &IndexedData{
		lines: NewIndexedLines(maxQuery),
		width: width,
	}
	defer id.lines.FinishAddLine()

	readC := make(chan string)
	resultC := make(chan visibleLine)
	go generateVisibleLines(width, readC, resultC)

	doneC := make(chan bool)
	go func() {
		for result := range resultC {
			id.lines.AddLine(result)
		}
		doneC <- true
	}()

	// Read 100 visible lines worth of bytes at a time.
	bufSize := width * 100
	if overrideBufSize > 0 {
		bufSize = overrideBufSize
	}
	buf := make([]byte, bufSize)

	for {
		n, err := inFile.Read(buf)
		if n > 0 {
			readC <- string(buf[:n])
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			log.Fatal(err)
		}
		if n < bufSize {
			break
		}
	}
	close(readC)
	<-doneC

	return id
}

func generateVisibleLines(width int, inC chan string, outC chan visibleLine) {
	leftOver := ""
	endsWithNewline := false
	if enableLogger {
		log.Printf("Starting range over inC")
	}
	for part := range inC {
		lines := strings.Split(leftOver+part, "\n")
		if enableLogger {
			log.Printf("Read %q, have lines %q", part, lines)
		}
		leftOver = ""
		endsWithNewline = part[len(part)-1] == '\n'
		if endsWithNewline {
			// The last element in the lines list is an empty string; let's
			// pop it.
			lines = lines[:len(lines)-1]
		}

		for i := 0; i < len(lines); i++ {
			line := lines[i]
			lastLine := i == len(lines)-1
			for len(line) > width {
				part := line[:width]
				line = line[width:]
				outC <- visibleLine{
					line:       part,
					hasNewline: false,
				}
			}
			if !lastLine || endsWithNewline {
				outC <- visibleLine{
					line:       line,
					hasNewline: true,
				}
			} else {
				leftOver = line
			}
		}
	}
	if len(leftOver) > 0 {
		outC <- visibleLine{
			line:       leftOver,
			hasNewline: endsWithNewline,
		}
	}
	close(outC)
}

type visibleLine struct {
	line       string
	hasNewline bool
}

func (id *IndexedData) LineMatches(i int, query string) bool {
	if i < 0 || i > id.lines.Size()-1 {
		log.Fatalf("LineMatches(%d, %q) called with out-of-bounds index", i, query)
	}
	vline := id.lines.Line(i)

	// If the line contains the query, great!
	if strings.Contains(vline.line, query) {
		return true
	}

	// Otherwise, concatenate a suffix to the string to see if the query
	// *starts* on the lineNumber i but isn't *entirely* on that line.

	suffix := ""
	{
		var sb strings.Builder
		j := i + 1
		for sb.Len() < len(query) {
			if j > id.lines.Size()-1 {
				break
			}
			vl := id.lines.Line(j)
			sb.WriteString(vl.line)
			if vl.hasNewline {
				sb.WriteRune('\n')
			}
			j++
		}
		suffix = sb.String()
		//p.logf("doSearch fetched %d suffix lines", j-i)
	}

	// However, if this suffix entirely has the query, then we return
	// false since line 'i' doesn't contain it.
	if strings.Contains(suffix, query) {
		return false
	}

	var final strings.Builder
	final.WriteString(vline.line)
	if vline.hasNewline {
		final.WriteRune('\n')
	}

	if enableLogger {
		log.Printf("LineMatches(%d, %q) against %q + %q", i, query, final.String(), suffix)
	}
	final.WriteString(suffix)

	return strings.Contains(final.String(), query)
}

func (id *IndexedData) VisibleLines() int {
	return id.lines.Size()
}

func (id *IndexedData) Resize(width int) {
	if width == id.width {
		return
	}
	id.width = width

	newLines := NewIndexedLines(id.lines.maxQuery)
	defer newLines.FinishAddLine()

	readC := make(chan string)
	resultC := make(chan visibleLine)
	go generateVisibleLines(width, readC, resultC)

	doneC := make(chan bool)
	go func() {
		for result := range resultC {
			newLines.AddLine(result)
		}
		doneC <- true
	}()

	for i := 0; i < id.lines.Size(); i++ {
		vl := id.lines.Line(i)
		if vl.hasNewline {
			readC <- vl.line + "\n"
		} else {
			readC <- vl.line
		}
		// Try to save some memory.
		id.lines.Clear(i)
	}
	close(readC)
	<-doneC

	id.lines = newLines
}
