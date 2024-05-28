package ppmerge

import (
	"bytes"
	"compress/gzip"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/google/pprof/profile"
	"github.com/pkg/errors"
	"google.golang.org/protobuf/proto"
)

type functionKey struct {
	name, systemName, filename, startLine int64
}

type mappingKey struct {
	start, limit, offset uint64
	buildIDOrFile        int64
}

type locationKey struct {
	mappingID, address uint64
	lines              string
	isFolded           bool
}

type locationMap struct {
	dense  []*MergeLocation
	sparse map[uint64]*MergeLocation
}

func makeLocationMap(n int) locationMap {
	return locationMap{
		dense:  make([]*MergeLocation, n),
		sparse: map[uint64]*MergeLocation{},
	}
}

func (lm locationMap) get(id uint64) *MergeLocation {
	if id < uint64(len(lm.dense)) {
		return lm.dense[int(id)]
	}
	return lm.sparse[id]
}

func (lm locationMap) put(id uint64, location *MergeLocation) {
	if id < uint64(len(lm.dense)) {
		lm.dense[int(id)] = location
		return
	}
	lm.sparse[id] = location
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

// ProfileUnPacker recovers any of the profiles stored inside mergedProfile
// ProfileUnPacker recovers any of the profiles stored inside mergedProfile
type ProfileUnPacker struct {
	mergedProfile *MergedProfile

	functionByID map[uint64]*profile.Function
	mappingByID  map[uint64]*profile.Mapping
	locationByID map[uint64]*profile.Location
}

// NewProfileUnPacker returns ProfileUnPacker instance
func NewProfileUnPacker(mergedProfile *MergedProfile) *ProfileUnPacker {
	return &ProfileUnPacker{
		mergedProfile: mergedProfile,
		functionByID:  make(map[uint64]*profile.Function),
		mappingByID:   make(map[uint64]*profile.Mapping),
		locationByID:  make(map[uint64]*profile.Location),
	}
}

func (pu *ProfileUnPacker) UnpackRaw(compressedRawProfile []byte, idx uint64) (*profile.Profile, error) {
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
		pu.mergedProfile = new(MergedProfile)
	}

	if err = proto.Unmarshal(rawProfile, pu.mergedProfile); err != nil {
		return nil, err
	}

	return pu.Unpack(idx)
}

func (pu *ProfileUnPacker) Unpack(idx uint64) (*profile.Profile, error) {
	var p profile.Profile
	if err := pu.unpackSampleTypes(&p, idx); err != nil {
		return nil, errors.Wrap(err, "unpack sample types")
	}
	if err := pu.unpackSamples(&p, idx); err != nil {
		return nil, errors.Wrap(err, "unpack samples")
	}
	if err := pu.unpackPeriodType(&p, idx); err != nil {
		return nil, errors.Wrap(err, "unpack period type")
	}
	if err := pu.unpackPeriod(&p, idx); err != nil {
		return nil, errors.Wrap(err, "unpack period")
	}
	if err := pu.unpackDurationNanos(&p, idx); err != nil {
		return nil, errors.Wrap(err, "unpack duration")
	}
	if err := pu.unpackTimeNanos(&p, idx); err != nil {
		return nil, errors.Wrap(err, "unpack time")
	}
	return &p, nil
}

func (pu *ProfileUnPacker) unpackSamples(p *profile.Profile, idx uint64) error {
	if idx > uint64(len(pu.mergedProfile.NumSamples)) {
		return errors.New("index out of range")
	}

	numSamples := pu.mergedProfile.NumSamples[idx]

	var offset uint64
	for i := uint64(0); i < idx; i++ {
		offset += pu.mergedProfile.NumSamples[i]
	}

	limit := offset + numSamples

	p.Sample = make([]*profile.Sample, 0, numSamples)
	for offset < limit {
		p.Sample = append(p.Sample, pu.unpackSample(p, offset))
		offset++
	}

	return nil
}

