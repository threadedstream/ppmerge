package ppmerge

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/google/pprof/profile"
	"github.com/pkg/errors"
)

func ParseProfileData(rawProfile []byte, debugGoroutine bool) (*Profile, error) {
	if debugGoroutine {
		return parseGoroutineDebugProfile(rawProfile)
	}

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

func ParseProfile(rd io.Reader, debugGoroutine bool) (*Profile, error) {
	b, err := io.ReadAll(rd)
	if err == nil {
		return ParseProfileData(b, debugGoroutine)
	}
	return nil, errors.Errorf("could not read profile: %v", err)
}

func parseGoroutineDebugProfile(rawProfile []byte) (*Profile, error) {
	p, err := profile.ParseData(rawProfile)
	if err != nil {
		return nil, err
	}

	vtProfile := ProfileFromVTPool()
	vtProfile.From(p)

	return vtProfile, nil
}

func (p *Profile) WriteDebug() (string, error) {
	if len(p.SampleType) == 0 {
		return "", errors.New("sample type is empty")
	}

	sampleType := p.SampleType[0].Type
	switch t := p.StringTable[sampleType]; t {
	default:
		return "", errors.Errorf("unsupported sample type: %s", t)
	case "goroutine":
	}

	bb := new(strings.Builder)

	sort.Slice(p.Sample, func(i, j int) bool {
		return p.Sample[i].Value[0] > p.Sample[j].Value[0]
	})

	var total int64
	for _, s := range p.Sample {
		total += s.Value[0]
	}

	fmt.Fprintf(bb, "goroutine profile: total %d\n", total)

	for _, s := range p.Sample {
		fmt.Fprintf(bb, "%d\n", s.Value[0])
		for i := len(s.LocationId) - 1; i > 0; i-- {
			l := p.Location[s.LocationId[i]-1]
			if len(l.Line) == 0 {
				continue
			}
			// TODO(threadedstream): output goroutines in debug format
			line := l.Line[0]
			fn := p.Function[line.FunctionId-1]
			fmt.Fprintf(bb, "#\t%#x\t%s\t%s:%d\n", l.Address, p.StringTable[fn.Name], p.StringTable[fn.Filename], line.Line)
		}
		bb.WriteRune('\n')
	}

	return bb.String(), nil
}
