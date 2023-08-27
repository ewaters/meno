package wrapper

import (
	"bytes"
	"fmt"
	"log"

	"github.com/ewaters/meno/blocks"
)

var (
	enableLogger = true
)

type lineWrapper struct {
	width int
}

func newLineWrapper(width int) *lineWrapper {
	return &lineWrapper{
		width: width,
	}
}

type visibleLine struct {
	loc             blocks.BlockIDOffsetRange
	endsWithLineSep bool
}

func (vl visibleLine) String() string {
	return fmt.Sprintf("loc %v, ends with line sep %v", vl.loc, vl.endsWithLineSep)
}

func generateVisibleLines(lineSep []byte, width int, inC chan blocks.Block, outC chan visibleLine) {
	var leftOver []byte
	var leftOverStart blocks.BlockIDOffset

	endsWithNewline := false

	if enableLogger {
		log.Printf("Starting range over inC")
	}
	for block := range inC {
		start := blocks.BlockIDOffset{
			BlockID: block.ID,
			Offset:  0,
		}
		if len(leftOver) > 0 {
			start = leftOverStart
		}
		end := blocks.BlockIDOffset{
			BlockID: block.ID,
			Offset:  0 - len(leftOver),
		}
		if enableLogger {
			log.Printf("reset start: %v, end: %v", start, end)
		}

		combined := append(leftOver, block.Bytes...)
		lines := bytes.Split(combined, lineSep)
		if enableLogger {
			var linesStr []string
			for _, line := range lines {
				linesStr = append(linesStr, string(line))
			}
			log.Printf("Block [%d] %q, have lines %q", block.ID, string(block.Bytes), linesStr)
		}
		leftOver = nil
		endsWithNewline = bytes.HasSuffix(combined, lineSep)
		if endsWithNewline {
			// The last element in the lines list is an empty string; let's
			// pop it.
			lines = lines[:len(lines)-1]
		}

		for i := 0; i < len(lines); i++ {
			line := lines[i]
			lastLine := i == len(lines)-1
			for len(line) > width {
				//part := line[:width]
				end.Offset += width - 1
				vl := visibleLine{
					// line:       part,
					loc: blocks.BlockIDOffsetRange{
						Start: start,
						End:   end,
					},
					endsWithLineSep: false,
				}
				if enableLogger {
					log.Printf("line: %q, sending vl %v (wrapped)", string(line[:width]), vl)
				}
				outC <- vl
				line = line[width:]
				end.Offset++
				start = end
				if enableLogger {
					log.Printf("start: %v, end: %v", start, end)
				}
			}
			if !lastLine || endsWithNewline {
				end.Offset += len(line) - 1 + len(lineSep)
				vl := visibleLine{
					loc: blocks.BlockIDOffsetRange{
						Start: start,
						End:   end,
					},
					endsWithLineSep: true,
				}
				if enableLogger {
					log.Printf("line: %q, sending vl %v", string(line), vl)
				}
				outC <- vl
				end.Offset++
				start = end
				if enableLogger {
					log.Printf("start: %v, end: %v", start, end)
				}
			} else {
				leftOver = line
				leftOverStart = start
			}
		}
	}
	if len(leftOver) > 0 {
		end := leftOverStart
		end.Offset += len(leftOver) - 1
		vl := visibleLine{
			//line:       leftOver,
			loc: blocks.BlockIDOffsetRange{
				Start: leftOverStart,
				End:   end,
			},
			endsWithLineSep: endsWithNewline,
		}
		if enableLogger {
			log.Printf("leftover line: %q, sending vl %v", string(leftOver), vl)
		}
		outC <- vl
	}
	close(outC)
}