func (pu *ProfileUnPacker) unpackSampleTypes(p *profile.Profile, idx uint64) error {
	if idx > uint64(len(pu.mergedProfile.NumSampleTypes)) {
		return errors.New("index out of range")
	}

	numSampleTypes := pu.mergedProfile.NumSampleTypes[idx]

	var offset uint64
	for i := uint64(0); i < idx; i++ {
		offset += pu.mergedProfile.NumSampleTypes[i] * 2
	}

	limit := offset + (numSampleTypes * 2)

	p.SampleType = make([]*profile.ValueType, 0, numSampleTypes)
	for offset < limit {
		p.SampleType = append(p.SampleType, pu.unpackSampleType(offset))
		offset += 2
	}

	return nil
}

func (pu *ProfileUnPacker) unpackSampleType(offset uint64) *profile.ValueType {
	var vt profile.ValueType
	vt.Type = pu.getString(int(pu.mergedProfile.SampleType[offset]))
	offset++
	vt.Unit = pu.getString(int(pu.mergedProfile.SampleType[offset]))
	offset++
	return &vt
}

func (pu *ProfileUnPacker) unpackSample(p *profile.Profile, offset uint64) *profile.Sample {
	var s profile.Sample
	sample := pu.mergedProfile.Samples[offset]
	s.Location = make([]*profile.Location, 0, len(sample.LocationId))
	for _, loc := range sample.LocationId {
		s.Location = append(s.Location, pu.unpackLocation(p, uint64(loc)))
	}
	s.Value = sample.Value
	return &s
}

func (pu *ProfileUnPacker) unpackPeriodType(p *profile.Profile, idx uint64) error {
	if idx*2 >= uint64(len(pu.mergedProfile.PeriodTypes)) || (idx*2)+1 > uint64(len(pu.mergedProfile.PeriodTypes))-1 {
		return errors.New("index out of range")
	}

	p.PeriodType = new(profile.ValueType)
	p.PeriodType.Type = pu.getString(int(pu.mergedProfile.PeriodTypes[idx*2]))
	p.PeriodType.Unit = pu.getString(int(pu.mergedProfile.PeriodTypes[idx*2+1]))

	return nil
}

func (pu *ProfileUnPacker) unpackPeriod(p *profile.Profile, idx uint64) error {
	if idx >= uint64(len(pu.mergedProfile.Periods)) {
		return errors.New("index out of range")
	}

	p.Period = pu.mergedProfile.Periods[idx]
	return nil
}

func (pu *ProfileUnPacker) unpackDurationNanos(p *profile.Profile, idx uint64) error {
	if idx >= uint64(len(pu.mergedProfile.DurationsNanos)) {
		return errors.New("index out of range")
	}

	p.DurationNanos = pu.mergedProfile.DurationsNanos[idx]
	return nil
}

func (pu *ProfileUnPacker) unpackTimeNanos(p *profile.Profile, idx uint64) error {
	if idx >= uint64(len(pu.mergedProfile.TimesNanos)) {
		return errors.New("index out of range")
	}

	p.TimeNanos = pu.mergedProfile.TimesNanos[idx]
	return nil
}

func (pu *ProfileUnPacker) unpackLocation(p *profile.Profile, id uint64) *profile.Location {
	if loc, ok := pu.locationByID[id]; ok {
		return loc
	}

	if id < 1 || id > uint64(len(pu.mergedProfile.Locations)) {
		return nil
	}

	mergedLocation := pu.mergedProfile.Locations[id-1]
	loc := &profile.Location{
		ID:      uint64(len(p.Location) + 1),
		Mapping: pu.unpackMapping(p, mergedLocation.MappingId),
		Address: mergedLocation.Address,
		Line:    make([]profile.Line, len(mergedLocation.Line), len(mergedLocation.Line)),
	}

	for i, line := range mergedLocation.Line {
		loc.Line[i] = pu.unpackLine(p, line)
	}

	p.Location = append(p.Location, loc)
	pu.locationByID[id] = loc

	return loc
}

func (pu *ProfileUnPacker) unpackLine(p *profile.Profile, line *MergeLine) profile.Line {
	return profile.Line{
		Line:     line.Line,
		Function: pu.unpackFunction(p, line.FunctionId),
	}
}

func (pu *ProfileUnPacker) getString(id int) string {
	if id < 0 || id > len(pu.mergedProfile.StringTable) {
		return ""
	}
	return pu.mergedProfile.StringTable[id]
}

