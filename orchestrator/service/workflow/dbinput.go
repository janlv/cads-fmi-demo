package workflow

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/norceresearch/cads-fmi-demo/orchestrator/service/internal/cadsdb"
	"github.com/norceresearch/cads-fmi-demo/orchestrator/service/internal/fmi"
)

// Input dataset sources recorded in `_run.steps[].fmus[].inputs` (ARCH-COMP-018).
const (
	InputSourceDB         = "db"
	InputSourceCSV        = "csv"
	InputSourceS3         = "s3"
	InputSourceDBFallback = "db-fallback-csv"
)

const dbReadTimeout = 2 * time.Minute

// InputDataset records which dataset fed one FMU input variable.
type InputDataset struct {
	Variable string `json:"variable"`
	Source   string `json:"source"`
	Dataset  string `json:"dataset"`
	From     string `json:"from,omitempty"`
	To       string `json:"to,omitempty"`
	Rows     int    `json:"rows"`
}

// dbInputSeriesSpec is `input_series.db`: FMU variables read from <site>.measurements over a
// time window that maps to simulation time 0..(to-from) seconds.
type dbInputSeriesSpec struct {
	Site     string            `yaml:"site"`
	Channels map[string]string `yaml:"channels"`
	Window   *dbWindowSpec     `yaml:"window"`
	Fallback *dbFallbackSpec   `yaml:"fallback"`
}

type dbWindowSpec struct {
	From string `yaml:"from"`
	To   string `yaml:"to"`
}

type dbFallbackSpec struct {
	CSV string `yaml:"csv"`
}

type parsedDBSpec struct {
	site        string
	variables   []string // sorted FMU variable names
	channels    map[string]string
	from, to    time.Time // zero for an unresolved data_window (fallback without database)
	fallback    string    // resolved absolute path, or ""
	fallbackRel string
	// windowed is set when the window comes from the workflow's data_window: the CSV then
	// starts at simStart and every channel needs a sample exactly at from.
	windowed bool
	simStart float64
}

// WithSeriesReader installs the database reader for `input_series.db` (tests, or a shared
// connection). Without it the executor connects lazily when CADS_DB_URL is set.
func WithSeriesReader(reader cadsdb.SeriesReader) Option {
	return func(e *Executor) {
		e.seriesReader = reader
	}
}

func (e *Executor) parseDBSpec(spec *dbInputSeriesSpec) (*parsedDBSpec, error) {
	site := strings.TrimSpace(spec.Site)
	if err := cadsdb.CheckSite(site); err != nil {
		return nil, fmt.Errorf("input_series.db.site: %w", err)
	}
	if len(spec.Channels) == 0 {
		return nil, fmt.Errorf("input_series.db.channels must map at least one FMU variable to a channel")
	}
	parsed := &parsedDBSpec{site: site, channels: make(map[string]string, len(spec.Channels))}
	for variable, channel := range spec.Channels {
		variable = strings.TrimSpace(variable)
		channel = strings.TrimSpace(channel)
		if variable == "" || channel == "" {
			return nil, fmt.Errorf("input_series.db.channels entries need a variable and a channel name")
		}
		if strings.ContainsAny(variable, ",\"\n\r") {
			return nil, fmt.Errorf("input_series.db.channels: invalid variable name %q", variable)
		}
		parsed.channels[variable] = channel
		parsed.variables = append(parsed.variables, variable)
	}
	sort.Strings(parsed.variables)
	switch {
	case spec.Window == nil && e.window != nil:
		// The workflow's data_window supplies the window (zero times when unresolved).
		parsed.windowed = true
		parsed.from, parsed.to = e.window.from, e.window.to
		parsed.simStart = e.window.simStart
	case spec.Window == nil:
		return nil, fmt.Errorf("input_series.db.window with from/to is required")
	default:
		from, err := time.Parse(time.RFC3339, strings.TrimSpace(spec.Window.From))
		if err != nil {
			return nil, fmt.Errorf("input_series.db.window.from must be RFC3339: %w", err)
		}
		to, err := time.Parse(time.RFC3339, strings.TrimSpace(spec.Window.To))
		if err != nil {
			return nil, fmt.Errorf("input_series.db.window.to must be RFC3339: %w", err)
		}
		if !to.After(from) {
			return nil, fmt.Errorf("input_series.db.window.to must be after window.from")
		}
		parsed.from, parsed.to = from.UTC(), to.UTC()
	}
	if spec.Fallback != nil && strings.TrimSpace(spec.Fallback.CSV) != "" {
		path, err := e.resolveRepoPath(strings.TrimSpace(spec.Fallback.CSV), "input series fallback")
		if err != nil {
			return nil, err
		}
		parsed.fallback = path
		parsed.fallbackRel = e.relPath(path)
	}
	return parsed, nil
}

