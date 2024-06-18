package ppmerge

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

// Parse parses goroutine profiles in debug=1 format
func (gp *GoroutineProfile) Parse(rawProfile []byte) error {
	s := bufio.NewScanner(bytes.NewBuffer(rawProfile))
	for s.Scan() && isSpaceOrComment(s.Text()) {
	}
	if err := s.Err(); err != nil {
		return err
	}
	if err := gp.parseTotalCount(s); err != nil {
		return err
	}

	return gp.parseStackTraces(s)
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

func (gp *GoroutineProfile) parseStackTraces(s *bufio.Scanner) error {
	stringTable := map[string]uint64{}

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

	// #\t%#x\t%s+%#x\t%s:%d

	// parse functions
	for s.Scan() && !isSpace(s.Text()) {
		line = s.Text()
		if strings.HasPrefix(line, "# labels:") {
			continue
		}

	}

	return st, nil
}
