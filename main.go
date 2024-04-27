package main

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"strings"

	"github.com/google/pprof/profile"
	"google.golang.org/protobuf/proto"
)

const dir = "/Users/gildarov/toys/ppmerge/"

func main() {
	files := make([]*os.File, 0, 4)
	profiles := make([]*profile.Profile, 0, 4)

	for _, filename := range []string{"cpuprof1", "cpuprof2", "cpuprof3", "cpuprof4"} {
		file, err := os.OpenFile(dir+filename, os.O_RDONLY, os.ModePerm)
		if err != nil {
			log.Fatal(err)
		}
		files = append(files, file)
	}

	for _, f := range files {
		p, err := profile.Parse(f)
		if err != nil {
			log.Fatal(err)
		}
		//p = p.Compact()
		profiles = append(profiles, p)
	}

	buffer := bytes.NewBuffer(nil)

	for _, p := range profiles {
		if err := p.Write(buffer); err != nil {
			log.Fatal(err)
		}
	}

	merger := newProfileMerger()
	merger.merge(profiles...)

	bufferCompact := bytes.NewBuffer(nil)
	if err := merger.writeCompressed(bufferCompact); err != nil {
		log.Fatal("failed to write compacted profile ", err)
	}

	println(buffer.Len())
	println(bufferCompact.Len())

	unpacker := newProfileUnPacker(merger.mergedProfile)
	p, _ := unpacker.unpack(1)
	println(p)
}

func getLinesKey(lines []*Line) string {
	var result []string
	for _, l := range lines {
		result = append(result, fmt.Sprintf("%d%d", l.FunctionId, l.Line))
	}
	return strings.Join(result, "|")
}

type functionKey struct {
	name, systemName, filename, startLine int64
}

type mappingKey struct {
	start, limit, offset uint64
	buildIDOrFile        int64
}

// profileUnPacker
type profileUnPacker struct {
	mergedProfile *MergedProfile

	functionByID map[uint64]*profile.Function
	mappingByID  map[uint64]*profile.Mapping
	locationByID map[uint64]*profile.Location
}

func newProfileUnPacker(mergedProfile *MergedProfile) *profileUnPacker {
	return &profileUnPacker{
		mergedProfile: mergedProfile,
		functionByID:  make(map[uint64]*profile.Function),
		mappingByID:   make(map[uint64]*profile.Mapping),
		locationByID:  make(map[uint64]*profile.Location),
	}
}

func (pu *profileUnPacker) unpack(idx uint64) (*profile.Profile, error) {
	var p profile.Profile
	_ = pu.unpackSampleTypes(&p, idx)
	_ = pu.unpackSamples(&p, idx)
	_ = pu.unpackPeriodType(&p, idx)
	_ = pu.unpackPeriod(&p, idx)
	_ = pu.unpackDurationNanos(&p, idx)
	_ = pu.unpackTimeNanos(&p, idx)
	return &p, nil
}

