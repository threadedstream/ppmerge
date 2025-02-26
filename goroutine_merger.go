package ppmerge

import (
	"bytes"
	"compress/gzip"
	"io"

	"github.com/threadedstream/ppmerge/profile"
	"google.golang.org/protobuf/proto"
)

type GoroutineProfileMerger struct {
	mergedProfile *MergedGoroutineProfile
	stringTable   map[string]uint64
}

func NewGoroutineProfileMerger() *GoroutineProfileMerger {
	return &GoroutineProfileMerger{
		mergedProfile: MergedGoroutineProfileFromVTPool(),
		stringTable: map[string]uint64{
			"": 0,
		},
	}
}

func (gpm *GoroutineProfileMerger) WriteCompressed(w io.Writer) error {
	// Write writes the profile as a gzip-compressed marshaled protobuf.
	zw := gzip.NewWriter(w)
	defer zw.Close()
	serialized, err := gpm.mergedProfile.MarshalVT()
	if err != nil {
		return err
	}

	_, err = zw.Write(serialized)
	return err
}

func (gpm *GoroutineProfileMerger) Merge(gps ...*profile.GoroutineProfile) *MergedGoroutineProfile {
	gpm.mergedProfile.Totals = make([]uint64, 0, len(gps))
	gpm.mergedProfile.NumStacktraces = make([]uint64, 0, len(gps))

	gpm.merge(gps...)

	gpm.finalizeStringTable()
	return gpm.mergedProfile
}

func (gpm *GoroutineProfileMerger) merge(gps ...*profile.GoroutineProfile) {
	var resultStacktraces []*profile.Stacktrace
	for _, gp := range gps {
		gpm.mergedProfile.Totals = append(gpm.mergedProfile.Totals, gp.Total)
		stacktraces := gp.GetStacktraces()

		gpm.mergedProfile.NumStacktraces = append(gpm.mergedProfile.NumStacktraces, uint64(len(stacktraces)))

		for _, st := range stacktraces {
			resultStacktrace := new(profile.Stacktrace)
			resultStacktrace.Total = st.Total

			resultStacktrace.PC = make([]uint64, len(st.PC))
			copy(resultStacktrace.PC, st.PC)

			frames := st.GetFrames()
			if frames == nil {
				resultStacktraces = append(resultStacktraces, resultStacktrace)
				continue
			}
			resultStacktrace.Frames = make([]*profile.Frame, 0, len(frames))
			for _, f := range frames {
				resultStacktrace.Frames = append(resultStacktrace.Frames, gpm.remapFrame(f, gp.StringTable))
			}
			resultStacktraces = append(resultStacktraces, resultStacktrace)
		}
	}

	gpm.mergedProfile.Stacktraces = resultStacktraces
}

func (gpm *GoroutineProfileMerger) remapFrame(frame *profile.Frame, gpStringTable []string) *profile.Frame {
	return &profile.Frame{
		Address:      frame.Address,
		FunctionName: gpm.putString(gpStringTable[frame.FunctionName]),
		Offset:       frame.Offset,
		Filename:     gpm.putString(gpStringTable[frame.Filename]),
		Line:         frame.Line,
	}
}

func (gpm *GoroutineProfileMerger) finalizeStringTable() {
	gpm.mergedProfile.StringTable = make([]string, len(gpm.stringTable))
	for k, v := range gpm.stringTable {
		gpm.mergedProfile.StringTable[v] = k
	}
}

func (gpm *GoroutineProfileMerger) putString(val string) uint64 {
	if id, ok := gpm.stringTable[val]; ok {
		return id
	}
	id := uint64(len(gpm.stringTable))
	gpm.stringTable[val] = id
	return id
}

type GoroutineProfileUnPacker struct {
	mergedProfile *MergedGoroutineProfile
	stringTable   map[string]uint64
}

func NewGoroutineProfileUnPacker(mergedProfile *MergedGoroutineProfile) *GoroutineProfileUnPacker {
	return &GoroutineProfileUnPacker{
		mergedProfile: mergedProfile,
		stringTable: map[string]uint64{
			"": 0,
		},
	}
}

func (gpu *GoroutineProfileUnPacker) UnpackRaw(compressedRawProfile []byte, idx uint64) (*profile.GoroutineProfile, error) {
	bb := bytes.NewBuffer(compressedRawProfile)

	gzReader, err := gzip.NewReader(bb)
	if err != nil {
		return nil, err
	}

	rawProfile, err := io.ReadAll(gzReader)
	if err != nil {
		return nil, err
	}

	if gpu.mergedProfile == nil {
		gpu.mergedProfile = MergedGoroutineProfileFromVTPool()
	}

	if err = proto.Unmarshal(rawProfile, gpu.mergedProfile); err != nil {
		return nil, err
	}

	return gpu.Unpack(idx)
}

func (gpu *GoroutineProfileUnPacker) Unpack(idx uint64) (*profile.GoroutineProfile, error) {
	if idx >= uint64(len(gpu.mergedProfile.NumStacktraces)) {
		return nil, indexOutOfRangeErr
	}
	gp := profile.GoroutineProfileFromVTPool()

	gp.Total = gpu.mergedProfile.Totals[idx]

	numStacktraces := gpu.mergedProfile.NumStacktraces[idx]

	var offset uint64
	for i := uint64(0); i < idx; i++ {
		offset += gpu.mergedProfile.NumStacktraces[i]
	}

	limit := offset + numStacktraces

	gp.Stacktraces = make([]*profile.Stacktrace, 0, numStacktraces)
	for offset < limit {
		gp.Stacktraces = append(gp.Stacktraces, gpu.remapStacktrace(gpu.mergedProfile.Stacktraces[offset]))
		offset++
	}

	gpu.finalizeStringTable(gp)

	return gp, nil
}

func (gpu *GoroutineProfileUnPacker) remapStacktrace(st *profile.Stacktrace) *profile.Stacktrace {
	resultStacktrace := new(profile.Stacktrace)
	resultStacktrace.Total = st.Total

	resultStacktrace.PC = make([]uint64, len(st.PC))
	copy(resultStacktrace.PC, st.PC)

	frames := st.GetFrames()
	if frames == nil {
		return resultStacktrace
	}

	resultStacktrace.Frames = make([]*profile.Frame, 0, len(st.Frames))
	for _, f := range frames {
		resultStacktrace.Frames = append(resultStacktrace.Frames, gpu.remapFrame(f))
	}

	return resultStacktrace
}

func (gpu *GoroutineProfileUnPacker) remapFrame(frame *profile.Frame) *profile.Frame {
	return &profile.Frame{
		Address:      frame.Address,
		FunctionName: gpu.putString(gpu.mergedProfile.StringTable[frame.FunctionName]),
		Offset:       frame.Offset,
		Filename:     gpu.putString(gpu.mergedProfile.StringTable[frame.Filename]),
		Line:         frame.Line,
	}
}

func (gpu *GoroutineProfileUnPacker) finalizeStringTable(gp *profile.GoroutineProfile) {
	gp.StringTable = make([]string, len(gpu.stringTable))
	for k, v := range gpu.stringTable {
		gp.StringTable[v] = k
	}
}

func (gpu *GoroutineProfileUnPacker) putString(val string) uint64 {
	if id, ok := gpu.stringTable[val]; ok {
		return id
	}
	id := uint64(len(gpu.stringTable))
	gpu.stringTable[val] = id
	return id
}
