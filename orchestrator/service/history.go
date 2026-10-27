package service

import (
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/norceresearch/cads-fmi-demo/orchestrator/service/internal/cadsdb"
)

// Limits of the `/api/sites/{site}/history` query.
const (
	historyDefaultSpan      = "24h"
	historyMaxChannels      = 16
	historyMaxSeries        = 32
	historyMinPoints        = 100
	historyMaxPoints        = 5000
	historyDefaultMaxPoints = 1500
	historyRunListLimit     = 500
	historyMaxRuns          = 200
)

// historySpans maps the accepted `span` values to durations; "all" (0) covers everything stored.
var historySpans = map[string]time.Duration{
	"6h":  6 * time.Hour,
	"24h": 24 * time.Hour,
	"7d":  7 * 24 * time.Hour,
	"30d": 30 * 24 * time.Hour,
	"all": 0,
}

var (
	historyChannelPattern = regexp.MustCompile(`^[a-z0-9_.-]{1,64}$`)
	historySeriesPart     = regexp.MustCompile(`^[A-Za-z0-9_.\-\[\]]{1,128}$`)
)

// historyResponse is the `/api/sites/{site}/history` payload. Times are RFC 3339 strings and,
// for plotting, epoch milliseconds. Anchor and window fields are absent when nothing is stored.
type historyResponse struct {
	Configured   bool                 `json:"configured"`
	Site         string               `json:"site"`
	Span         string               `json:"span"`
	Anchor       *string              `json:"anchor,omitempty"`
	From         *string              `json:"from,omitempty"`
	To           *string              `json:"to,omitempty"`
	FromMs       *int64               `json:"fromMs,omitempty"`
	ToMs         *int64               `json:"toMs,omitempty"`
	BinSeconds   *float64             `json:"binSeconds,omitempty"`
	Truncated    bool                 `json:"truncated"`
	Measurements []historyMeasurement `json:"measurements"`
	Runs         []historyRun         `json:"runs"`
}

// historyMeasurement is one channel's (possibly binned) samples as [epochMs, value] pairs.
type historyMeasurement struct {
	Channel string       `json:"channel"`
	Unit    string       `json:"unit"`
	Points  [][2]float64 `json:"points"`
}

// historyRun is one stored succeeded run whose assessed window overlaps the history range.
// Series stay in simulation seconds; a client maps them to wall time with
// windowFromMs + (t - simStartTime) * 1000.
type historyRun struct {
	RunID        string                                 `json:"runId"`
	WorkflowPath string                                 `json:"workflowPath"`
	Source       string                                 `json:"source"`
	StartedAt    *string                                `json:"startedAt"`
	FinishedAt   *string                                `json:"finishedAt"`
	WindowFrom   string                                 `json:"windowFrom"`
	WindowTo     string                                 `json:"windowTo"`
	WindowFromMs int64                                  `json:"windowFromMs"`
	WindowToMs   int64                                  `json:"windowToMs"`
	SimStartTime float64                                `json:"simStartTime"`
	AnchorSource string                                 `json:"anchorSource"`
	Outputs      map[string]map[string]float64          `json:"outputs"`
	Series       map[string]map[string]*historyRunTrace `json:"series"`
}

type historyRunTrace struct {
	T []float64  `json:"t"`
	V []*float64 `json:"v"`
}

type historyQuery struct {
	span      string
	spanDur   time.Duration
	channels  []string
	series    [][2]string // step, signal
	maxPoints int
}

// windowedRun is a stored run with its resolved assessment window.
type windowedRun struct {
	run      cadsdb.StoredRun
	from, to time.Time
	source   string
}

