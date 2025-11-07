package history

import (
	"sort"
	"strings"
	"time"

	"jobmonitor/internal/models"
)

const (
	// DefaultTimelinePoints controls how many dots we generate per service.
	DefaultTimelinePoints = 80
	maxDetailsPerPoint    = 4
)

var warningStates = map[string]struct{}{
	"activating":   {},
	"deactivating": {},
	"reloading":    {},
	"maintenance":  {},
}

type sample struct {
	Timestamp time.Time
	OK        bool
	State     string
	Error     string
}

// BuildServiceTimelines converts a history series into compact per-service timelines.
func BuildServiceTimelines(
	entries []models.StatusEntry,
	latest *models.StatusEntry,
	targets []models.Target,
	start, end time.Time,
	points int,
) []models.ServiceTimeline {
	if points <= 0 {
		points = DefaultTimelinePoints
	}
	if !end.After(start) {
		end = start.Add(time.Minute)
	}

	nameMap := make(map[string]string)

	registerName := func(id, name string) {
		if id == "" {
			return
		}
		if name == "" {
			name = id
		}
		if _, ok := nameMap[id]; !ok || nameMap[id] == "" {
			nameMap[id] = name
		}
	}

	for _, target := range targets {
		registerName(target.ID, target.Name)
	}

	historyMap := make(map[string][]sample)
	addSample := func(id, name string, s sample) {
		if id == "" {
			return
		}
		registerName(id, name)
		historyMap[id] = append(historyMap[id], s)
	}

	for _, entry := range entries {
		ts := entry.Timestamp
		for _, check := range entry.Checks {
			addSample(check.ID, check.Name, sample{
				Timestamp: ts,
				OK:        check.OK,
				State:     check.State,
				Error:     valueOrEmpty(check.Error),
			})
		}
	}
	if latest != nil {
		for _, check := range latest.Checks {
			registerName(check.ID, check.Name)
		}
	}

	if len(nameMap) == 0 {
		return nil
	}
	ids := make([]string, 0, len(nameMap))
	for id := range nameMap {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		return strings.ToLower(nameMap[ids[i]]) < strings.ToLower(nameMap[ids[j]])
	})

	result := make([]models.ServiceTimeline, 0, len(ids))
	for _, id := range ids {
		name := nameMap[id]
		timeline := buildTimeline(historyMap[id], start, end, points)
		result = append(result, models.ServiceTimeline{
			ServiceID:   id,
			ServiceName: name,
			Timeline:    timeline,
		})
	}
	return result
}

func buildTimeline(samples []sample, start, end time.Time, points int) []models.TimelinePoint {
	output := make([]models.TimelinePoint, 0, points)
	if points <= 0 {
		return output
	}
	if len(samples) > 1 {
		sort.Slice(samples, func(i, j int) bool {
			return samples[i].Timestamp.Before(samples[j].Timestamp)
		})
	}

	bucketDuration := end.Sub(start) / time.Duration(points)
	if bucketDuration <= 0 {
		bucketDuration = time.Minute
	}

	cursor := 0
	for i := 0; i < points; i++ {
		bucketStart := start.Add(time.Duration(i) * bucketDuration)
		bucketEnd := bucketStart.Add(bucketDuration)
		if i == points-1 {
			bucketEnd = end
		}
		bucketSamples, nextCursor := collectBucketSamples(samples, bucketStart, bucketEnd, cursor)
		cursor = nextCursor
		class, label, details := evaluateBucket(bucketSamples)
		output = append(output, models.TimelinePoint{
			ClassName: class,
			Label:     label,
			Start:     bucketStart,
			End:       bucketEnd,
			Details:   details,
		})
	}
	return output
}

func collectBucketSamples(samples []sample, start, end time.Time, cursor int) ([]sample, int) {
	total := len(samples)
	if total == 0 || cursor >= total {
		return nil, cursor
	}

	i := cursor
	for i < total && samples[i].Timestamp.Before(start) {
		i++
	}
	j := i
	for j < total && samples[j].Timestamp.Before(end) {
		j++
	}
	if i >= j {
		return nil, j
	}
	chunk := make([]sample, j-i)
	copy(chunk, samples[i:j])
	return chunk, j
}

func evaluateBucket(entries []sample) (className, label string, details []models.TimelineDetail) {
	if len(entries) == 0 {
		return "state-missing", "No data", nil
	}
	var (
		hasError   bool
		hasWarning bool
		hasSuccess bool
		hasMissing bool
	)

	details = make([]models.TimelineDetail, 0, maxDetailsPerPoint)
	for _, entry := range entries {
		state := strings.ToLower(entry.State)
		errorState := !entry.OK && (state == "inactive" || state == "failed" || state == "degraded" || (state == "" && entry.Error != ""))
		switch {
		case errorState:
			hasError = true
			details = appendDetail(details, entry)
		case entry.OK || state == "active" || state == "running":
			hasSuccess = true
		case state == "missing":
			hasMissing = true
		case isWarningState(state):
			hasWarning = true
			details = appendDetail(details, entry)
		case state == "" || state == "unknown":
			hasMissing = true
		default:
			if entry.OK {
				hasSuccess = true
			} else {
				hasError = true
				details = appendDetail(details, entry)
			}
		}
	}

	switch {
	case hasError:
		return "state-error", "Unavailable", details
	case hasMissing:
		return "state-missing", "No data", details
	case hasWarning:
		return "state-warning", "Transitioning", details
	case hasSuccess:
		return "state-success", "Operational", nil
	default:
		return "state-missing", "No data", details
	}
}

