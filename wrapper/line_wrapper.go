package wrapper

import (
	"fmt"
	"log"
	"strings"

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
	loc        blocks.BlockIDOffsetRange
	hasNewline bool
}

func (vl visibleLine) String() string {
	return fmt.Sprintf("loc %v, has newline %v", vl.loc, vl.hasNewline)
}

func generateVisibleLines(width int, inC chan blocks.Block, outC chan visibleLine) {
	var leftOver string
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
		if leftOver != "" {
			start = leftOverStart
		}
		end := blocks.BlockIDOffset{
			BlockID: block.ID,
			Offset:  0,
		}

		part := string(block.Bytes)
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
				//part := line[:width]
				end.Offset += width
				line = line[width:]
				outC <- visibleLine{
					// line:       part,
					loc: blocks.BlockIDOffsetRange{
						Start: start,
						End:   end,
					},
					hasNewline: false,
				}
			}
			if !lastLine || endsWithNewline {
				outC <- visibleLine{
					//line:       line,
					loc: blocks.BlockIDOffsetRange{
						Start: start,
						End:   end,
					},
					hasNewline: true,
				}
			} else {
				leftOver = line
			}
		}
	}
	if len(leftOver) > 0 {
		outC <- visibleLine{
			//line:       leftOver,
			loc: blocks.BlockIDOffsetRange{
				//Start: start,
				//End:   end,
			},
			hasNewline: endsWithNewline,
		}
	}
	close(outC)
}