func parseHistoryQuery(r *http.Request) (historyQuery, error) {
	q := historyQuery{span: historyDefaultSpan, maxPoints: historyDefaultMaxPoints}
	// url.ParseQuery rejects ';' separators that URL.Query() would silently drop.
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return q, fmt.Errorf("invalid query: %v", err)
	}
	if raw := strings.TrimSpace(values.Get("span")); raw != "" {
		q.span = raw
	}
	dur, ok := historySpans[q.span]
	if !ok {
		return q, fmt.Errorf("span must be one of 6h, 24h, 7d, 30d, all")
	}
	q.spanDur = dur

	seen := map[string]bool{}
	for _, raw := range splitList(values["channels"]) {
		if !historyChannelPattern.MatchString(raw) {
			return q, fmt.Errorf("invalid channel %q", raw)
		}
		if !seen[raw] {
			seen[raw] = true
			q.channels = append(q.channels, raw)
		}
	}
	if len(q.channels) > historyMaxChannels {
		return q, fmt.Errorf("at most %d channels", historyMaxChannels)
	}

	seen = map[string]bool{}
	for _, raw := range splitList(values["series"]) {
		step, signal, found := strings.Cut(raw, ":")
		if !found || !historySeriesPart.MatchString(step) || !historySeriesPart.MatchString(signal) {
			return q, fmt.Errorf("invalid series %q (want step:signal)", raw)
		}
		if !seen[raw] {
			seen[raw] = true
			q.series = append(q.series, [2]string{step, signal})
		}
	}
	if len(q.series) > historyMaxSeries {
		return q, fmt.Errorf("at most %d series", historyMaxSeries)
	}

	if raw := strings.TrimSpace(values.Get("max_points")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < historyMinPoints || n > historyMaxPoints {
			return q, fmt.Errorf("max_points must be an integer in %d..%d", historyMinPoints, historyMaxPoints)
		}
		q.maxPoints = n
	}
	return q, nil
}

// splitList splits repeated and comma-separated query values, dropping empty entries.
func splitList(values []string) []string {
	var out []string
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

// chooseBin returns the bucket width in seconds that keeps a channel sampled every
// samplingSeconds within maxPoints over [from, to): 0 (raw samples) when it already fits,
// else ceil(span / maxPoints). An unknown sampling interval is treated as 1 s.
func chooseBin(from, to time.Time, samplingSeconds float64, maxPoints int) float64 {
	spanSeconds := to.Sub(from).Seconds()
	if spanSeconds <= 0 || maxPoints <= 0 {
		return 0
	}
	if samplingSeconds <= 0 {
		samplingSeconds = 1
	}
	if spanSeconds/samplingSeconds <= float64(maxPoints) {
		return 0
	}
	return math.Ceil(spanSeconds / float64(maxPoints))
}

// latestPerWindow keeps, per workflow and window start, only the newest run (by start time,
// then finish time, then run id), so re-runs of the same assessment do not stack up.
func latestPerWindow(runs []windowedRun) []windowedRun {
	best := map[string]int{}
	var out []windowedRun
	for _, run := range runs {
		key := run.run.WorkflowPath + "\x00" + strconv.FormatInt(run.from.UnixNano(), 10)
		if i, ok := best[key]; ok {
			if newerRun(run.run, out[i].run) {
				out[i] = run
			}
			continue
		}
		best[key] = len(out)
		out = append(out, run)
	}
	return out
}

func newerRun(a, b cadsdb.StoredRun) bool {
	if c := compareTimePtr(a.StartedAt, b.StartedAt); c != 0 {
		return c > 0
	}
	if c := compareTimePtr(a.FinishedAt, b.FinishedAt); c != 0 {
		return c > 0
	}
	return a.RunID > b.RunID
}

// compareTimePtr orders nil before any time.
func compareTimePtr(a, b *time.Time) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return -1
	case b == nil:
		return 1
	case a.Before(*b):
		return -1
	case a.After(*b):
		return 1
	}
	return 0
}

