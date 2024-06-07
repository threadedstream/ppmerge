package ppmerge

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"

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
		if err := gz.Close(); err != nil {
			return nil, fmt.Errorf("close gzip reader: %v", err)
		}
	}

	profile := ProfileFromVTPool()
	if err := profile.UnmarshalVT(rawProfile); err != nil {
		return nil, fmt.Errorf("unmarshalling profile: %v", err)
	}
	return profile, nil
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
