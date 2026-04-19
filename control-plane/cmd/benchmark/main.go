package main

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"os"
	"strings"
	"sync"
	"time"
)

var (
	server       = flag.String("server", "http://localhost:8000", "Server base URL")
	authDisabled = flag.Bool("auth-disabled", true, "Server runs with CLAWORC_AUTH_DISABLED=true")
	username     = flag.String("username", "", "Login username (requires -password)")
	password     = flag.String("password", "", "Login password (requires -username)")
	outputFile   = flag.String("output", "", "CSV output file path")
	concurrency  = flag.Int("concurrency", 5, "Max concurrency for tests")
	instances    = flag.Int("instances", 0, "Number of instances to use (0 = all running)")
	prompt       = flag.String("prompt", "respond with exactly one word: ok", "Chat prompt to send")
	timeout      = flag.Duration("timeout", 120*time.Second, "Per-request timeout")
	skipRestart  = flag.Bool("skip-restart", false, "Skip restart isolation scenario")
)

func main() {
	flag.Parse()
	log.SetFlags(log.Ltime | log.Lmicroseconds)

	client, err := newHTTPClient()
	if err != nil {
		log.Fatalf("Auth failed: %v", err)
	}

	instanceIDs, err := fetchRunningInstances(client)
	if err != nil {
		log.Fatalf("Failed to fetch instances: %v", err)
	}
	if len(instanceIDs) == 0 {
		log.Fatal("No running instances found")
	}
	if *instances > 0 && *instances < len(instanceIDs) {
		instanceIDs = instanceIDs[:*instances]
	}
	log.Printf("Using %d instances: %v", len(instanceIDs), instanceIDs)

	var allRows []ResultRow

	log.Println("\n=== Scenario 1: Concurrent Chat ===")
	allRows = append(allRows, runConcurrentChat(client, instanceIDs)...)

	log.Println("\n=== Scenario 3: API Latency ===")
	allRows = append(allRows, runAPILatency(client)...)

	if !*skipRestart && len(instanceIDs) >= 2 {
		log.Println("\n=== Scenario 2: Restart Isolation ===")
		allRows = append(allRows, runRestartIsolation(client, instanceIDs))
	}

	fmt.Println()
	printTable(allRows)

	if *outputFile != "" {
		writeCSV(allRows, *outputFile)
		log.Printf("Results written to %s", *outputFile)
	}
}

func newHTTPClient() (*http.Client, error) {
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 30 * time.Second}

	if *authDisabled {
		return client, nil
	}
	if *username == "" || *password == "" {
		return nil, fmt.Errorf("-username and -password required when not using -auth-disabled")
	}

	body, _ := json.Marshal(map[string]string{"username": *username, "password": *password})
	resp, err := client.Post(*server+"/api/v1/auth/login", "application/json", strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("login request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("login failed (%d): %s", resp.StatusCode, string(b))
	}
	log.Println("Authenticated as", *username)
	return client, nil
}

