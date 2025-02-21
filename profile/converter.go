package profile

import "github.com/google/pprof/profile"

func (p *Profile) From(src *profile.Profile) {
	m := map[string]uint64{}

	p.convertSamples(src.Sample, m)
	p.convertFunctions(src.Function, m)
	p.convertLocations(src.Location)
	p.convertMappings(src.Mapping, m)

	p.SampleType = make([]*ValueType, len(src.SampleType))
	for i, t := range src.SampleType {
		p.SampleType[i] = &ValueType{
			Type: int64(p.putString(t.Type, m)),
			Unit: int64(p.putString(t.Unit, m)),
		}
	}

	p.PeriodType = &ValueType{
		Type: int64(p.putString(src.PeriodType.Type, m)),
		Unit: int64(p.putString(src.PeriodType.Unit, m)),
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

	p.StringTable = make([]string, len(m)+1)
	for s, i := range m {
		p.StringTable[i] = s
	}
}

func ConvertLabels(s *profile.Sample, labels *Labels, stringTable []string) {
	for _, label := range labels.GetLabels() {
		key := stringTable[label.GetKey()]
		if strIdx := label.GetStr(); strIdx > 0 {
			if s.Label == nil {
				s.Label = make(map[string][]string)
			}
			s.Label[key] = []string{stringTable[strIdx]}
		} else if label.GetNum() != 0 || label.GetNumUnit() > 0 {
			if s.NumLabel == nil {
				s.NumLabel = make(map[string][]int64)
				s.NumUnit = make(map[string][]string)
			}
			s.NumLabel[key] = []int64{label.GetNum()}
			s.NumUnit[key] = []string{stringTable[label.GetNumUnit()]}
		}
	}
}

func (p *Profile) convertSamples(samples []*profile.Sample, m map[string]uint64) {
	p.Sample = make([]*Sample, len(samples))
	for i, sample := range samples {
		p.Sample[i] = &Sample{
			Value: make([]int64, len(sample.Value)),
		}
		copy(p.Sample[i].Value, sample.Value)

		if sample.Label != nil {
			for key, values := range sample.Label {
				p.Sample[i].Label = append(p.Sample[i].Label, &Label{
					Key: int64(p.putString(key, m)),
					Str: int64(p.putString(values[0], m)),
				})
			}
		}

		if sample.NumLabel != nil {
			for key, values := range sample.NumLabel {
				p.Sample[i].Label = append(p.Sample[i].Label, &Label{
					Key:     int64(p.putString(key, m)),
					Num:     values[0],
					NumUnit: int64(p.putString(sample.NumUnit[key][0], m)),
				})
			}
		}
	}
}

func (p *Profile) convertFunctions(functions []*profile.Function, m map[string]uint64) {
	p.Function = make([]*Function, len(functions))
	for i, f := range functions {
		p.Function[i] = &Function{
			Id:         f.ID,
			Name:       int64(p.putString(f.Name, m)),
			SystemName: int64(p.putString(f.SystemName, m)),
			Filename:   int64(p.putString(f.Filename, m)),
			StartLine:  f.StartLine,
		}
	}
}

func (p *Profile) convertLocations(locations []*profile.Location) {
	p.Location = make([]*Location, len(locations))
	for i, loc := range locations {
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
}

func (p *Profile) convertMappings(mappings []*profile.Mapping, m map[string]uint64) {
	p.Mapping = make([]*Mapping, len(mappings))
	for i, sm := range mappings {
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
}

func (p *Profile) putString(val string, m map[string]uint64) uint64 {
	if id, ok := m[val]; ok {
		return id
	}
	nextID := uint64(len(m)) + 1
	m[val] = nextID
	return nextID
}
