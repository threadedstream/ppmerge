package ppmerge

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"

	"github.com/pkg/errors"
)

func ParseProfileData(rawProfile []byte) (*Profile, error) {
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

func ParseProfile(rd io.Reader) (*Profile, error) {
	b, err := io.ReadAll(rd)
	if err == nil {
		return ParseProfileData(b)
	}
	return nil, errors.Errorf("could not read profile: %v", err)
}