func appendDetail(details []models.TimelineDetail, entry sample) []models.TimelineDetail {
	if len(details) >= maxDetailsPerPoint {
		return details
	}
	return append(details, models.TimelineDetail{
		Timestamp: entry.Timestamp,
		State:     entry.State,
		Error:     entry.Error,
	})
}

func valueOrEmpty(ptr *string) string {
	if ptr == nil {
		return ""
	}
	return *ptr
}

func isWarningState(state string) bool {
	_, ok := warningStates[state]
	return ok
}

// BuildConnectivityTimeline reduces connectivity samples into compact timeline points.
func BuildConnectivityTimeline(entries []models.ConnectivityStatus, start, end time.Time, points int) []models.TimelinePoint {
	if points <= 0 {
		points = DefaultTimelinePoints
	}
	if !end.After(start) {
		end = start.Add(time.Minute)
	}

	samples := make([]models.ConnectivityStatus, 0, len(entries))
	for _, entry := range entries {
		if entry.CheckedAt.IsZero() {
			continue
		}
		samples = append(samples, entry)
	}
	sort.Slice(samples, func(i, j int) bool {
		return samples[i].CheckedAt.Before(samples[j].CheckedAt)
	})

	bucketDuration := end.Sub(start) / time.Duration(points)
	if bucketDuration <= 0 {
		bucketDuration = time.Minute
	}

	gapThreshold := deriveConnectivityGap(samples)

	result := make([]models.TimelinePoint, 0, points)
	idx := 0
	var last models.ConnectivityStatus
	var haveLast bool
	for idx < len(samples) && samples[idx].CheckedAt.Before(start) {
		last = samples[idx]
		haveLast = true
		idx++
	}

	for i := 0; i < points; i++ {
		bucketStart := start.Add(time.Duration(i) * bucketDuration)
		bucketEnd := bucketStart.Add(bucketDuration)
		if i == points-1 {
			bucketEnd = end
		}

		point := models.TimelinePoint{
			ClassName: "state-missing",
			Label:     "No data",
			Start:     bucketStart,
			End:       bucketEnd,
		}

		details := make([]models.TimelineDetail, 0, maxDetailsPerPoint)
		bucketSamples := make([]models.ConnectivityStatus, 0)
		for idx < len(samples) && !samples[idx].CheckedAt.After(bucketEnd) {
			current := samples[idx]
			last = current
			haveLast = true
			bucketSamples = append(bucketSamples, current)
			idx++
		}

		switch {
		case len(bucketSamples) > 0:
			selected := bucketSamples[len(bucketSamples)-1]
			point.ClassName, point.Label = connectivityClass(selected)
			for _, sample := range bucketSamples {
				if len(details) >= maxDetailsPerPoint {
					break
				}
				details = append(details, connectivityDetail(sample))
			}
		case haveLast && bucketStart.Sub(last.CheckedAt) <= gapThreshold:
			point.ClassName, point.Label = connectivityClass(last)
			detail := connectivityDetail(last)
			detail.Timestamp = bucketStart
			details = append(details, detail)
		default:
			point.ClassName = "state-missing"
			point.Label = "No data"
		}

		if len(details) > maxDetailsPerPoint {
			details = details[:maxDetailsPerPoint]
		}
		if len(details) > 0 && point.ClassName != "state-missing" {
			if len(details) == 0 {
				details = append(details, connectivityDetail(last))
			}
			point.Details = details
		} else if point.ClassName == "state-missing" {
			point.Details = nil
		}

		result = append(result, point)
	}

	return result
}

func deriveConnectivityGap(samples []models.ConnectivityStatus) time.Duration {
	const defaultGap = 5 * time.Minute
	if len(samples) < 2 {
		return defaultGap
	}
	diffs := make([]time.Duration, 0, len(samples)-1)
	prev := samples[0].CheckedAt
	for i := 1; i < len(samples); i++ {
		curr := samples[i].CheckedAt
		if curr.After(prev) {
			diffs = append(diffs, curr.Sub(prev))
		}
		prev = curr
	}
	if len(diffs) == 0 {
		return defaultGap
	}
	sort.Slice(diffs, func(i, j int) bool {
		return diffs[i] < diffs[j]
	})
	median := diffs[len(diffs)/2]
	if median <= 0 {
		return defaultGap
	}
	gap := median * 2
	if gap < time.Minute {
		return time.Minute
	}
	if gap > 2*time.Hour {
		return 2 * time.Hour
	}
	return gap
}

func connectivityDetail(status models.ConnectivityStatus) models.TimelineDetail {
	return models.TimelineDetail{
		Timestamp: status.CheckedAt,
		State:     connectivityState(status),
		Error:     status.Error,
	}
}

func connectivityState(status models.ConnectivityStatus) string {
	if status.OK {
		return "online"
	}
	if status.Error != "" {
		return "offline"
	}
	return "unknown"
}

func connectivityClass(status models.ConnectivityStatus) (className, label string) {
	switch {
	case status.OK:
		return "state-success", "Operational"
	case status.Error != "":
		return "state-error", "Unavailable"
	default:
		return "state-warning", "Unknown"
	}
}