func (pu *ProfileUnPacker) unpackFunction(p *profile.Profile, id uint64) *profile.Function {
	if fn, ok := pu.functionByID[id]; ok {
		return fn
	}

	if id < 1 || id > uint64(len(pu.mergedProfile.Functions)) {
		return nil
	}

	mergedFunction := pu.mergedProfile.Functions[id-1]

	fn := &profile.Function{
		ID:         uint64(len(p.Function) + 1),
		Name:       pu.getString(int(mergedFunction.Name)),
		SystemName: pu.getString(int(mergedFunction.SystemName)),
		Filename:   pu.getString(int(mergedFunction.Filename)),
		StartLine:  mergedFunction.StartLine,
	}
	p.Function = append(p.Function, fn)
	pu.functionByID[id] = fn
	return fn
}

func (pu *ProfileUnPacker) unpackMapping(p *profile.Profile, id uint64) *profile.Mapping {
	if m, ok := pu.mappingByID[id]; ok {
		return m
	}

	if id < 1 || id > uint64(len(pu.mergedProfile.Mappings)) {
		return nil
	}

	mergedMapping := pu.mergedProfile.Mappings[id-1]
	profileMapping := &profile.Mapping{
		ID:      uint64(len(p.Mapping) + 1),
		Start:   mergedMapping.MemoryStart,
		Limit:   mergedMapping.MemoryLimit,
		Offset:  mergedMapping.FileOffset,
		File:    pu.getString(int(mergedMapping.Filename)),
		BuildID: pu.getString(int(mergedMapping.BuildId)),
	}
	p.Mapping = append(p.Mapping, profileMapping)
	pu.mappingByID[id] = profileMapping

	return profileMapping
}

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

// ProfileMerger merges several profiles into a single one
type ProfileMerger struct {
	mergedProfile *MergedProfile
	stringTable   map[string]int

	functionTable map[functionKey]uint64
	mappingTable  map[mappingKey]uint64
	locationTable map[locationKey]uint64
}

func NewProfileMerger() *ProfileMerger {
	return &ProfileMerger{
		mergedProfile: &MergedProfile{},
		stringTable:   make(map[string]int),
		functionTable: make(map[functionKey]uint64),
		mappingTable:  make(map[mappingKey]uint64),
		locationTable: make(map[locationKey]uint64),
	}
}

func (pw *ProfileMerger) WriteCompressed(w io.Writer) error {
	// Write writes the profile as a gzip-compressed marshaled protobuf.
	zw := gzip.NewWriter(w)
	defer zw.Close()
	serialized, err := pw.mergedProfile.MarshalVT()
	if err != nil {
		return err
	}

	_, err = zw.Write(serialized)
	return err
}

func (pw *ProfileMerger) WriteUncompressed(w io.Writer) error {
	serialized, err := pw.mergedProfile.MarshalVT()
	if err != nil {
		return err
	}
	_, err = w.Write(serialized)
	return err
}

func (pw *ProfileMerger) Merge(ps ...*Profile) *MergedProfile {
	pw.mergedProfile.NumFunctions = make([]uint64, 0, len(ps))
	pw.mergedProfile.NumLocations = make([]uint64, 0, len(ps))
	pw.mergedProfile.NumSampleTypes = make([]uint64, 0, len(ps))
	pw.mergedProfile.NumMappings = make([]uint64, 0, len(ps))
	pw.mergedProfile.NumSamples = make([]uint64, 0, len(ps))

	for _, p := range ps {
		pw.mergedProfile.NumFunctions = append(pw.mergedProfile.NumFunctions, uint64(len(p.Function)))
		pw.mergedProfile.NumLocations = append(pw.mergedProfile.NumLocations, uint64(len(p.Location)))
		pw.mergedProfile.NumSampleTypes = append(pw.mergedProfile.NumSampleTypes, uint64(len(p.SampleType)))
		pw.mergedProfile.NumMappings = append(pw.mergedProfile.NumMappings, uint64(len(p.Mapping)))
		pw.mergedProfile.NumSamples = append(pw.mergedProfile.NumSamples, uint64(len(p.Sample)))
	}

	pw.mergeSamples(ps...)
	pw.mergeSampleTypes(ps...)
	pw.mergeTimeNanos(ps...)
	pw.mergeDurationNanos(ps...)
	pw.mergePeriods(ps...)
	pw.mergePeriodTypes(ps...)

	pw.mergedProfile.StringTable = make([]string, len(pw.stringTable)+1)
	pw.mergedProfile.StringTable[0] = ""
	for st, id := range pw.stringTable {
		pw.mergedProfile.StringTable[id] = st
	}

	return pw.mergedProfile
}

