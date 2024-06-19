package ppmerge

import (
	"bytes"
	"compress/gzip"
	"io"

	"google.golang.org/protobuf/proto"
)

type ByteProfileMerger struct {
	mergedProfile *MergedByteProfile
}

func NewByteProfileMerger() *ByteProfileMerger {
	return &ByteProfileMerger{
		mergedProfile: new(MergedByteProfile),
	}
}

func (bm *ByteProfileMerger) Merge(profiles ...[]byte) *MergedByteProfile {
	bm.mergedProfile.Profiles = make([][]byte, len(profiles))
	for i, p := range profiles {
		bm.mergedProfile.Profiles[i] = p
	}

	return bm.mergedProfile
}

func (bm *ByteProfileMerger) WriteCompressed(w io.Writer) error {
	// Write writes the profile as a gzip-compressed marshaled protobuf.
	zw := gzip.NewWriter(w)
	defer zw.Close()
	serialized, err := proto.Marshal(bm.mergedProfile)
	if err != nil {
		return err
	}

	_, err = zw.Write(serialized)
	return err
}

func (bm *ByteProfileMerger) WriteUncompressed(w io.Writer) error {
	serialized, err := bm.mergedProfile.MarshalVT()
	if err != nil {
		return err
	}
	_, err = w.Write(serialized)
	return err
}

// ByteProfileUnPacker is the unpacker for MergedByteProfile
type ByteProfileUnPacker struct {
	mergedProfile *MergedByteProfile
}

// NewByteProfileUnPacker returns new ByteProfileUnPacker instance
func NewByteProfileUnPacker(mergedProfile *MergedByteProfile) *ByteProfileUnPacker {
	return &ByteProfileUnPacker{
		mergedProfile: mergedProfile,
	}
}

func (pu *ByteProfileUnPacker) UnpackRaw(compressedRawProfile []byte, idx uint64) ([]byte, error) {
	bb := bytes.NewBuffer(compressedRawProfile)

	gzReader, err := gzip.NewReader(bb)
	if err != nil {
		return nil, err
	}

	rawProfile, err := io.ReadAll(gzReader)
	if err != nil {
		return nil, err
	}

	if pu.mergedProfile == nil {
		pu.mergedProfile = new(MergedByteProfile)
	}

	if err = pu.mergedProfile.UnmarshalVT(rawProfile); err != nil {
		return nil, err
	}

	return pu.Unpack(idx)
}

func (pu *ByteProfileUnPacker) Unpack(idx uint64) ([]byte, error) {
	return pu.mergedProfile.Profiles[idx], nil
}
