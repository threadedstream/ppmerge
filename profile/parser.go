package profile

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"github.com/pkg/errors"
)

var (
	countStartRE = regexp.MustCompile(`\A(\S+) profile: total (\d+)\z`)
	countRE      = regexp.MustCompile(`\A(\d+) @(( 0x[0-9a-f]+)+)\z`)
	frameInfoRe  = regexp.MustCompile(`\A#\t+(0x[0-9a-f]+)\t+(\S+)[+](0x[0-9a-f]+)\t+(\S+):(\d+)\z`)
)

var errUnrecognized = fmt.Errorf("unrecognized profile format")
var errMalformed = fmt.Errorf("malformed profile format")

func ParseProfileData(rawProfile []byte) (*Profile, error) {
	if len(rawProfile) >= 2 && rawProfile[0] == 0x1f && rawProfile[1] == 0x8b {
		gz, err := gzip.NewReader(bytes.NewBuffer(rawProfile))
		if err == nil {
			rawProfile, err = io.ReadAll(gz)
		}
		if err != nil {
			return nil, fmt.Errorf("decompressing profile: %v", err)
		}
		if err = gz.Close(); err != nil {
			return nil, fmt.Errorf("close gzip reader: %v", err)
		}
	}

	p := ProfileFromVTPool()
	if err := p.UnmarshalVT(rawProfile); err != nil {
		return nil, fmt.Errorf("unmarshalling profile: %v", err)
	}
	return p, nil
}

func ParseProfile(rd io.Reader) (*Profile, error) {
	b, err := io.ReadAll(rd)
	if err == nil {
		return ParseProfileData(b)
	}
	return nil, errors.Errorf("could not read profile: %v", err)
}

func (gp *GoroutineProfile) MarshalDebug() string {
	var sb strings.Builder

	gp.writeProlog(&sb)

	gp.writeStackTraces(&sb)

	return sb.String()
}

func (gp *GoroutineProfile) writeProlog(sb *strings.Builder) {
	fmt.Fprintf(sb, "goroutine profile: total %d\n", gp.GetTotal())
}

func (gp *GoroutineProfile) writeStackTraces(sb *strings.Builder) {
	for _, st := range gp.GetStacktraces() {
		gp.writeStackTrace(sb, st)
		sb.WriteRune('\n')
	}
}

func (gp *GoroutineProfile) writeStackTrace(sb *strings.Builder, st *Stacktrace) {
	pc := make([]string, 0, len(st.PC))
	for _, addr := range st.PC {
		pc = append(pc, fmt.Sprintf("%#x", addr))
	}
	fmt.Fprintf(sb, "%d @ %s\n", st.Total, strings.Join(pc, " "))
	for _, f := range st.GetFrames() {
		gp.writeFrame(sb, f)
	}
}

func (gp *GoroutineProfile) writeFrame(sb *strings.Builder, f *Frame) {
	if f.FunctionName == 0 {
		// empty function name
		fmt.Fprintf(sb, "#\t%#x\n", f.Address)
	} else {
		fmt.Fprintf(sb, "#\t%#x\t%s+%#x\t%s:%d\n", f.Address, gp.StringTable[f.FunctionName], f.Offset, gp.StringTable[f.Filename], f.Line)
	}
}

// Parse parses goroutine profiles in debug=1 format
func (gp *GoroutineProfile) Parse(rawProfile []byte) error {
	s := bufio.NewScanner(bytes.NewBuffer(rawProfile))
	for s.Scan() && isSpaceOrComment(s.Text()) {
	}
	if err := s.Err(); err != nil {
		return err
	}

	stringTable := map[string]uint64{
		"": 0,
	}

	if err := gp.parseTotalCount(s); err != nil {
		return err
	}

	if err := gp.parseStackTraces(s, stringTable); err != nil {
		return err
	}

	gp.finalizeStringTable(stringTable)

	return nil
}

func isSpaceOrComment(line string) bool {
	trimmed := strings.TrimSpace(line)
	return len(trimmed) == 0 || trimmed[0] == '#'
}

func isSpace(line string) bool {
	trimmed := strings.TrimSpace(line)
	return len(trimmed) == 0
}

func (gp *GoroutineProfile) parseTotalCount(s *bufio.Scanner) error {
	m := countStartRE.FindStringSubmatch(s.Text())
	if m == nil {
		return errUnrecognized
	}

	totalCount, err := strconv.ParseUint(m[2], 10, 64)
	if err != nil {
		return err
	}
	gp.Total = totalCount
	return nil
}

func (gp *GoroutineProfile) parseStackTraces(s *bufio.Scanner, stringTable map[string]uint64) error {
	for s.Scan() {
		line := s.Text()
		if isSpace(line) {
			continue
		}
		stt, err := parseStackTrace(line, s, stringTable)
		if err != nil {
			return err
		}
		gp.Stacktraces = append(gp.Stacktraces, stt)
	}

	return nil
}

func (gp *GoroutineProfile) finalizeStringTable(stringTable map[string]uint64) {
	gp.StringTable = make([]string, len(stringTable))
	for k, v := range stringTable {
		gp.StringTable[v] = k
	}
}

func parseStackTrace(line string, s *bufio.Scanner, stringTable map[string]uint64) (*Stacktrace, error) {
	m := countRE.FindStringSubmatch(line)
	if m == nil {
		return nil, errMalformed
	}
	n, err := strconv.ParseUint(m[1], 0, 64)
	if err != nil {
		return nil, errMalformed
	}
	fields := strings.Fields(m[2])
	st := &Stacktrace{}
	st.Total = n
	st.PC = make([]uint64, 0, len(fields))
	for _, pc := range fields {
		addr, err := strconv.ParseUint(pc, 0, 64)
		if err != nil {
			return nil, errMalformed
		}
		st.PC = append(st.PC, addr)
	}

	// parse functions
	for s.Scan() && !isSpace(s.Text()) {
		frame := &Frame{}
		line = s.Text()
		if strings.HasPrefix(line, "# labels:") {
			continue
		}
		m = frameInfoRe.FindStringSubmatch(line)
		if m == nil {
			return nil, errMalformed
		}

		// remove 0x in the beginning
		validHex := m[1][2:]
		locAddr, err := strconv.ParseUint(validHex, 16, 64)
		if err != nil {
			return nil, err
		}
		frame.Address = locAddr
		frame.FunctionName = putString(stringTable, m[2])

		validHex = m[3][2:]
		offset, err := strconv.ParseUint(validHex, 16, 64)
		if err != nil {
			return nil, err
		}
		frame.Offset = offset
		frame.Filename = putString(stringTable, m[4])
		lineNum, err := strconv.ParseUint(strings.Trim(m[5], "\""), 10, 64)
		if err != nil {
			return nil, err
		}
		frame.Line = lineNum
		st.Frames = append(st.Frames, frame)
	}

	if err = s.Err(); err != nil {
		return nil, err
	}

	return st, nil
}

func putString(stringTable map[string]uint64, val string) uint64 {
	if id, ok := stringTable[val]; ok {
		return id
	}
	id := uint64(len(stringTable))
	stringTable[val] = id
	return id
}