func (pw *ProfileMerger) mergeSamples(ps ...*Profile) {
	// allocate samples slice beforehand
	size := 0
	for _, p := range ps {
		size += len(p.Sample)
	}
	pw.mergedProfile.Samples = make([]*MergeSample, 0, size)

	for _, p := range ps {
		for _, s := range p.Sample {
			pw.mergedProfile.Samples = append(pw.mergedProfile.Samples, pw.asMergedSample(s, p))
		}
	}
}

func (pw *ProfileMerger) mergePeriodTypes(ps ...*Profile) {
	pw.mergedProfile.PeriodTypes = make([]int64, 0, len(ps)*2)

	for _, p := range ps {
		pw.mergedProfile.PeriodTypes = append(pw.mergedProfile.PeriodTypes,
			p.PeriodType.Type,
			p.PeriodType.Unit,
		)
	}
}

func (pw *ProfileMerger) mergeTimeNanos(ps ...*Profile) {
	pw.mergedProfile.TimesNanos = make([]int64, 0, len(ps))

	for _, p := range ps {
		pw.mergedProfile.TimesNanos = append(pw.mergedProfile.TimesNanos, p.TimeNanos)
	}
}

func (pw *ProfileMerger) mergeDurationNanos(ps ...*Profile) {
	pw.mergedProfile.DurationsNanos = make([]int64, 0, len(ps))

	for _, p := range ps {
		pw.mergedProfile.DurationsNanos = append(pw.mergedProfile.DurationsNanos, p.DurationNanos)
	}
}

func (pw *ProfileMerger) mergePeriods(ps ...*Profile) {
	pw.mergedProfile.Periods = make([]int64, 0, len(ps))

	for _, p := range ps {
		pw.mergedProfile.Periods = append(pw.mergedProfile.Periods, p.Period)
	}
}

func (pw *ProfileMerger) mergeSampleTypes(ps ...*Profile) {
	size := 0
	for _, p := range ps {
		size += len(p.SampleType)
	}

	pw.mergedProfile.SampleType = make([]int64, 0, size*2)

	for _, p := range ps {
		for _, vt := range p.SampleType {
			pw.mergedProfile.SampleType = append(pw.mergedProfile.SampleType,
				int64(pw.getStringRef(uint64(vt.Type), p)),
				int64(pw.getStringRef(uint64(vt.Unit), p)),
			)
		}
	}
}

func (pw *ProfileMerger) putMapping(src *Mapping, p *Profile) uint64 {
	if src == nil {
		return math.MaxUint64
	}

	mapping := &MergeMapping{
		MemoryStart:     src.MemoryStart,
		MemoryLimit:     src.MemoryLimit,
		FileOffset:      src.FileOffset,
		Filename:        int64(pw.getStringRef(uint64(src.Filename), p)),
		BuildId:         int64(pw.getStringRef(uint64(src.BuildId), p)),
		HasFilenames:    src.HasFilenames,
		HasFunctions:    src.HasFunctions,
		HasInlineFrames: src.HasInlineFrames,
		HasLineNumbers:  src.HasInlineFrames,
	}

	key := pw.getMappingKey(mapping)
	if mappingID, ok := pw.mappingTable[key]; ok {
		return mappingID
	}

	mapping.Id = uint64(len(pw.mergedProfile.Mappings) + 1)

	pw.mappingTable[key] = mapping.Id
	pw.mergedProfile.Mappings = append(pw.mergedProfile.Mappings, mapping)
	return mapping.Id
}

func (pw *ProfileMerger) asMergedSample(s *Sample, p *Profile) *MergeSample {
	mergedProfileSample := &MergeSample{
		LocationId: make([]int64, 0, len(s.LocationId)),
		Value:      s.Value,
	}

	for _, locId := range s.LocationId {
		mergedProfileSample.LocationId = append(mergedProfileSample.LocationId, int64(pw.putLocation(p.Location[locId-1], p)))
	}

	return mergedProfileSample
}

