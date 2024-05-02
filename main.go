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
	"strconv"
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
}

func getLinesKey(lines []*Line) string {
	var result []string
	for _, l := range lines {
		result = append(result, fmt.Sprintf("%d%d", l.FunctionId, l.Line))
	}
	return strings.Join(result, "|")
}

type lineKey struct {
	functionID, line int64
}
type functionKey struct {
	name, systemName, filename, startLine int64
}

type mappingKey struct {
	size, offset  uint64
	buildIDOrFile int64
}

type locationKey struct {
	addr, mappingID uint64
	lines           string
}

type mapInfo struct {
	m      *Mapping
	offset int64
}

// / profileMerger
type profileMerger struct {
	mergedProfile *MergedProfile
	stringTable   map[string]int
	locationsByID map[uint64]*Location
	functionsByID map[uint64]*Function
	mappingsByID  map[uint64]mapInfo

	functionTable map[functionKey]*Function
	mappingTable  map[mappingKey]*Mapping
	locationTable map[locationKey]*Location
}

func newProfileMerger() *profileMerger {
	return &profileMerger{
		mergedProfile: &MergedProfile{},
		stringTable:   make(map[string]int),
		functionTable: make(map[functionKey]*Function),
		mappingTable:  make(map[mappingKey]*Mapping),
		locationTable: make(map[locationKey]*Location),
		locationsByID: make(map[uint64]*Location),
		functionsByID: make(map[uint64]*Function),
		mappingsByID:  make(map[uint64]mapInfo),
	}
}

func (pw *profileMerger) unpack(idx uint64) (*profile.Profile, error) {
	var p profile.Profile
	p.Mapping, _ = pw.unpackMappings(idx)
	//p.Function, _ = pw.unpackFunctions(idx)
	p.Location, _ = pw.unpackLocations(idx, p.Function, p.Mapping)
	p.SampleType, _ = pw.unpackSampleTypes(idx)
	p.Sample, _ = pw.unpackSamples(idx, p.Location)
	return &p, nil
}

func (pw *profileMerger) unpackMappings(idx uint64) ([]*profile.Mapping, error) {
	if idx > uint64(len(pw.mergedProfile.NumMappings)) {
		return nil, errors.New("index out of range")
	}
	mappingsLen := pw.mergedProfile.NumMappings[idx]
	mappings := make([]*profile.Mapping, 0, mappingsLen)

	var offset uint64
	for i := uint64(0); i < idx; i++ {
		offset += pw.mergedProfile.NumMappings[i] * 6
	}

	limit := offset + (mappingsLen * 6)

	for offset < limit {
		mappings = append(mappings, pw.unpackMapping(offset))
		offset += 6
	}

	return mappings, nil
}

func (pw *profileMerger) unpackSamples(idx uint64, locations []*profile.Location) ([]*profile.Sample, error) {
	if idx > uint64(len(pw.mergedProfile.NumSamples)) {
		return nil, errors.New("index out of range")
	}

	numSamples := pw.mergedProfile.NumSamples[idx]

	var offset uint64
	for i := uint64(0); i < idx; i++ {
		offset += pw.mergedProfile.NumSamples[i]
	}

	limit := offset + numSamples

	samples := make([]*profile.Sample, 0, numSamples)
	for offset < limit {
		samples = append(samples, pw.unpackSample(offset, locations))
		offset++
	}

	return samples, nil
}

func (pw *profileMerger) unpackSampleTypes(idx uint64) ([]*profile.ValueType, error) {
	if idx > uint64(len(pw.mergedProfile.NumSampleTypes)) {
		return nil, errors.New("index out of range")
	}

	numSampleTypes := pw.mergedProfile.NumSampleTypes[idx]

	var offset uint64
	for i := uint64(0); i < idx; i++ {
		offset += pw.mergedProfile.NumSampleTypes[i] * 2
	}

	limit := offset + (numSampleTypes * 2)

	sampleTypes := make([]*profile.ValueType, 0, numSampleTypes)
	for offset < limit {
		sampleTypes = append(sampleTypes, pw.unpackSampleType(offset))
		offset += 2
	}

	return sampleTypes, nil
}

