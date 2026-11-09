package engine

import (
	"fmt"
	"time"
)

type faultField struct {
	name string
	get  func(FaultSpec) float64
}

var probabilityFields = []faultField{
	{"crash_on_io", func(f FaultSpec) float64 { return f.CrashOnIO }},
	{"fsync_eio", func(f FaultSpec) float64 { return f.FsyncEIO }},
	{"write_eio", func(f FaultSpec) float64 { return f.WriteEIO }},
	{"write_enospc", func(f FaultSpec) float64 { return f.WriteENOSPC }},
	{"read_eio", func(f FaultSpec) float64 { return f.ReadEIO }},
	{"read_corrupt", func(f FaultSpec) float64 { return f.ReadCorrupt }},
	{"lost_write", func(f FaultSpec) float64 { return f.LostWrite }},
	{"misdirected_write", func(f FaultSpec) float64 { return f.MisdirectedWrite }},
	{"mmap_early_writeback", func(f FaultSpec) float64 { return f.MmapEarlyWriteback }},
	{"udp_loss", func(f FaultSpec) float64 { return f.UDPLoss }},
	{"udp_dup", func(f FaultSpec) float64 { return f.UDPDup }},
}

var rateFields = []faultField{
	{"crash_per_sec", func(f FaultSpec) float64 { return f.CrashPerSec }},
	{"partition_per_sec", func(f FaultSpec) float64 { return f.PartitionPerSec }},
	{"clog_per_sec", func(f FaultSpec) float64 { return f.ClogPerSec }},
	{"swizzle_per_sec", func(f FaultSpec) float64 { return f.SwizzlePerSec }},
	{"reset_per_sec", func(f FaultSpec) float64 { return f.ResetPerSec }},
	{"slow_per_sec", func(f FaultSpec) float64 { return f.SlowPerSec }},
	{"pause_per_sec", func(f FaultSpec) float64 { return f.PausePerSec }},
	{"clock_jump_per_sec", func(f FaultSpec) float64 { return f.ClockJumpPerSec }},
	{"bitrot_per_sec", func(f FaultSpec) float64 { return f.BitrotPerSec }},
}

func fieldNames(fields []faultField) []string {
	names := make([]string, len(fields))
	for i, f := range fields {
		names[i] = f.name
	}
	return names
}

func ProbabilityFields() []string { return fieldNames(probabilityFields) }

func RateFields() []string { return fieldNames(rateFields) }

type namedRange struct {
	name string
	r    Range
}

func (c *Config) ranges() []namedRange {
	f := c.Faults
	out := []namedRange{
		{"latency", c.Latency},
		{"faults.restart_after", f.RestartAfter},
		{"faults.partition_for", f.PartitionFor},
		{"faults.clog_for", f.ClogFor},
		{"faults.clog_delay", f.ClogDelay},
		{"faults.slow_for", f.SlowFor},
		{"faults.pause_for", f.PauseFor},
		{"faults.disk_latency", f.DiskLatency},
	}
	for i, l := range c.Links {
		out = append(out, namedRange{fmt.Sprintf("links[%d].latency", i), l.Latency})
	}
	return out
}

func (c *Config) validateBounds() error {
	for _, p := range probabilityFields {
		if v := p.get(c.Faults); v < 0 || v > 1 {
			return fmt.Errorf("config: faults.%s must be between 0 and 1, got %g", p.name, v)
		}
	}
	for _, r := range rateFields {
		if v := r.get(c.Faults); v < 0 {
			return fmt.Errorf("config: faults.%s must not be negative, got %g", r.name, v)
		}
	}
	for _, r := range c.ranges() {
		if r.r[0] < 0 || r.r[0] > r.r[1] {
			return fmt.Errorf("config: %s must be [min, max] with 0 <= min <= max, got [%s, %s]", r.name, time.Duration(r.r[0]), time.Duration(r.r[1]))
		}
	}
	switch {
	case c.Duration <= 0:
		return fmt.Errorf("config: duration must be positive, got %s", time.Duration(c.Duration))
	case c.Watchdog <= 0:
		return fmt.Errorf("config: watchdog must be positive, got %s", time.Duration(c.Watchdog))
	case c.Faults.ClockJumpMax < 0:
		return fmt.Errorf("config: faults.clock_jump_max must not be negative, got %s", time.Duration(c.Faults.ClockJumpMax))
	}
	return nil
}
