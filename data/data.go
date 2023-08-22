package data

import (
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

	// Read 100 visible lines worth of bytes at a time.
	bufSize := width * 100
	if overrideBufSize > 0 {
		bufSize = overrideBufSize
	}
	buf := make([]byte, bufSize)

	leftOver := ""
	endsWithNewline := false
	for {
		n, err := inFile.Read(buf)
		if n > 0 {
			lines := strings.Split(leftOver+string(buf[:n]), "\n")
			if enableLogger {
				log.Printf("Read %q, have lines %q", string(buf[:n]), lines)
			}
			leftOver = ""
			endsWithNewline = buf[n-1] == '\n'
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
					id.lines = append(id.lines, visibleLine{
						line:       part,
						hasNewline: false,
					})
				}
				if !lastLine || endsWithNewline {
					id.lines = append(id.lines, visibleLine{
						line:       line,
						hasNewline: true,
					})
				} else {
					leftOver = line
				}
			}

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
	if len(leftOver) > 0 {
		id.lines = append(id.lines, visibleLine{
			line:       leftOver,
			hasNewline: endsWithNewline,
		})
	}

	return id
}

type visibleLine struct {
	line       string
	hasNewline bool
}

func splitLine(line string, width int) []visibleLine {
	var result []visibleLine
	for len(line) > width {
		part := line[:width]
		line = line[width:]
		result = append(result, visibleLine{
			line:       part,
			hasNewline: false,
		})
	}
	result = append(result, visibleLine{
		line:       line,
		hasNewline: true,
	})
	return result
}

func (id *IndexedData) VisibleLines() int {
	return len(id.lines)
}

func (id *IndexedData) Resize(width int) {
	if width == id.width {
		return
	}
	id.width = width
	newLines := make([]visibleLine, 0, len(id.lines))

	var sb strings.Builder
	for i := 0; i < len(id.lines); i++ {
		vl := id.lines[i]
		sb.WriteString(vl.line)
		if vl.hasNewline {
			newLines = append(newLines, splitLine(sb.String(), width)...)
			sb.Reset()
			// Try to save some memory
			id.lines[i] = visibleLine{}
		}
	}
	if sb.Len() > 0 {
		newLines = append(newLines, splitLine(sb.String(), width)...)
	}
	id.lines = newLines
}