func (pw *profileMerger) unpackSampleType(offset uint64) *profile.ValueType {
	var vt profile.ValueType
	vt.Type = pw.getString(int(pw.mergedProfile.SampleType[offset]))
	offset++
	vt.Unit = pw.getString(int(pw.mergedProfile.SampleType[offset]))
	offset++
	return &vt
}

func (pw *profileMerger) unpackSample(offset uint64, locations []*profile.Location) *profile.Sample {
	var s profile.Sample
	sample := pw.mergedProfile.Samples[offset]
	s.Location = make([]*profile.Location, 0, len(sample.LocationId))
	for _, loc := range sample.LocationId {
		s.Location = append(s.Location, locations[loc-1])
	}
	s.Value = sample.Value
	return &s
}

func (pw *profileMerger) unpackLocations(idx uint64, functions []*profile.Function, mappings []*profile.Mapping) ([]*profile.Location, error) {
	if idx > uint64(len(pw.mergedProfile.NumSampleTypes)) {
		return nil, errors.New("index out of range")
	}

	numLocations := pw.mergedProfile.NumLocations[idx]

	var offset uint64
	for i := uint64(0); i < idx; i++ {
		offset += pw.mergedProfile.NumLocations[i]
	}

	limit := offset + numLocations
	locations := make([]*profile.Location, 0, numLocations)
	for offset < limit {
		locations = append(locations, pw.unpackLocation(offset, functions, mappings))
		offset++
	}

	return locations, nil
}

func (pw *profileMerger) unpackMapping(offset uint64) *profile.Mapping {
	var p profile.Mapping
	//p.ID = uint64(pw.mergedProfile.Mappings[offset])
	//offset++
	//p.Start = uint64(pw.mergedProfile.Mappings[offset])
	//offset++
	//p.Limit = uint64(pw.mergedProfile.Mappings[offset])
	//offset++
	//p.Offset = uint64(pw.mergedProfile.Mappings[offset])
	//offset++
	//p.File = pw.getString(int(pw.mergedProfile.Mappings[offset]))
	//offset++
	//p.BuildID = pw.getString(int(pw.mergedProfile.Mappings[offset]))
	//offset++

	return &p
}

func (pw *profileMerger) unpackLocation(offset uint64, functions []*profile.Function, mappings []*profile.Mapping) *profile.Location {
	var p profile.Location
	location := pw.mergedProfile.Locations[offset]
	p.Mapping = mappings[location.MappingId-1]
	p.Address = location.Address
	p.ID = location.Id
	p.Line = pw.unpackLines(location.Line, functions)
	return &p
}