func (s *Server) handleSiteHistory(w http.ResponseWriter, r *http.Request) {
	site := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/sites/"), "/history")
	if err := cadsdb.CheckSite(site); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	q, err := parseHistoryQuery(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	response := historyResponse{Configured: s.Data != nil, Site: site, Span: q.span,
		Measurements: []historyMeasurement{}, Runs: []historyRun{}}
	if s.Data == nil {
		writeJSON(w, http.StatusOK, response)
		return
	}
	if err := s.buildHistory(r, site, q, &response); err != nil {
		writeJSONError(w, http.StatusBadGateway, fmt.Sprintf("read history of %s: %v", site, err))
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) buildHistory(r *http.Request, site string, q historyQuery, response *historyResponse) error {
	ctx := r.Context()
	channelInfo, err := s.Data.ChannelStatus(ctx, site)
	if err != nil {
		return err
	}
	requested := map[string]bool{}
	for _, channel := range q.channels {
		requested[channel] = true
	}
	units := map[string]string{}
	var anchor, earliest time.Time
	minSampling := 0.0
	for _, info := range channelInfo {
		units[info.Channel] = info.Unit
		if len(requested) > 0 && !requested[info.Channel] {
			continue
		}
		if info.To != nil && info.To.After(anchor) {
			anchor = info.To.UTC()
		}
		if info.From != nil && (earliest.IsZero() || info.From.Before(earliest)) {
			earliest = info.From.UTC()
		}
		if info.SamplingIntervalS > 0 && (minSampling == 0 || info.SamplingIntervalS < minSampling) {
			minSampling = info.SamplingIntervalS
		}
	}

	stored, err := s.Data.ListRuns(ctx, site, historyRunListLimit)
	if err != nil {
		return err
	}
	var windowed []windowedRun
	var runEnd, runStart time.Time
	for _, run := range stored {
		if run.Status != "succeeded" {
			continue
		}
		from, to, source, ok := cadsdb.RunWindow(run.Provenance, run.StartedAt, run.SimulatedSeconds)
		if !ok {
			continue
		}
		windowed = append(windowed, windowedRun{run: run, from: from, to: to, source: source})
		if to.After(runEnd) {
			runEnd = to
		}
		if runStart.IsZero() || from.Before(runStart) {
			runStart = from
		}
	}

	if anchor.IsZero() {
		anchor = runEnd
	}
	if anchor.IsZero() {
		return nil
	}
	to := anchor.Add(time.Second)
	from := anchor.Add(-q.spanDur)
	if q.spanDur == 0 {
		// "all": from the earliest stored measurement or run window.
		from = anchor
		for _, t := range []time.Time{earliest, runStart} {
			if !t.IsZero() && t.Before(from) {
				from = t
			}
		}
	}
	bin := chooseBin(from, to, minSampling, q.maxPoints)

	anchorText, fromText, toText := formatTime(anchor), formatTime(from), formatTime(to)
	fromMs, toMs := from.UnixMilli(), to.UnixMilli()
	response.Anchor, response.From, response.To = &anchorText, &fromText, &toText
	response.FromMs, response.ToMs, response.BinSeconds = &fromMs, &toMs, &bin

	if len(q.channels) > 0 {
		rows, err := s.Data.ReadSeriesBinned(ctx, site, q.channels, from, to, bin)
		if err != nil {
			return err
		}
		byChannel := map[string]*historyMeasurement{}
		for _, channel := range q.channels {
			response.Measurements = append(response.Measurements,
				historyMeasurement{Channel: channel, Unit: units[channel], Points: [][2]float64{}})
		}
		for i := range response.Measurements {
			byChannel[response.Measurements[i].Channel] = &response.Measurements[i]
		}
		for _, row := range rows {
			m := byChannel[row.Channel]
			if m == nil || math.IsNaN(row.Value) || math.IsInf(row.Value, 0) {
				continue
			}
			m.Points = append(m.Points, [2]float64{float64(row.Time.UnixMilli()), row.Value})
		}
	}

	var inRange []windowedRun
	for _, run := range windowed {
		if !run.from.After(to) && !run.to.Before(from) {
			inRange = append(inRange, run)
		}
	}
	inRange = latestPerWindow(inRange)
	sort.SliceStable(inRange, func(i, j int) bool {
		if !inRange[i].from.Equal(inRange[j].from) {
			return inRange[i].from.Before(inRange[j].from)
		}
		return compareTimePtr(inRange[i].run.StartedAt, inRange[j].run.StartedAt) < 0
	})
	if len(inRange) > historyMaxRuns {
		// Keep the most recent windows.
		inRange = inRange[len(inRange)-historyMaxRuns:]
		response.Truncated = true
	}
	if len(inRange) == 0 {
		return nil
	}

	runIDs := make([]string, len(inRange))
	byID := map[string]*historyRun{}
	response.Runs = make([]historyRun, len(inRange))
	for i, run := range inRange {
		runIDs[i] = run.run.RunID
		response.Runs[i] = newHistoryRun(run)
		byID[run.run.RunID] = &response.Runs[i]
	}

	outputs, err := s.Data.ReadRunOutputs(ctx, site, runIDs)
	if err != nil {
		return err
	}
	for _, out := range outputs {
		run := byID[out.RunID]
		if run == nil || out.Value == nil || math.IsNaN(*out.Value) || math.IsInf(*out.Value, 0) {
			continue
		}
		if run.Outputs[out.Step] == nil {
			run.Outputs[out.Step] = map[string]float64{}
		}
		run.Outputs[out.Step][out.Name] = *out.Value
	}

	if len(q.series) == 0 {
		return nil
	}
	wanted := map[[2]string]bool{}
	var steps, signals []string
	seenStep, seenSignal := map[string]bool{}, map[string]bool{}
	for _, pair := range q.series {
		wanted[pair] = true
		if !seenStep[pair[0]] {
			seenStep[pair[0]] = true
			steps = append(steps, pair[0])
		}
		if !seenSignal[pair[1]] {
			seenSignal[pair[1]] = true
			signals = append(signals, pair[1])
		}
	}
	series, err := s.Data.ReadRunSeries(ctx, site, runIDs, steps, signals)
	if err != nil {
		return err
	}
	for _, row := range series {
		run := byID[row.RunID]
		if run == nil || !wanted[[2]string{row.Step, row.Signal}] {
			continue
		}
		if run.Series[row.Step] == nil {
			run.Series[row.Step] = map[string]*historyRunTrace{}
		}
		trace := run.Series[row.Step][row.Signal]
		if trace == nil {
			trace = &historyRunTrace{T: []float64{}, V: []*float64{}}
			run.Series[row.Step][row.Signal] = trace
		}
		var value *float64
		if row.Value != nil && !math.IsNaN(*row.Value) && !math.IsInf(*row.Value, 0) {
			v := *row.Value
			value = &v
		}
		trace.T = append(trace.T, row.T)
		trace.V = append(trace.V, value)
	}
	return nil
}

func newHistoryRun(run windowedRun) historyRun {
	source := "argo"
	if strings.HasPrefix(run.run.RunID, "local-") {
		source = "local"
	}
	out := historyRun{
		RunID:        run.run.RunID,
		WorkflowPath: run.run.WorkflowPath,
		Source:       source,
		StartedAt:    formatTimePtr(run.run.StartedAt),
		FinishedAt:   formatTimePtr(run.run.FinishedAt),
		WindowFrom:   formatTime(run.from),
		WindowTo:     formatTime(run.to),
		WindowFromMs: run.from.UnixMilli(),
		WindowToMs:   run.to.UnixMilli(),
		AnchorSource: run.source,
		Outputs:      map[string]map[string]float64{},
		Series:       map[string]map[string]*historyRunTrace{},
	}
	if dw, ok := run.run.Provenance["data_window"].(map[string]any); ok {
		if v, ok := dw["sim_start_time"].(float64); ok && !math.IsNaN(v) && !math.IsInf(v, 0) {
			out.SimStartTime = v
		}
	}
	return out
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func formatTimePtr(t *time.Time) *string {
	if t == nil || t.IsZero() {
		return nil
	}
	text := formatTime(*t)
	return &text
}
