package metrics

import (
	"math"
	"sort"
	"time"

	"jobmonitor/internal/models"
)

// ServiceUptime summarises health of a monitored service.
type ServiceUptime struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	UptimePercent float64 `json:"uptime_percent"`
	TotalChecks   int     `json:"total_checks"`
	Passing       int     `json:"passing"`
	Failing       int     `json:"failing"`
	Missing       int     `json:"missing_slots"`
	LastState     string  `json:"last_state,omitempty"`
	LastUpdated   string  `json:"last_updated,omitempty"`
}

// ComputeServiceUptime aggregates uptime statistics per service from history entries.
// Entries should already be filtered to the desired time window [start, end].
// Missing samples (based on interval) are treated as failing checks to approximate downtime
// when the monitor or server was offline.
func ComputeServiceUptime(
	entries []models.StatusEntry,
	start time.Time,
	end time.Time,
	interval time.Duration,
	expectedTargets []models.Target,
) []ServiceUptime {
	if end.Before(start) {
		end = start
	}

	type acc struct {
		name      string
		passing   int
		failing   int
		lastState string
		lastTime  time.Time
	}

	summary := make(map[string]*acc)
	for _, target := range expectedTargets {
		summary[target.ID] = &acc{name: target.Name}
	}

	// ensure entries sorted? assume chronological.
	for _, entry := range entries {
		for _, check := range entry.Checks {
			target := summary[check.ID]
			if target == nil {
				target = &acc{name: check.Name}
				summary[check.ID] = target
			}
			if check.OK {
				target.passing++
			} else {
				target.failing++
			}
			if check.State != "" {
				target.lastState = check.State
				target.lastTime = entry.Timestamp
			}
		}
	}

	if len(summary) == 0 {
		return nil
	}

	expectedSlots := 0
	if interval > 0 {
		duration := end.Sub(start)
		if duration < 0 {
			duration = 0
		}
		expectedSlots = int(math.Ceil(float64(duration) / float64(interval)))
		// include final slot at end boundary
		if expectedSlots < len(entries) {
			expectedSlots = len(entries)
		}
	}

	missingSlots := 0
	if expectedSlots > len(entries) {
		missingSlots = expectedSlots - len(entries)
	}

	keys := make([]string, 0, len(summary))
	for id := range summary {
		keys = append(keys, id)
	}
	sort.Strings(keys)

	results := make([]ServiceUptime, 0, len(keys))
	for _, id := range keys {
		data := summary[id]
		data.failing += missingSlots

		total := data.passing + data.failing
		uptime := 0.0
		if total > 0 {
			uptime = float64(data.passing) / float64(total) * 100
		}

		result := ServiceUptime{
			ID:            id,
			Name:          data.name,
			UptimePercent: round2(uptime),
			TotalChecks:   total,
			Passing:       data.passing,
			Failing:       data.failing,
			Missing:       missingSlots,
			LastState:     data.lastState,
		}
		if !data.lastTime.IsZero() {
			result.LastUpdated = data.lastTime.UTC().Format(time.RFC3339)
		}
		results = append(results, result)
	}
	return results
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}