func (pw *profileMerger) unpackLines(lines []*Line, functions []*profile.Function) []profile.Line {
	result := make([]profile.Line, 0, len(lines))

	for _, line := range lines {
		result = append(result, profile.Line{
			Line:     line.Line,
			Function: functions[line.FunctionId-1],
		})
	}

	return result
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
		MappingId: loc.Mapping.ID,
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

func (pw *profileMerger) getString(id int) string {
	if id < 0 || id > len(pw.mergedProfile.StringTable) {
		return ""
	}
	return pw.mergedProfile.StringTable[id]
}

func (pw *profileMerger) getMappingKey(m *profile.Mapping) mappingKey {
	// Normalize addresses to handle address space randomization.
	// Round up to next 4K boundary to avoid minor discrepancies.
	const mapsizeRounding = 0x1000

	size := m.Limit - m.Start
	size = size + mapsizeRounding - 1
	size = size - (size % mapsizeRounding)
	key := mappingKey{
		size:   size,
		offset: m.Offset,
	}

	switch {
	case m.BuildID != "":
		key.buildIDOrFile = int64(pw.putString(m.BuildID))
	case m.File != "":
		key.buildIDOrFile = int64(pw.putString(m.File))
	default:
	}

	return key
}

func (pw *profileMerger) getFunctionKey(fn *profile.Function) functionKey {
	return functionKey{
		name:       int64(pw.putString(fn.Name)),
		systemName: int64(pw.putString(fn.SystemName)),
		filename:   int64(pw.putString(fn.Filename)),
		startLine:  fn.StartLine,
	}
}

func (pw *profileMerger) getLocationKey(l *Location) locationKey {
	key := locationKey{
		addr: l.Address,
	}

	mapping := pw.mappingsByID[l.MappingId]
	if mapping.m != nil {
		key.addr -= mapping.m.MemoryStart
		key.mappingID = l.MappingId
	}
	lines := make([]string, len(l.Line)*2)
	for i, line := range l.Line {
		lines[i*2] = strconv.FormatUint(line.FunctionId, 16)
		lines[i*2+1] = strconv.FormatInt(line.Line, 16)
	}
	key.lines = strings.Join(lines, "|")
	return key
}

func (pw *profileMerger) putLocation(src *profile.Location) *Location {
	if src == nil {
		return nil
	}

	if l, ok := pw.locationsByID[src.ID]; ok {
		pw.locationsByID[src.ID] = l
		return l
	}

	mi := pw.putMapping(src.Mapping)
	l := &Location{
		Id:        uint64(len(pw.mergedProfile.Locations) + 1),
		MappingId: mi.m.Id,
		Address:   uint64(int64(src.Address) + mi.offset),
		Line:      make([]*Line, len(src.Line)),
	}
	for i, ln := range src.Line {
		l.Line[i] = pw.putLine(ln)
	}

	k := pw.getLocationKey(l)
	if ll, ok := pw.locationTable[k]; ok {
		pw.locationsByID[src.ID] = ll
		return ll
	}

	pw.locationsByID[src.ID] = l
	pw.locationTable[k] = l
	pw.mergedProfile.Locations = append(pw.mergedProfile.Locations, l)
	return l
}

func (pw *profileMerger) putMapping(src *profile.Mapping) mapInfo {
	if src == nil {
		return mapInfo{}
	}

	if mi, ok := pw.mappingsByID[src.ID]; ok {
		return mi
	}

	mk := pw.getMappingKey(src)
	if m, ok := pw.mappingTable[mk]; ok {
		mi := mapInfo{m, int64(m.MemoryStart) - int64(src.Start)}
		pw.mappingsByID[src.ID] = mi
		return mi
	}

	m := &Mapping{
		Id:          uint64(len(pw.mergedProfile.Mappings) + 1),
		MemoryStart: src.Start,
		MemoryLimit: src.Limit,
		FileOffset:  src.Offset,
		Filename:    int64(pw.putString(src.File)),
		BuildId:     int64(pw.putString(src.BuildID)),
	}

	pw.mergedProfile.Mappings = append(pw.mergedProfile.Mappings, m)

	pw.mappingTable[mk] = m
	mi := mapInfo{m, 0}
	pw.mappingsByID[src.ID] = mi
	return mi
}

func (pw *profileMerger) putLine(src profile.Line) *Line {
	return &Line{
		FunctionId: pw.putFunction(src.Function),
		Line:       src.Line,
	}
}

func (pw *profileMerger) putFunction(src *profile.Function) uint64 {
	if src == nil {
		return math.MaxUint64
	}

	if f, ok := pw.functionsByID[src.ID]; ok {
		return f.Id
	}

	key := pw.getFunctionKey(src)
	if f, ok := pw.functionTable[key]; ok {
		pw.functionsByID[src.ID] = f
		return f.Id
	}
	f := &Function{
		Id:         uint64(len(pw.mergedProfile.Functions) + 1),
		Name:       int64(pw.putString(src.Name)),
		SystemName: int64(pw.putString(src.SystemName)),
		Filename:   int64(pw.putString(src.Filename)),
		StartLine:  src.StartLine,
	}
	pw.functionTable[key] = f
	pw.functionsByID[src.ID] = f
	pw.mergedProfile.Functions = append(pw.mergedProfile.Functions, f)
	return f.Id
}

func (pw *profileMerger) getFunction(id int) *profile.Function {
	if id < 0 || id > len(pw.mergedProfile.Functions) {
		return nil
	}
	fn := pw.mergedProfile.Functions[id]
	return &profile.Function{
		ID:         fn.Id,
		Name:       pw.getString(int(fn.Name)),
		SystemName: pw.getString(int(fn.SystemName)),
		Filename:   pw.getString(int(fn.Filename)),
		StartLine:  fn.StartLine,
	}
}
