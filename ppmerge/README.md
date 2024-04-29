# What?!

Simple! Just merge multiple pprof profiles into a single one 

## Why?

Very useful in cases when you need to store your profiles in some object storage (s3 for instance). 
As number of profiles grows, the storage requirements do as well. 

There's already a way to represent several profiles in a compact way by just using
Merge function in pprof package, but the latter doesn't offer ability to recover any of the 
input profiles. 

## How it works?

In order to understand how merger works, we need to dig a bit deeper into a profile structure

```go
// Profile is an in-memory representation of profile.proto.
type Profile struct {
	SampleType        []*ValueType
	DefaultSampleType string
	Sample            []*Sample
	Mapping           []*Mapping
	Location          []*Location
	Function          []*Function
	Comments          []string

	DropFrames string
	KeepFrames string

	TimeNanos     int64
	DurationNanos int64
	PeriodType    *ValueType
	Period        int64

	// The following fields are modified during encoding and copying,
	// so are protected by a Mutex.
	encodeMu sync.Mutex

	commentX           []int64
	dropFramesX        int64
	keepFramesX        int64
	stringTable        []string
	defaultSampleTypeX int64
}
```

In this profile, we have a metadata, such as functions, mappings, and locations and 
actual data, such as samples. Samples represent actual values, which can mean different things depending on 
sample type, i.e. number of allocations, cpu nanoseconds, etc....

Now let's get to the merging process. In the very beginning we could merge profiles into a single one 
ignoring any compaction-related optimizations, but it would be pointless if our end goal is to save some space. 

First of all, if one examines samples one will notice that instead of storing slice of ids to locations they store the slice of locations. 
Storing ids makes more sense, as we can easily recover initial profile by consulting "metadata storage" inside our merged profile. 

Second optimization was targeted towards efficient storage of strings. All strings were replaced by integer references to string table, which 
is stored as a slice. 

Third optimization consisted in eliminating unused metadata. During merge process algorithm stores only metadata that samples have reference to.

## How to recover profiles

It is assumed that you "remember" the order profiles were passed to merge function. 
If you have additional storage like PostgreSQL or Clickhouse to store profiles' metadata, you can follow the scheme below

## Space optimization
Unlike pprof.Merge, this merge algorithm is able to store profiles of any sample type.

