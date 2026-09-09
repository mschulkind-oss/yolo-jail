package stores

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// MaxSamples bounds the ledger: the last 30 samples per store survive a write,
// the older ones are dropped by the same write that appends.
//
// THE BOUND IS THE RULING, not a tuning knob (OQ-BF9): "size it bounded (last 30
// samples per store) so the handle on growth cannot itself become a store that
// grows." Thirty daily runs is a month of history in ~1.5 kB per store, which is
// enough to see a rate and small enough that the ledger never becomes a row in
// its own inventory worth reclaiming.
const MaxSamples = 30

// sampleExt is the ledger file's extension. One file per store, named by the
// store's stable Key, so a store that disappears leaves its history behind
// instead of shifting everything else's.
const sampleExt = ".samples"

// Sample is one dated measurement of one store — the ledger's line format,
// which is this implementation's own choice (the ruling fixed the CADENCE, the
// BOUND and the writer, not the bytes on disk):
//
//	2026-09-08T14:02:11Z measured 14382919168 0
//	<RFC3339 UTC>        <sizing> <bytes>     <count>
//
// Space-separated, one line, newest last, exactly like the load sentinel's
// read-append-trim-write shape (image.AddLoadedPath) that this mirrors. The
// SIZING IS RECORDED, not just the number: a rate computed across a partial
// sample and a complete one is a fabricated rate, and without the field on disk
// nothing downstream could tell them apart.
type Sample struct {
	At     time.Time `json:"at"`
	Sizing Sizing    `json:"sizing"`
	Bytes  int64     `json:"bytes"`
	Count  int       `json:"count,omitempty"`
}

// SampleFile returns the ledger path for a store key under dir.
//
// The key is sanitized rather than trusted: a cache subdir's name reaches this
// function straight off the disk, and a name carrying a "/" or a ".." would
// write outside the ledger directory. Every byte that is not [A-Za-z0-9._-]
// becomes "_", and a name that sanitizes to nothing (or to a directory
// traversal) becomes "_".
func SampleFile(dir, key string) string {
	return filepath.Join(dir, sanitizeKey(key)+sampleExt)
}

func sanitizeKey(key string) string {
	var b strings.Builder
	for _, r := range key {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := b.String()
	if out == "" || out == "." || out == ".." || strings.Trim(out, ".") == "" {
		return "_"
	}
	return out
}

// ReadSamples returns one store's recorded samples, oldest first. A missing
// ledger is not an error — it is a store nobody has sampled yet, which is every
// store on the first run. Unparseable lines are DROPPED rather than failing the
// read: the ledger is a convenience for a growth column, and a corrupted line
// must never take out the inventory that reads it.
func ReadSamples(dir, key string) []Sample {
	data, err := os.ReadFile(SampleFile(dir, key))
	if err != nil {
		return nil
	}
	var out []Sample
	for _, line := range strings.Split(string(data), "\n") {
		if s, ok := parseSample(line); ok {
			out = append(out, s)
		}
	}
	return out
}

func parseSample(line string) (Sample, bool) {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) < 3 {
		return Sample{}, false
	}
	at, err := time.Parse(time.RFC3339, fields[0])
	if err != nil {
		return Sample{}, false
	}
	bytes, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil {
		return Sample{}, false
	}
	s := Sample{At: at, Sizing: Sizing(fields[1]), Bytes: bytes}
	if len(fields) >= 4 {
		if n, err := strconv.Atoi(fields[3]); err == nil {
			s.Count = n
		}
	}
	return s, true
}

// AppendSample writes one dated line for one store, keeping the last MaxSamples.
//
// Read-append-trim-write, the shape image.AddLoadedPath uses for the load
// sentinel: the trim happens on the WRITE, so the bound holds for a ledger that
// was already over it (a file hand-edited, or written by a build with a larger
// cap) rather than only for files this code grew one line at a time.
//
// It is the ONLY writer in this package, and `yolo stores` is the only caller of
// record() — the single-writer half of the ruling is a property of there being
// one call site, so a second one is the defect to look for.
func AppendSample(dir, key string, s Sample) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	samples := ReadSamples(dir, key)
	samples = append(samples, s)
	if len(samples) > MaxSamples {
		samples = samples[len(samples)-MaxSamples:]
	}
	var b strings.Builder
	for _, x := range samples {
		fmt.Fprintf(&b, "%s %s %d %d\n", x.At.UTC().Format(time.RFC3339), x.Sizing, x.Bytes, x.Count)
	}
	return os.WriteFile(SampleFile(dir, key), []byte(b.String()), 0o644)
}

// record appends this run's sample for every store that produced a size, and
// returns how many lines it wrote plus every store it could not write.
//
// A store whose size is UNKNOWN is recorded too, with its sizing: "I looked and
// could not read it" is a fact about a date, and a gap in the ledger would later
// be indistinguishable from a run that never happened. What such a line must
// never do is enter a rate — growthFrom only pairs measured samples.
func record(rep Report, o Options) (int, []string) {
	dir := o.SamplesDir()
	written := 0
	var errs []string
	for _, s := range rep.Stores {
		if s.Sizing == SizingCached {
			continue // not a new measurement; recording it would double-count a date
		}
		err := AppendSample(dir, s.Key, Sample{
			At: o.Now().UTC(), Sizing: s.Sizing, Bytes: s.Bytes, Count: s.Count,
		})
		if err != nil {
			errs = append(errs, s.Key+": "+err.Error())
			continue
		}
		written++
	}
	return written, errs
}

// attachGrowth fills each store's growth column from the ledger — READ ONLY, and
// called before record() so this run's own line can never be one of the two
// dated points a rate is computed from.
func attachGrowth(rep *Report, o Options) {
	dir := o.SamplesDir()
	for i := range rep.Stores {
		rep.Stores[i].Growth = growthFrom(ReadSamples(dir, rep.Stores[i].Key), rep.Stores[i], o.Now())
	}
}

// minGrowthWindow is the shortest span two samples may straddle and still yield
// a rate. Two runs a minute apart differ by noise, and dividing that noise by
// 1/1440 of a day manufactures a number with the magnitude of a catastrophe.
const minGrowthWindow = time.Hour

// growthFrom computes a bytes-per-day rate between the OLDEST usable recorded
// sample and this run's measurement.
//
// Oldest rather than previous, deliberately: the design's own growth figures
// ("≈ 0.43 GB/day") are long-baseline rates, and a store measured daily has a
// day-to-day delta dominated by whatever ran that day. The window is reported
// alongside the rate so the number is never read as instantaneous.
//
// Only MEASURED samples on both ends. A partial figure is a lower bound, so a
// rate touching one is a lower bound divided by a real interval — a number with
// no honest name. Such a store shows no rate at all, which is the correct answer
// until it can be walked completely.
func growthFrom(samples []Sample, cur Store, now time.Time) *Growth {
	if cur.Sizing != SizingMeasured {
		return nil
	}
	for _, s := range samples {
		if s.Sizing != SizingMeasured {
			continue
		}
		span := now.Sub(s.At)
		if span < minGrowthWindow {
			continue // and every later sample is newer still
		}
		days := span.Hours() / 24
		return &Growth{
			BytesPerDay: int64(float64(cur.Bytes-s.Bytes) / days),
			Days:        days,
			Since:       s.At.UTC(),
			Samples:     len(samples),
		}
	}
	return nil
}