func (pu *profileUnPacker) unpackSamples(p *profile.Profile, idx uint64) error {
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

func (pu *profileUnPacker) unpackSampleTypes(p *profile.Profile, idx uint64) error {
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

func (pu *profileUnPacker) unpackSampleType(offset uint64) *profile.ValueType {
	var vt profile.ValueType
	vt.Type = pu.getString(int(pu.mergedProfile.SampleType[offset]))
	offset++
	vt.Unit = pu.getString(int(pu.mergedProfile.SampleType[offset]))
	offset++
	return &vt
}

func (pu *profileUnPacker) unpackSample(p *profile.Profile, offset uint64) *profile.Sample {
	var s profile.Sample
	sample := pu.mergedProfile.Samples[offset]
	s.Location = make([]*profile.Location, 0, len(sample.LocationId))
	for _, loc := range sample.LocationId {
		s.Location = append(s.Location, pu.unpackLocation(p, uint64(loc)))
	}
	s.Value = sample.Value
	return &s
}

func (pu *profileUnPacker) unpackPeriodType(p *profile.Profile, idx uint64) error {
	if idx*2 >= uint64(len(pu.mergedProfile.PeriodTypes)) || (idx*2)+2 >= uint64(len(pu.mergedProfile.PeriodTypes)) {
		return errors.New("index out of range")
	}

	p.PeriodType = new(profile.ValueType)
	p.PeriodType.Type = pu.getString(int(pu.mergedProfile.PeriodTypes[idx*2]))
	idx++
	p.PeriodType.Unit = pu.getString(int(pu.mergedProfile.PeriodTypes[idx*2+1]))

	return nil
}

func (pu *profileUnPacker) unpackPeriod(p *profile.Profile, idx uint64) error {
	if idx >= uint64(len(pu.mergedProfile.Periods)) {
		return errors.New("index out of range")
	}

	p.Period = pu.mergedProfile.Periods[idx]
	return nil
}

func (pu *profileUnPacker) unpackDurationNanos(p *profile.Profile, idx uint64) error {
	if idx >= uint64(len(pu.mergedProfile.DurationsNanos)) {
		return errors.New("index out of range")
	}

	p.DurationNanos = pu.mergedProfile.DurationsNanos[idx]
	return nil
}

func (pu *profileUnPacker) unpackTimeNanos(p *profile.Profile, idx uint64) error {
	if idx >= uint64(len(pu.mergedProfile.TimesNanos)) {
		return errors.New("index out of range")
	}

	p.TimeNanos = pu.mergedProfile.TimesNanos[idx]
	return nil
}

func (pu *profileUnPacker) unpackLocation(p *profile.Profile, id uint64) *profile.Location {
	if loc, ok := pu.locationByID[id]; ok {
		return loc
	}

	if id < 1 || id > uint64(len(pu.mergedProfile.Locations)) {
		return nil
	}

	mergedLocation := pu.mergedProfile.Locations[id]
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

func (pu *profileUnPacker) unpackLine(p *profile.Profile, line *Line) profile.Line {
	return profile.Line{
		Line:     line.Line,
		Function: pu.unpackFunction(p, line.FunctionId),
	}
}

func (pu *profileUnPacker) getString(id int) string {
	if id < 0 || id > len(pu.mergedProfile.StringTable) {
		return ""
	}
	return pu.mergedProfile.StringTable[id]
}

func (pu *profileUnPacker) unpackFunction(p *profile.Profile, id uint64) *profile.Function {
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

func (pu *profileUnPacker) unpackMapping(p *profile.Profile, id uint64) *profile.Mapping {
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

// / profileMerger
type profileMerger struct {
	mergedProfile *MergedProfile
	stringTable   map[string]int

	functionTable map[functionKey]uint64
	mappingTable  map[mappingKey]uint64
}

func newProfileMerger() *profileMerger {
	return &profileMerger{
		mergedProfile: &MergedProfile{},
		stringTable:   make(map[string]int),
		functionTable: make(map[functionKey]uint64),
		mappingTable:  make(map[mappingKey]uint64),
	}
}

func (pw *profileMerger) writeCompressed(w io.Writer) error {
	// Write writes the profile as a gzip-compressed marshaled protobuf.
	zw := gzip.NewWriter(w)
	defer zw.Close()
	serialized, err := proto.Marshal(pw.mergedProfile)
	if err != nil {
		return err
	}

	_, err = zw.Write(serialized)
	return err
}

func (pw *profileMerger) merge(ps ...*profile.Profile) {
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

	pw.mergeLocations(ps...)
	pw.mergeSamples(ps...)
	pw.mergeSampleTypes(ps...)
	pw.mergeTimeNanos(ps...)
	pw.mergeDurationNanos(ps...)
	pw.mergePeriods(ps...)
	pw.mergePeriodTypes(ps...)

	pw.mergedProfile.StringTable = make([]string, len(pw.stringTable), len(pw.stringTable))
	for st, id := range pw.stringTable {
		pw.mergedProfile.StringTable[id] = st
	}
}

func (pw *profileMerger) mergeSamples(ps ...*profile.Profile) {
	// allocate samples slice beforehand
	size := 0
	for _, p := range ps {
		size += len(p.Sample)
	}
	pw.mergedProfile.Samples = make([]*Sample, 0, size)

	for _, p := range ps {
		for _, s := range p.Sample {
			pw.mergedProfile.Samples = append(pw.mergedProfile.Samples, pw.asMergedSample(s))
		}
	}
}

func (pw *profileMerger) mergePeriodTypes(ps ...*profile.Profile) {
	pw.mergedProfile.PeriodTypes = make([]int64, 0, len(ps)*2)

	for _, p := range ps {
		pw.mergedProfile.PeriodTypes = append(pw.mergedProfile.PeriodTypes,
			int64(pw.putString(p.PeriodType.Type)),
			int64(pw.putString(p.PeriodType.Unit)),
		)
	}
}

func (pw *profileMerger) mergeTimeNanos(ps ...*profile.Profile) {
	pw.mergedProfile.TimesNanos = make([]int64, 0, len(ps))

	for _, p := range ps {
		pw.mergedProfile.TimesNanos = append(pw.mergedProfile.TimesNanos, p.TimeNanos)
	}
}

func (pw *profileMerger) mergeDurationNanos(ps ...*profile.Profile) {
	pw.mergedProfile.DurationsNanos = make([]int64, 0, len(ps))

	for _, p := range ps {
		pw.mergedProfile.DurationsNanos = append(pw.mergedProfile.DurationsNanos, p.DurationNanos)
	}
}

func (pw *profileMerger) mergePeriods(ps ...*profile.Profile) {
	pw.mergedProfile.Periods = make([]int64, 0, len(ps))

	for _, p := range ps {
		pw.mergedProfile.Periods = append(pw.mergedProfile.Periods, p.Period)
	}
}

func (pw *profileMerger) mergeSampleTypes(ps ...*profile.Profile) {
	size := 0
	for _, p := range ps {
		size += len(p.SampleType)
	}

	pw.mergedProfile.SampleType = make([]int64, 0, size*2)

	for _, p := range ps {
		for _, vt := range p.SampleType {
			pw.mergedProfile.SampleType = append(pw.mergedProfile.SampleType,
				int64(pw.putString(vt.Type)),
				int64(pw.putString(vt.Unit)),
			)
		}
	}
}

func (pw *profileMerger) putMapping(src *profile.Mapping) uint64 {
	if src == nil {
		return math.MaxUint64
	}

	key := mappingKey{
		start:  src.Start,
		limit:  src.Limit,
		offset: src.Offset,
	}
	switch {
	case src.File != "":
		key.buildIDOrFile = int64(pw.putString(src.File))
	case src.BuildID != "":
		key.buildIDOrFile = int64(pw.putString(src.BuildID))
	default:
	}

	if mappingID, ok := pw.mappingTable[key]; ok {
		return mappingID
	}

	mapping := &Mapping{
		Id:          uint64(len(pw.mergedProfile.Mappings) + 1),
		MemoryStart: src.Start,
		MemoryLimit: src.Limit,
		FileOffset:  src.Offset,
		Filename:    int64(pw.putString(src.File)),
		BuildId:     int64(pw.putString(src.BuildID)),
	}
	pw.mappingTable[key] = mapping.Id
	pw.mergedProfile.Mappings = append(pw.mergedProfile.Mappings, mapping)
	return mapping.Id
}

func (pw *profileMerger) mergeLocations(ps ...*profile.Profile) {
	size := 0
	for _, p := range ps {
		size += len(p.Location)
	}

	pw.mergedProfile.Locations = make([]*Location, 0, size)

	for _, p := range ps {
		for _, loc := range p.Location {
			pw.mergedProfile.Locations = append(pw.mergedProfile.Locations, pw.asMergedProfileLocation(loc))
		}
	}
}

func (pw *profileMerger) asMergedProfileLocation(loc *profile.Location) *Location {
	return &Location{
		Id:        loc.ID,
		MappingId: pw.putMapping(loc.Mapping),
		Address:   loc.Address,
		Line:      pw.asMergedProfileLines(loc.Line),
	}
}

func (pw *profileMerger) asMergedSample(s *profile.Sample) *Sample {
	mergedProfileSample := &Sample{
		LocationId: make([]int64, 0, len(s.Location)),
		Value:      s.Value,
	}

	for _, loc := range s.Location {
		mergedProfileSample.LocationId = append(mergedProfileSample.LocationId, int64(loc.ID))
	}

	return mergedProfileSample
}

func (pw *profileMerger) asMergedValueType(vt *profile.ValueType) *ValueType {
	return &ValueType{
		Type: int64(pw.putString(vt.Type)),
		Unit: int64(pw.putString(vt.Unit)),
	}
}

func (pw *profileMerger) asMergedProfileLines(lines []profile.Line) []*Line {
	mergedProfileLines := make([]*Line, 0, len(lines))
	for _, ln := range lines {
		mergedProfileLines = append(mergedProfileLines, pw.asMergedProfileLine(ln))
	}
	return mergedProfileLines
}

func (pw *profileMerger) asMergedProfileLine(line profile.Line) *Line {
	return &Line{
		FunctionId: pw.putFunction(line.Function),
		Line:       line.Line,
	}
}

func (pw *profileMerger) putString(val string) int {
	id, ok := pw.stringTable[val]
	if !ok {
		id = len(pw.stringTable)
		pw.stringTable[val] = id
	}
	return id
}

func (pw *profileMerger) getFunctionKey(fn *profile.Function) functionKey {
	return functionKey{
		name:       int64(pw.putString(fn.Name)),
		systemName: int64(pw.putString(fn.SystemName)),
		filename:   int64(pw.putString(fn.Filename)),
		startLine:  fn.StartLine,
	}
}

func (pw *profileMerger) putLine(src profile.Line) *Line {
	return &Line{
		FunctionId: pw.putFunction(src.Function),
		Line:       src.Line,
	}
}

func (pw *profileMerger) putLocation(src *profile.Location) uint64 {
	if src == nil {
		return math.MaxUint64
	}

	loc := &Location{
		Id:        uint64(len(pw.mergedProfile.Locations) + 1),
		MappingId: pw.putMapping(src.Mapping),
		Address:   src.Address,
		Line:      make([]*Line, len(src.Line), len(src.Line)),
	}

	for i, line := range src.Line {
		loc.Line[i] = pw.putLine(line)
	}

	pw.mergedProfile.Locations = append(pw.mergedProfile.Locations, loc)
	return loc.Id
}

func (pw *profileMerger) putFunction(src *profile.Function) uint64 {
	if src == nil {
		return math.MaxUint64
	}

	key := pw.getFunctionKey(src)
	if functionID, ok := pw.functionTable[key]; ok {
		return functionID
	}
	f := &Function{
		Id:         uint64(len(pw.mergedProfile.Functions) + 1),
		Name:       int64(pw.putString(src.Name)),
		SystemName: int64(pw.putString(src.SystemName)),
		Filename:   int64(pw.putString(src.Filename)),
		StartLine:  src.StartLine,
	}
	pw.functionTable[key] = f.Id
	pw.mergedProfile.Functions = append(pw.mergedProfile.Functions, f)
	return f.Id
}
