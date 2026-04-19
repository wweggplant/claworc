package main

import (
	"fmt"
	"sort"
)

// ResultRow is a unified row for all scenario outputs.
type ResultRow struct {
	Scenario      string
	Count         int
	Errors        int
	TTFTP50       float64
	TTFTP95       float64
	P50           float64
	P95           float64
	P99           float64
	RecoveryMs    float64
	FailureRate   float64
	LatencyChange float64
	Error         string
}

// percentile returns the p-th percentile (0-100) from a slice of float64.
func percentile(data []float64, p float64) float64 {
	if len(data) == 0 {
		return 0
	}
	sorted := make([]float64, len(data))
	copy(sorted, data)
	sort.Float64s(sorted)

	idx := (p / 100) * float64(len(sorted)-1)
	low := int(idx)
	high := low + 1
	if high >= len(sorted) {
		return sorted[len(sorted)-1]
	}
	frac := idx - float64(low)
	return sorted[low]*(1-frac) + sorted[high]*frac
}

func printTable(rows []ResultRow) {
	fmt.Println()
	fmt.Println("┌────────────────────┬───────┬────────┬───────────┬───────────┬───────────┬───────────┐")
	fmt.Println("│ Scenario           │ Count │ Errors │ TTFT p50  │ TTFT p95  │ Total p50 │ Total p99 │")
	fmt.Println("├────────────────────┼───────┼────────┼───────────┼───────────┼───────────┼───────────┤")
	for _, r := range rows {
		if r.Error != "" {
			fmt.Printf("│ %-18s │ %s\n", r.Scenario, r.Error)
			continue
		}
		if r.Scenario == "restart-isolation" {
			fmt.Printf("│ %-18s │ recovery=%0.0fms  fail=%.1f%%  latency_change=%.1f%%\n",
				r.Scenario, r.RecoveryMs, r.FailureRate, r.LatencyChange)
			continue
		}
		fmt.Printf("│ %-18s │ %5d │ %6d │ %7.0fms │ %7.0fms │ %7.0fms │ %7.0fms │\n",
			r.Scenario, r.Count, r.Errors, r.TTFTP50, r.TTFTP95, r.P50, r.P99)
	}
	fmt.Println("└────────────────────┴───────┴────────┴───────────┴───────────┴───────────┴───────────┘")
}

func printScenarioRow(r ResultRow) {
	if r.Error != "" {
		fmt.Printf("    %-18s  ERROR: %s\n", r.Scenario, r.Error)
		return
	}
	if r.Scenario == "restart-isolation" {
		fmt.Printf("    %-18s  recovery=%0.0fms  failure_rate=%.1f%%  latency_change=%.1f%%\n",
			r.Scenario, r.RecoveryMs, r.FailureRate, r.LatencyChange)
		return
	}
	fmt.Printf("    %-18s  n=%d  err=%d  ttft_p50=%0.0fms  total_p50=%0.0fms  total_p99=%0.0fms\n",
		r.Scenario, r.Count, r.Errors, r.TTFTP50, r.P50, r.P99)
}
