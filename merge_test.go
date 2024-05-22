package ppmerge

import (
	"bytes"
	"io"
	"os"
	"testing"

	"github.com/google/pprof/profile"
	"github.com/stretchr/testify/require"
)

func TestHeapMerge(t *testing.T) {
	profiles := getProfiles(t, "hprof1", "hprof2", "hprof3", "hprof4")
	profileMerger := NewProfileMerger()

	// merge profiles
	mergedProfile := profileMerger.Merge(profiles...)
	require.NotNil(t, mergedProfile)

	type testCase struct {
		name                string
		recoveredProfileIdx uint64
		actualProfile       *profile.Profile
	}

	for _, tc := range []testCase{
		{
			name:                "parca_heap profile 1",
			recoveredProfileIdx: 0,
			actualProfile:       profiles[0],
		},
		{
			name:                "parca_heap profile 2",
			recoveredProfileIdx: 1,
			actualProfile:       profiles[1],
		},
		{
			name:                "parca_heap profile 3",
			recoveredProfileIdx: 2,
			actualProfile:       profiles[2],
		},
		{
			name:                "parca_heap profile 4",
			recoveredProfileIdx: 3,
			actualProfile:       profiles[3],
		},
	} {

		t.Run(tc.name, func(t *testing.T) {
			// try to unpack first one
			unpacker := NewProfileUnPacker(profileMerger.mergedProfile)
			recoveredOne, err := unpacker.Unpack(tc.recoveredProfileIdx)
			require.NoError(t, err)

			for i, sample := range tc.actualProfile.Sample {
				currentRecoveredSample := recoveredOne.Sample[i]
				require.Equal(t, currentRecoveredSample.Value, sample.Value)
				for locIdx, loc := range sample.Location {
					for lineIdx, line := range loc.Line {
						require.Equal(t, currentRecoveredSample.Location[locIdx].Line[lineIdx].Line, line.Line)
						require.Equal(t, currentRecoveredSample.Location[locIdx].Line[lineIdx].Function.StartLine, line.Function.StartLine)
						require.Equal(t, currentRecoveredSample.Location[locIdx].Line[lineIdx].Function.Name, line.Function.Name)
						require.Equal(t, currentRecoveredSample.Location[locIdx].Line[lineIdx].Function.SystemName, line.Function.SystemName)
						require.Equal(t, currentRecoveredSample.Location[locIdx].Line[lineIdx].Function.Filename, line.Function.Filename)
					}
					require.Equal(t, currentRecoveredSample.Location[locIdx].Address, loc.Address)
				}
			}

			require.Equal(t, recoveredOne.Period, tc.actualProfile.Period)

			for i, st := range tc.actualProfile.SampleType {
				require.Equal(t, recoveredOne.SampleType[i].Type, st.Type)
				require.Equal(t, recoveredOne.SampleType[i].Unit, st.Unit)
			}

			require.Equal(t, recoveredOne.DurationNanos, tc.actualProfile.DurationNanos)
			require.Equal(t, recoveredOne.TimeNanos, tc.actualProfile.TimeNanos)
		})
	}
}

func TestMergeWrite(t *testing.T) {
	profiles := getProfiles(t, "hprof1", "hprof2", "hprof3", "hprof4")

	profileMerger := NewProfileMerger()
	mergedProfile := profileMerger.Merge(profiles...)
	require.NotNil(t, mergedProfile)

	compressedBB := bytes.NewBuffer(nil)
	require.NoError(t, profileMerger.WriteCompressed(compressedBB))
	require.Greater(t, compressedBB.Len(), 0)

	uncompressedBB := bytes.NewBuffer(nil)
	require.NoError(t, profileMerger.WriteUncompressed(uncompressedBB))
	require.Greater(t, uncompressedBB.Len(), 0)

	require.Greater(t, uncompressedBB.Len(), compressedBB.Len())

	noCompactBB := bytes.NewBuffer(nil)
	for _, p := range profiles {
		require.NoError(t, p.Write(noCompactBB))
	}
	require.Less(t, compressedBB.Len(), noCompactBB.Len())

	// merge profiles with different sample types
	profiles = getProfiles(t, "parca_heap", "parca_cpu", "parca_goroutine")
	mergedProfile = profileMerger.Merge(profiles...)
	require.NotNil(t, mergedProfile)

	compressedBB = bytes.NewBuffer(nil)
	require.NoError(t, profileMerger.WriteCompressed(compressedBB))

	noCompactBB = bytes.NewBuffer(nil)
	for _, p := range profiles {
		require.NoError(t, p.Write(noCompactBB))
	}
	require.Less(t, compressedBB.Len(), noCompactBB.Len())
}

