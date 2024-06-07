package ppmerge

import "github.com/google/pprof/profile"

func (p *Profile) From(src *profile.Profile) {
	m := map[string]uint64{}

	p.Sample = make([]*Sample, len(src.Sample))
	for i, sample := range src.Sample {
		p.Sample[i] = &Sample{
			Value: make([]int64, len(sample.Value)),
		}
		copy(p.Sample[i].Value, sample.Value)
	}
	p.Function = make([]*Function, len(src.Function))
	for i, f := range src.Function {
		p.Function[i] = &Function{
			Id:         f.ID,
			Name:       int64(p.putString(f.Name, m)),
			SystemName: int64(p.putString(f.SystemName, m)),
			Filename:   int64(p.putString(f.Filename, m)),
			StartLine:  f.StartLine,
		}
	}

	p.Location = make([]*Location, len(src.Location))
	for i, loc := range src.Location {
		p.Location[i] = &Location{
			Id:       loc.ID,
			Address:  loc.Address,
			IsFolded: loc.IsFolded,
		}

		if loc.Mapping != nil {
			p.Location[i].MappingId = loc.Mapping.ID
		}

		if len(loc.Line) > 0 {
			p.Location[i].Line = make([]*Line, len(loc.Line))
			for j, l := range loc.Line {
				p.Location[i].Line[j] = &Line{
					Line: l.Line,
				}
				if l.Function != nil {
					p.Location[i].Line[j].FunctionId = l.Function.ID
				}
			}
		}
	}

	p.Mapping = make([]*Mapping, len(src.Mapping))
	for i, sm := range src.Mapping {
		p.Mapping[i] = &Mapping{
			Id:              sm.ID,
			MemoryStart:     sm.Start,
			MemoryLimit:     sm.Limit,
			FileOffset:      sm.Offset,
			Filename:        int64(p.putString(sm.File, m)),
			BuildId:         int64(p.putString(sm.BuildID, m)),
			HasInlineFrames: sm.HasInlineFrames,
			HasLineNumbers:  sm.HasLineNumbers,
			HasFunctions:    sm.HasFunctions,
			HasFilenames:    sm.HasFilenames,
		}
	}

	p.SampleType = make([]*ValueType, len(src.SampleType))
	for i, t := range src.SampleType {
		p.SampleType[i] = &ValueType{
			Type: int64(p.putString(t.Type, m)),
			Unit: int64(p.putString(t.Unit, m)),
		}
	}

	p.Comment = make([]int64, len(src.Comments))
	for i, c := range src.Comments {
		p.Comment[i] = int64(p.putString(c, m))
	}

	p.DefaultSampleType = int64(p.putString(src.DefaultSampleType, m))
	p.KeepFrames = int64(p.putString(src.KeepFrames, m))
	p.DropFrames = int64(p.putString(src.DropFrames, m))
	p.DurationNanos = src.DurationNanos
	p.TimeNanos = src.TimeNanos
	p.Period = src.Period

	p.StringTable = make([]string, len(m))
	for s, i := range m {
		p.StringTable[i] = s
	}
}

func (p *Profile) putString(val string, m map[string]uint64) uint64 {
	if id, ok := m[val]; ok {
		return id
	}
	nextID := uint64(len(m))
	m[val] = nextID
	return nextID
}