func (e *Executor) buildDBInputSeries(spec *dbInputSeriesSpec) (*resolvedInputSeries, error) {
	parsed, err := e.parseDBSpec(spec)
	if err != nil {
		return nil, err
	}
	reader, err := e.dbReader()
	if err != nil {
		return nil, err
	}
	if reader == nil {
		if parsed.fallback == "" {
			return nil, fmt.Errorf("input_series.db: database not configured (%s) and no fallback", cadsdb.EnvURL)
		}
		return e.buildDBFallback(parsed)
	}
	if parsed.windowed && !e.window.resolved() {
		return nil, fmt.Errorf("input_series.db: data_window is not resolved")
	}

	channelSet := make(map[string]struct{}, len(parsed.channels))
	var channels []string
	for _, variable := range parsed.variables {
		channel := parsed.channels[variable]
		if _, seen := channelSet[channel]; !seen {
			channelSet[channel] = struct{}{}
			channels = append(channels, channel)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), dbReadTimeout)
	defer cancel()
	rows, err := reader.ReadSeries(ctx, parsed.site, channels, parsed.from, parsed.to)
	if err != nil {
		return nil, fmt.Errorf("input_series.db: %w", err)
	}
	var csvText string
	var counts map[string]int
	if parsed.windowed {
		csvText, counts, err = buildWindowedDBSeriesCSV(parsed.variables, parsed.channels, parsed.from, parsed.simStart, rows)
	} else {
		csvText, counts, err = buildDBSeriesCSV(parsed.variables, parsed.channels, parsed.from, rows)
	}
	if err != nil {
		return nil, fmt.Errorf("input_series.db (%s, %s..%s): %w", parsed.site,
			parsed.from.Format(time.RFC3339), parsed.to.Format(time.RFC3339), err)
	}
	path, cleanup, err := writeTempCSV("cads-db-input-*.csv", csvText)
	if err != nil {
		return nil, err
	}
	e.logf("[workflow] input_series.db: read %d samples from %s.measurements", len(rows), parsed.site)

	inputs := make([]InputDataset, 0, len(parsed.variables))
	for _, variable := range parsed.variables {
		channel := parsed.channels[variable]
		inputs = append(inputs, InputDataset{
			Variable: variable,
			Source:   InputSourceDB,
			Dataset:  parsed.site + ".measurements/" + channel,
			From:     parsed.from.Format(time.RFC3339),
			To:       parsed.to.Format(time.RFC3339),
			Rows:     counts[channel],
		})
	}
	return &resolvedInputSeries{Config: &fmi.InputSeriesConfig{CSVPath: path}, Cleanup: cleanup, Inputs: inputs}, nil
}