func TestMergeUnpack(t *testing.T) {
	t.Run("general merge unpack", func(t *testing.T) {
		profiles := getProfiles(t, "hprof1", "hprof2", "hprof3", "hprof4")

		profileMerger := NewProfileMerger()
		mergedProfile := profileMerger.Merge(profiles...)
		require.NotNil(t, mergedProfile)

		compressedBB := bytes.NewBuffer(nil)
		require.NoError(t, profileMerger.WriteCompressed(compressedBB))
		require.Greater(t, compressedBB.Len(), 0)

		unpacker := NewProfileUnPacker(nil)
		p, err := unpacker.UnpackRaw(compressedBB.Bytes(), 0)
		require.NoError(t, err)
		require.NotNil(t, p)
	})

	t.Run("merge unpack debug goroutine profiles", func(t *testing.T) {
		profiles := getDebugProfiles(t, "parca_goroutine_debug_1_1", "parca_goroutine_debug_1_2", "parca_goroutine_debug_1_2")

		profileMerger := NewByteProfileMerger()
		mergedProfile := profileMerger.Merge(profiles...)
		require.NotNil(t, mergedProfile)

		unpacker := NewByteProfileUnPacker(mergedProfile)
		p, err := unpacker.Unpack(0)
		require.NoError(t, err)
		require.NotNil(t, p)
		require.Equal(t, profiles[0], p)

		p, err = unpacker.Unpack(1)
		require.NoError(t, err)
		require.NotNil(t, p)
		require.Equal(t, profiles[1], p)

		p, err = unpacker.Unpack(2)
		require.NoError(t, err)
		require.NotNil(t, p)
		require.Equal(t, profiles[2], p)
	})

	t.Run("merge unpack raw debug goroutine profiles", func(t *testing.T) {
		profiles := getDebugProfiles(t, "parca_goroutine_debug_1_1", "parca_goroutine_debug_1_2", "parca_goroutine_debug_1_2")

		profileMerger := NewByteProfileMerger()
		mergedProfile := profileMerger.Merge(profiles...)
		require.NotNil(t, mergedProfile)

		bb := bytes.NewBuffer(nil)
		require.NoError(t, profileMerger.WriteCompressed(bb))

		unpacker := NewByteProfileUnPacker(mergedProfile)
		p, err := unpacker.UnpackRaw(bb.Bytes(), 0)
		require.NoError(t, err)
		require.NotNil(t, p)
		require.Equal(t, profiles[0], p)

		p, err = unpacker.UnpackRaw(bb.Bytes(), 1)
		require.NoError(t, err)
		require.NotNil(t, p)
		require.Equal(t, profiles[1], p)

		p, err = unpacker.UnpackRaw(bb.Bytes(), 2)
		require.NoError(t, err)
		require.NotNil(t, p)
		require.Equal(t, profiles[2], p)
	})
}

func BenchmarkProfileMerger(b *testing.B) {
	profiles := getProfiles(b, "hprof1", "hprof2", "hprof3", "hprof4")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		profileMerger := NewProfileMerger()
		profileMerger.Merge(profiles...)
	}
}

func BenchmarkProfileUnPacker(b *testing.B) {
	profiles := getProfiles(b, "hprof1", "hprof2", "hprof3", "hprof4")

	profileMerger := NewProfileMerger()
	mergedProfile := profileMerger.Merge(profiles...)

	unpacker := NewProfileUnPacker(mergedProfile)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := unpacker.Unpack(uint64(i % 4))
		require.NoError(b, err)
	}
}

func getProfiles(t require.TestingT, paths ...string) []*profile.Profile {
	dir := "./testdata/"
	var profiles []*profile.Profile
	for _, profileName := range paths {
		file, err := os.OpenFile(dir+profileName, os.O_RDONLY, 0666)
		require.NoError(t, err)
		prof, err := profile.Parse(file)
		require.NoError(t, err)
		profiles = append(profiles, prof)
	}

	return profiles
}

func getDebugProfiles(t require.TestingT, paths ...string) [][]byte {
	dir := "./testdata/"
	profiles := make([][]byte, len(paths))
	for i, profileName := range paths {
		file, err := os.OpenFile(dir+profileName, os.O_RDONLY, 0666)
		require.NoError(t, err)
		p, err := io.ReadAll(file)
		require.NoError(t, err)
		profiles[i] = p
	}

	return profiles
}