func (pw *ProfileMerger) asMergedValueType(vt *ValueType) *MergeValueType {
	return &MergeValueType{
		Type: vt.Type,
		Unit: vt.Unit,
	}
}

func (pw *ProfileMerger) asMergedProfileLines(lines []*Line, p *Profile) []*MergeLine {
	mergedProfileLines := make([]*MergeLine, 0, len(lines))
	for _, ln := range lines {
		mergedProfileLines = append(mergedProfileLines, pw.asMergedProfileLine(ln, p))
	}
	return mergedProfileLines
}

func (pw *ProfileMerger) asMergedProfileLine(line *Line, p *Profile) *MergeLine {
	return &MergeLine{
		FunctionId: pw.putFunction(p.Function[line.FunctionId-1], p),
		Line:       line.Line,
	}
}

func (pw *ProfileMerger) getStringRef(id uint64, p *Profile) int {
	strVal := p.StringTable[id]
	if localId, ok := pw.stringTable[strVal]; ok {
		return localId
	}
	newId := len(pw.stringTable) + 1
	pw.stringTable[strVal] = newId
	return newId
}

func (pw *ProfileMerger) getFunctionKey(fn *MergeFunction) functionKey {
	return functionKey{
		name:       fn.Name,
		systemName: fn.SystemName,
		filename:   fn.Filename,
		startLine:  fn.StartLine,
	}
}

func (pw *ProfileMerger) getMappingKey(m *MergeMapping) mappingKey {
	key := mappingKey{
		start:  m.MemoryStart,
		limit:  m.MemoryLimit,
		offset: m.FileOffset,
	}
	switch {
	case m.Filename > 0:
		key.buildIDOrFile = m.Filename
	case m.BuildId > 0:
		key.buildIDOrFile = m.BuildId
	default:
	}

	return key
}

func (pw *ProfileMerger) getLocationKey(loc *MergeLocation) locationKey {
	key := locationKey{
		mappingID: loc.MappingId,
		address:   loc.Address,
		isFolded:  loc.IsFolded,
	}

	lines := make([]string, len(loc.Line)*2)
	for i, line := range loc.Line {
		if line.FunctionId > 0 {
			lines[i*2] = strconv.FormatUint(line.FunctionId, 16)
		}
		lines[i*2+1] = strconv.FormatInt(line.Line, 16)
	}
	key.lines = strings.Join(lines, "|")

	return key
}

func (pw *ProfileMerger) putLine(src *Line, p *Profile) *MergeLine {
	return &MergeLine{
		FunctionId: pw.putFunction(p.Function[src.FunctionId-1], p),
		Line:       src.Line,
	}
}

func (pw *ProfileMerger) putLocation(src *Location, p *Profile) uint64 {
	if src == nil {
		return math.MaxUint64
	}

	loc := &MergeLocation{
		MappingId: pw.putMapping(p.Mapping[src.MappingId-1], p),
		Address:   src.Address,
		Line:      make([]*MergeLine, len(src.Line), len(src.Line)),
		IsFolded:  src.IsFolded,
	}

	for i, line := range src.Line {
		loc.Line[i] = pw.putLine(line, p)
	}

	key := pw.getLocationKey(loc)
	if locID, ok := pw.locationTable[key]; ok {
		return locID
	}

	loc.Id = uint64(len(pw.mergedProfile.Locations) + 1)
	pw.locationTable[key] = loc.Id
	pw.mergedProfile.Locations = append(pw.mergedProfile.Locations, loc)
	return loc.Id
}

func (pw *ProfileMerger) putFunction(src *Function, p *Profile) uint64 {
	if src == nil {
		return math.MaxUint64
	}

	f := &MergeFunction{
		Name:       int64(pw.getStringRef(uint64(src.Name), p)),
		SystemName: int64(pw.getStringRef(uint64(src.SystemName), p)),
		Filename:   int64(pw.getStringRef(uint64(src.Filename), p)),
		StartLine:  src.StartLine,
	}

	key := pw.getFunctionKey(f)
	if functionID, ok := pw.functionTable[key]; ok {
		return functionID
	}

	f.Id = uint64(len(pw.mergedProfile.Functions) + 1)
	pw.functionTable[key] = f.Id
	pw.mergedProfile.Functions = append(pw.mergedProfile.Functions, f)
	return f.Id
}
