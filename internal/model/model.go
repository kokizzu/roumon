package model

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"strconv"
	"strings"
)

// Goroutine info from pprof API. See: https://github.com/DataDog/go-profiler-notes/blob/main/goroutine.md
// Status Details:
// See: https://github.com/golang/go/blob/go1.15.6/src/runtime/runtime2.go#L14-L105
// and https://github.com/golang/go/blob/go1.15.6/src/runtime/runtime2.go#L996-L1024
type Goroutine struct {
	ID             int64
	Status         string // TODO: move known states to array and use slice for unknown
	WaitSinceMin   int64
	StackTrace     []StackFrame
	CratedBy       *StackFrame // Only one frame long. Nill if not set
	LockedToThread bool
}

// StackContains returns true if string is included on one of the elements of the stack slice
func StackContains(sf []StackFrame, subString string) bool {
	for _, s := range sf {
		if strings.Contains(strings.ToLower(s.String()), subString) {
			return true
		}
	}
	return false
}

// StackFrame contains the info for one stack frame
// See: https://dev.to/mcaci/reading-stack-traces-in-go-3ah5
type StackFrame struct {
	FuncName string
	File     string
	Line     int32
	Position *int // Relative stack position. Not mandatory
}

func (s StackFrame) String() string {
	return fmt.Sprintf("%s\n   file://%s#%d +0x%x", s.FuncName, s.File, s.Line, s.Position)
}

// For example /usr/local/go/src/net/http/server.go:2969 +0x970
func parseStackPos(scanner *bufio.Scanner) (fileName string, line int32, pos *int, err error) {
	if !scanner.Scan() {
		err = fmt.Errorf("Unexpected end of file")
		return
	}
	text := strings.TrimSpace(scanner.Text())

	if len(text) == 0 {
		err = fmt.Errorf("Unexpected empty line")
		return
	}

	fileLineSep := strings.LastIndex(text, ":")

	fileName = text[:fileLineSep]

	linePosSep := strings.LastIndex(text, " ")
	var lineStr string
	if fileLineSep+1 >= linePosSep {
		// Cannot parse stack pos for text. Keep default of nill
		lineStr = text[fileLineSep+1:]
	} else {
		posInt64, errParse := strconv.ParseInt(text[linePosSep+4:], 16, 64)
		if errParse != nil {
			err = fmt.Errorf("Could parse stack pos %s to line int. Error: %s", text, errParse.Error())
			return
		}
		posInt := int(posInt64)
		pos = &posInt
		lineStr = text[fileLineSep+1 : linePosSep]
	}

	lineInt, errParse := strconv.ParseInt(lineStr, 10, 32)
	if errParse != nil {
		err = fmt.Errorf("Could parse line %s to line int. Err: %s", text, errParse.Error())
		return
	}
	line = int32(lineInt)

	return
}

// parseHeader of stack trace. See: https://golang.org/src/runtime/traceback.go?s=30186:30213#L869
func parseHeader(header string) (routine Goroutine, err error) {
	routineHeader := strings.Split(header, " ")
	if len(routineHeader) < 3 {
		err = fmt.Errorf("Expected header with len >= 3, but got: %s", routineHeader)
		return
	}
	if routineHeader[0] != "goroutine" {
		err = fmt.Errorf("Expected goroutine header, but got: %s", routineHeader)
		return
	}
	id, parseErr := strconv.ParseInt(routineHeader[1], 10, 64)
	if parseErr != nil {
		err = fmt.Errorf("Could not parse ID. Err: %s", parseErr.Error())
		return
	}

	stateStartIdx := strings.Index(header, "[")
	// Remove []:
	fullState := header[stateStartIdx+1 : len(header)-2]
	stateParts := strings.Split(fullState, ",")
	var status string
	lockedToThread := false
	waitTimeMin := int64(0)
	if len(stateParts) == 1 {
		status = fullState
	} else {
		status = stateParts[0]
		for idx := 1; idx < len(stateParts); idx++ {
			part := strings.Trim(stateParts[idx], " ")
			if strings.HasSuffix(part, "minutes") {
				minUnitSep := strings.Index(part, " ")
				waitTimeMin, parseErr = strconv.ParseInt(part[:minUnitSep], 10, 64)
				if parseErr != nil {
					err = fmt.Errorf("Failed to parse minutes. Err: %s", parseErr.Error())
					return
				}
			} else if part == "locked to thread" {
				lockedToThread = true
			}
		}
	}

	routine = Goroutine{
		Status:         status,
		ID:             id,
		WaitSinceMin:   waitTimeMin,
		LockedToThread: lockedToThread,
	}
	return
}

// ParseStackFrame reads full file and return all goroutines as slice
func ParseStackFrame(reader io.Reader) (routines []Goroutine, err error) {
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Text()

		routine, err := parseHeader(line)
		if err != nil {
			log.Printf("Failed to parse routine header. Err: %s", err.Error())
			continue
		}

		routine.StackTrace = make([]StackFrame, 0)
		for scanner.Scan() {
			traceLine := scanner.Text()

			if len(traceLine) == 0 {
				break
			}

			if strings.HasPrefix(traceLine, "created by ") {
				file, line, pos, err := parseStackPos(scanner)
				if err != nil {
					log.Printf("Failed to parse created by stack. Err: %s", err.Error())
					continue
				}
				routine.CratedBy = &StackFrame{
					FuncName: traceLine[11:],
					File:     file,
					Line:     line,
					Position: pos,
				}
			} else {
				file, line, pos, err := parseStackPos(scanner)
				if err != nil {
					log.Printf("Failed to parse stack. Err: %s", err.Error())
					continue
				}
				frame := StackFrame{
					FuncName: traceLine,
					File:     file,
					Line:     line,
					Position: pos,
				}
				routine.StackTrace = append(routine.StackTrace, frame)
			}
		}
		routines = append(routines, routine)
	}

	err = scanner.Err()
	return
}
