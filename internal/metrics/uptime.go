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
	LastState     string  `json:"last_state,omitempty"`
	LastUpdated   string  `json:"last_updated,omitempty"`
}

// ComputeServiceUptime aggregates uptime statistics per service from history entries.
func ComputeServiceUptime(entries []models.StatusEntry) []ServiceUptime {
	type acc struct {
		name      string
		passing   int
		failing   int
		lastState string
		lastTime  time.Time
	}
	state := make(map[string]*acc)
	for _, entry := range entries {
		for _, check := range entry.Checks {
			target := state[check.ID]
			if target == nil {
				target = &acc{name: check.Name}
				state[check.ID] = target
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
	if len(state) == 0 {
		return nil
	}

	keys := make([]string, 0, len(state))
	for k := range state {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	results := make([]ServiceUptime, 0, len(keys))
	for _, id := range keys {
		data := state[id]
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