// dbReader returns the configured reader, connecting on first use when CADS_DB_URL is set; nil
// means no database is configured.
func (e *Executor) dbReader() (cadsdb.SeriesReader, error) {
	if e.seriesReader != nil {
		return e.seriesReader, nil
	}
	url := cadsdb.URLFromEnv()
	if url == "" {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, err := cadsdb.Open(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("input_series.db: %w", err)
	}
	e.seriesReader = store
	e.ownedStore = store
	return store, nil
}

// closeOwnedStore releases a connection the executor opened itself.
func (e *Executor) closeOwnedStore() {
	if e.ownedStore != nil {
		e.ownedStore.Close()
		e.ownedStore = nil
		e.seriesReader = nil
	}
}

// buildDBSeriesCSV turns time-ordered samples into the bridge's input CSV: header
// `time,<var>...`, time in seconds since from, one row per distinct timestamp, each channel
// carrying its last value forward. The first row must have a sample for every channel.
// It also returns the sample count per channel.
func buildDBSeriesCSV(variables []string, channels map[string]string, from time.Time, rows []cadsdb.Row) (string, map[string]int, error) {
	return buildSeriesCSV(variables, channels, from, 0, false, rows)
}

// buildWindowedDBSeriesCSV is buildDBSeriesCSV for a data_window: time is
// `simStart + (at - from)` seconds and every channel must have a sample exactly at from, so
// consecutive assessments join without a gap (the previous run's last held sample is the
// first sample of this one).
func buildWindowedDBSeriesCSV(variables []string, channels map[string]string, from time.Time, simStart float64, rows []cadsdb.Row) (string, map[string]int, error) {
	return buildSeriesCSV(variables, channels, from, simStart, true, rows)
}

func buildSeriesCSV(variables []string, channels map[string]string, from time.Time, offset float64, requireAtFrom bool, rows []cadsdb.Row) (string, map[string]int, error) {
	counts := make(map[string]int)
	if len(rows) == 0 {
		return "", counts, fmt.Errorf("no samples in window for channel(s) %s", strings.Join(uniqueChannels(variables, channels), ", "))
	}
	sorted := append([]cadsdb.Row(nil), rows...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Time.Before(sorted[j].Time) })
	if requireAtFrom {
		atFrom := make(map[string]struct{})
		for _, row := range sorted {
			if row.Time.Equal(from) {
				atFrom[row.Channel] = struct{}{}
			}
		}
		var missing []string
		for _, channel := range uniqueChannels(variables, channels) {
			if _, ok := atFrom[channel]; !ok {
				missing = append(missing, channel)
			}
		}
		if len(missing) > 0 {
			return "", counts, fmt.Errorf("channel(s) %s have no sample exactly at the window start %s (data_window windows must start on a sample time shared by all channels)",
				strings.Join(missing, ", "), from.UTC().Format(time.RFC3339Nano))
		}
	}

	var builder strings.Builder
	builder.WriteString("time")
	for _, variable := range variables {
		builder.WriteString(",")
		builder.WriteString(variable)
	}
	builder.WriteString("\n")

	last := make(map[string]float64)
	writeRow := func(at time.Time) {
		builder.WriteString(formatFloat(offset + at.Sub(from).Seconds()))
		for _, variable := range variables {
			builder.WriteString(",")
			builder.WriteString(formatFloat(last[channels[variable]]))
		}
		builder.WriteString("\n")
	}

	first := true
	for i := 0; i < len(sorted); {
		at := sorted[i].Time
		for ; i < len(sorted) && sorted[i].Time.Equal(at); i++ {
			last[sorted[i].Channel] = sorted[i].Value
			counts[sorted[i].Channel]++
		}
		if first {
			for _, channel := range uniqueChannels(variables, channels) {
				if _, ok := last[channel]; !ok {
					return "", counts, fmt.Errorf("channel %s has no sample at the first timestamp %s (all channels must start together)",
						channel, at.UTC().Format(time.RFC3339Nano))
				}
			}
			first = false
		}
		writeRow(at)
	}
	return builder.String(), counts, nil
}

func uniqueChannels(variables []string, channels map[string]string) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, variable := range variables {
		channel := channels[variable]
		if _, ok := seen[channel]; ok {
			continue
		}
		seen[channel] = struct{}{}
		out = append(out, channel)
	}
	return out
}