func fetchRunningInstances(client *http.Client) ([]int, error) {
	resp, err := client.Get(*server + "/api/v1/instances")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var instances []struct {
		ID     int    `json:"id"`
		Status string `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&instances); err != nil {
		return nil, err
	}

	var ids []int
	for _, inst := range instances {
		if inst.Status == "running" {
			ids = append(ids, inst.ID)
		}
	}
	return ids, nil
}

// --- Scenario 1: Concurrent Chat ---

func runConcurrentChat(client *http.Client, instanceIDs []int) []ResultRow {
	var rows []ResultRow

	// Single-instance baseline: 1, 3, 5 concurrency on the first instance
	singleID := instanceIDs[0]
	for _, conc := range []int{1, 3, 5} {
		if conc > *concurrency {
			break
		}
		label := fmt.Sprintf("single-%d", conc)
		log.Printf("  Running %s (instance %d, %d concurrent)...", label, singleID, conc)
		results := runChatOnInstance(client, singleID, conc, *prompt, *timeout)
		rows = append(rows, resultToRow(label, results))
		printScenarioRow(rows[len(rows)-1])
	}

	// Multi-instance parallel: each instance handles 1 concurrent request
	if len(instanceIDs) >= 3 {
		for _, n := range []int{3, 5} {
			if n > len(instanceIDs) || n > *concurrency {
				continue
			}
			ids := instanceIDs[:n]
			label := fmt.Sprintf("multi-%d", n)
			log.Printf("  Running %s (%d instances, 1 each)...", label, n)

			var allResults []ChatResult
			var mu sync.Mutex
			var wg sync.WaitGroup

			for _, id := range ids {
				wg.Add(1)
				go func(instID int) {
					defer wg.Done()
					res := runChatOnInstance(client, instID, 1, *prompt, *timeout)
					mu.Lock()
					allResults = append(allResults, res...)
					mu.Unlock()
				}(id)
			}
			wg.Wait()

			rows = append(rows, resultToRow(label, allResults))
			printScenarioRow(rows[len(rows)-1])
		}
	}

	return rows
}

func runChatOnInstance(client *http.Client, instanceID, conc int, msg string, timeout time.Duration) []ChatResult {
	var results []ChatResult
	var mu sync.Mutex
	var wg sync.WaitGroup

	for i := 0; i < conc; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := runSingleChat(client, instanceID, msg, timeout)
			mu.Lock()
			results = append(results, r)
			mu.Unlock()
		}()
	}
	wg.Wait()
	return results
}

// --- Scenario 2: Restart Isolation ---

func runRestartIsolation(client *http.Client, instanceIDs []int) ResultRow {
	targetID := instanceIDs[0]
	otherIDs := instanceIDs[1:]

	// Baseline: measure chat latency on other instances
	log.Printf("  Measuring baseline chat latency on other instances...")
	baselineResults := make([]ChatResult, 0, len(otherIDs))
	for _, id := range otherIDs {
		r := runSingleChat(client, id, *prompt, *timeout)
		if r.Error == nil {
			baselineResults = append(baselineResults, r)
		}
	}
	baselineP50 := percentile(chatDurations(baselineResults), 50)

	// Restart target instance
	log.Printf("  Restarting instance %d...", targetID)
	restartStart := time.Now()
	resp, err := client.Post(*server+fmt.Sprintf("/api/v1/instances/%d/restart", targetID), "", nil)
	if err != nil {
		return ResultRow{Scenario: "restart-isolation", Error: fmt.Sprintf("restart request: %v", err)}
	}
	resp.Body.Close()

	// Poll until running, sending chat to other instances every 5s
	var otherErrors int
	var otherLatencies []float64
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			<-ticker.C
			for _, id := range otherIDs {
				r := runSingleChat(client, id, *prompt, *timeout)
				if r.Error != nil {
					otherErrors++
				} else {
					otherLatencies = append(otherLatencies, r.TotalMs)
				}
			}
			// Check if target is back
			if isInstanceRunning(client, targetID) {
				// Verify chat is actually working
				verify := runSingleChat(client, targetID, *prompt, *timeout)
				if verify.Error == nil {
					return
				}
			}
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Minute):
		return ResultRow{Scenario: "restart-isolation", Error: "timeout waiting for instance recovery"}
	}

	recoveryMs := time.Since(restartStart).Seconds() * 1000
	failureRate := float64(0)
	if len(otherLatencies)+otherErrors > 0 {
		failureRate = float64(otherErrors) / float64(len(otherLatencies)+otherErrors) * 100
	}

	latencyChange := float64(0)
	if baselineP50 > 0 && len(otherLatencies) > 0 {
		duringP50 := percentile(otherLatencies, 50)
		latencyChange = (duringP50 - baselineP50) / baselineP50 * 100
	}

	return ResultRow{
		Scenario:       "restart-isolation",
		RecoveryMs:     recoveryMs,
		FailureRate:    failureRate,
		LatencyChange:  latencyChange,
	}
}

func isInstanceRunning(client *http.Client, id int) bool {
	resp, err := client.Get(*server + fmt.Sprintf("/api/v1/instances/%d", id))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	var inst struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&inst); err != nil {
		return false
	}
	return inst.Status == "running"
}

// --- Scenario 3: API Latency ---

func runAPILatency(client *http.Client) []ResultRow {
	endpoints := []struct {
		name string
		path string
	}{
		{"api-instances", "/api/v1/instances"},
		{"health", "/health"},
	}

	var rows []ResultRow
	for _, ep := range endpoints {
		log.Printf("  Benchmarking %s...", ep.name)
		var latencies []float64
		var mu sync.Mutex
		var wg sync.WaitGroup

		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				start := time.Now()
				resp, err := client.Get(*server + ep.path)
				elapsed := time.Since(start).Seconds() * 1000
				if err == nil {
					io.ReadAll(resp.Body)
					resp.Body.Close()
				}
				mu.Lock()
				latencies = append(latencies, elapsed)
				mu.Unlock()
			}()
		}
		wg.Wait()

		rows = append(rows, ResultRow{
			Scenario: ep.name,
			P50:      percentile(latencies, 50),
			P95:      percentile(latencies, 95),
			P99:      percentile(latencies, 99),
		})
		printScenarioRow(rows[len(rows)-1])
	}
	return rows
}

func chatDurations(results []ChatResult) []float64 {
	var d []float64
	for _, r := range results {
		if r.Error == nil {
			d = append(d, r.TotalMs)
		}
	}
	return d
}

func chatTTFTs(results []ChatResult) []float64 {
	var d []float64
	for _, r := range results {
		if r.Error == nil && r.TTFT > 0 {
			d = append(d, r.TTFT)
		}
	}
	return d
}

func resultToRow(label string, results []ChatResult) ResultRow {
	durations := chatDurations(results)
	ttfts := chatTTFTs(results)
	row := ResultRow{
		Scenario: label,
		Count:    len(durations),
		Errors:   len(results) - len(durations),
	}
	if len(ttfts) > 0 {
		row.TTFTP50 = percentile(ttfts, 50)
		row.TTFTP95 = percentile(ttfts, 95)
	}
	if len(durations) > 0 {
		row.P50 = percentile(durations, 50)
		row.P95 = percentile(durations, 95)
		row.P99 = percentile(durations, 99)
	}
	return row
}

func writeCSV(rows []ResultRow, path string) {
	f, err := os.Create(path)
	if err != nil {
		log.Fatalf("Cannot create CSV: %v", err)
	}
	defer f.Close()
	w := csv.NewWriter(f)
	defer w.Flush()
	w.Write([]string{"scenario", "count", "errors", "ttft_p50_ms", "ttft_p95_ms", "total_p50_ms", "total_p95_ms", "total_p99_ms", "recovery_ms", "failure_rate_pct", "latency_change_pct"})
	for _, r := range rows {
		w.Write([]string{
			r.Scenario,
			fmt.Sprintf("%d", r.Count),
			fmt.Sprintf("%d", r.Errors),
			fmt.Sprintf("%.1f", r.TTFTP50),
			fmt.Sprintf("%.1f", r.TTFTP95),
			fmt.Sprintf("%.1f", r.P50),
			fmt.Sprintf("%.1f", r.P95),
			fmt.Sprintf("%.1f", r.P99),
			fmt.Sprintf("%.0f", r.RecoveryMs),
			fmt.Sprintf("%.1f", r.FailureRate),
			fmt.Sprintf("%.1f", r.LatencyChange),
		})
	}
}
