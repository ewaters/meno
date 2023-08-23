package data

import (
	"fmt"
	"io"
	"log"
	"strings"
)

type IndexedData struct {
	width int
	lines []visibleLine
}

// For testing.
var (
	overrideBufSize = 0
	enableLogger    = false
)

func NewIndexedData(inFile io.Reader, width int) *IndexedData {
	id := &IndexedData{
		width: width,
	}

	readC := make(chan string)
	resultC := make(chan visibleLine)
	go generateVisibleLines(width, readC, resultC)

	doneC := make(chan bool)
	go func() {
		for result := range resultC {
			id.lines = append(id.lines, result)
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
	if i < 0 || i > len(id.lines)-1 {
		log.Fatalf("LineMatches(%d, %q) called with out-of-bounds index", i, query)
	}
	vline := id.lines[i]

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
			if j > len(id.lines)-1 {
				break
			}
			vl := id.lines[j]
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

	final := fmt.Sprintf("%s%s", vline.line, suffix)
	return strings.Contains(final, query)
}

func (id *IndexedData) VisibleLines() int {
	return len(id.lines)
}

func (id *IndexedData) Resize(width int) {
	if width == id.width {
		return
	}
	id.width = width

	readC := make(chan string)
	resultC := make(chan visibleLine)
	go generateVisibleLines(width, readC, resultC)

	doneC := make(chan bool)
	newLines := make([]visibleLine, 0, len(id.lines))
	go func() {
		for result := range resultC {
			newLines = append(newLines, result)
		}
		doneC <- true
	}()

	for i := 0; i < len(id.lines); i++ {
		vl := id.lines[i]
		if vl.hasNewline {
			readC <- vl.line + "\n"
		} else {
			readC <- vl.line
		}
		// Try to save some memory.
		id.lines[i] = visibleLine{}
	}
	close(readC)
	<-doneC

	id.lines = newLines
}
