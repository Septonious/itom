//go:build race

package tunnel

// The instrumented binary exercises the complete workload for races/integrity,
// but cannot represent production timing. The release launcher also requires
// the identical normal Go suite with the numerical performance assertions.
const raceInstrumented = true