// buildDBFallback uses the repo-relative fallback CSV (time in seconds). Header columns named
// after a channel instead of the FMU variable are renamed into a temporary copy.
func (e *Executor) buildDBFallback(parsed *parsedDBSpec) (*resolvedInputSeries, error) {
	header, rows, err := csvSummary(parsed.fallback)
	if err != nil {
		return nil, fmt.Errorf("input_series.db fallback: %w", err)
	}
	byChannel := make(map[string]string, len(parsed.channels))
	for _, variable := range parsed.variables {
		if _, taken := byChannel[parsed.channels[variable]]; !taken {
			byChannel[parsed.channels[variable]] = variable
		}
	}
	renamed := append([]string(nil), header...)
	changed := false
	for i := 1; i < len(renamed); i++ {
		if _, isVar := parsed.channels[renamed[i]]; isVar {
			continue
		}
		if variable, ok := byChannel[renamed[i]]; ok {
			renamed[i] = variable
			changed = true
		}
	}
	present := make(map[string]struct{}, len(renamed))
	for _, column := range renamed[1:] {
		present[column] = struct{}{}
	}
	for _, variable := range parsed.variables {
		if _, ok := present[variable]; !ok {
			return nil, fmt.Errorf("input_series.db fallback %s has no column for %s (channel %s)",
				parsed.fallbackRel, variable, parsed.channels[variable])
		}
	}

	resolved := &resolvedInputSeries{Config: &fmi.InputSeriesConfig{CSVPath: parsed.fallback}}
	if changed {
		data, err := os.ReadFile(parsed.fallback)
		if err != nil {
			return nil, fmt.Errorf("input_series.db fallback: %w", err)
		}
		text := string(data)
		body := ""
		if idx := strings.IndexByte(text, '\n'); idx >= 0 {
			body = text[idx+1:]
		}
		path, cleanup, err := writeTempCSV("cads-db-fallback-*.csv", strings.Join(renamed, ",")+"\n"+body)
		if err != nil {
			return nil, err
		}
		resolved.Config.CSVPath = path
		resolved.Cleanup = cleanup
	}
	e.logf("[workflow] input_series.db: %s not set, using fallback %s", cadsdb.EnvURL, parsed.fallbackRel)
	if parsed.windowed && e.window != nil {
		span := e.window.simStop - e.window.simStart
		if last, err := csvLastTime(parsed.fallback); err == nil {
			if covered := last - firstTimeOr0(parsed.fallback); covered < span {
				fmt.Fprintf(os.Stderr, "[workflow] warning: input_series.db fallback %s covers %g s, shorter than the %g s data_window span; its last values are held\n",
					parsed.fallbackRel, covered, span)
			}
		}
	}
	for _, variable := range parsed.variables {
		input := InputDataset{
			Variable: variable,
			Source:   InputSourceDBFallback,
			Dataset:  parsed.fallbackRel,
			Rows:     rows,
		}
		if !parsed.from.IsZero() {
			input.From = parsed.from.Format(time.RFC3339)
			input.To = parsed.to.Format(time.RFC3339)
		}
		resolved.Inputs = append(resolved.Inputs, input)
	}
	return resolved, nil
}

// csvLastTime returns the time column of the last non-empty data row.
func csvLastTime(path string) (float64, error) {
	return csvTimeAt(path, true)
}

func firstTimeOr0(path string) float64 {
	first, err := csvTimeAt(path, false)
	if err != nil {
		return 0
	}
	return first
}

func csvTimeAt(path string, last bool) (float64, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	header := true
	found := ""
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if header {
			header = false
			continue
		}
		if line == "" {
			continue
		}
		found = line
		if !last {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	if found == "" {
		return 0, fmt.Errorf("%s has no data rows", path)
	}
	column, _, _ := strings.Cut(found, ",")
	return strconv.ParseFloat(strings.Trim(strings.TrimSpace(column), `"`), 64)
}

// fileInputs describes a CSV-backed input series for provenance; read errors are left to the
// bridge, which reports them with full context.
func fileInputs(path, source, dataset string) []InputDataset {
	header, rows, err := csvSummary(path)
	if err != nil || len(header) < 2 {
		return []InputDataset{}
	}
	inputs := make([]InputDataset, 0, len(header)-1)
	for _, variable := range header[1:] {
		inputs = append(inputs, InputDataset{Variable: variable, Source: source, Dataset: dataset, Rows: rows})
	}
	return inputs
}

// csvSummary returns the trimmed header columns and the number of non-empty data rows.
func csvSummary(path string) ([]string, int, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return nil, 0, err
		}
		return nil, 0, fmt.Errorf("%s is empty", path)
	}
	var header []string
	for _, column := range strings.Split(strings.TrimRight(scanner.Text(), "\r"), ",") {
		header = append(header, strings.Trim(strings.TrimSpace(column), `"`))
	}
	rows := 0
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) != "" {
			rows++
		}
	}
	return header, rows, scanner.Err()
}

func writeTempCSV(pattern, content string) (string, func(), error) {
	file, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", nil, fmt.Errorf("create temp input CSV: %w", err)
	}
	path := file.Name()
	if _, err := file.WriteString(content); err != nil {
		file.Close()
		os.Remove(path)
		return "", nil, fmt.Errorf("write temp input CSV: %w", err)
	}
	if err := file.Close(); err != nil {
		os.Remove(path)
		return "", nil, fmt.Errorf("close temp input CSV: %w", err)
	}
	return path, func() { _ = os.Remove(path) }, nil
}
