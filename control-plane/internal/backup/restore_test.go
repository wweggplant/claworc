package backup

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/config"
	"github.com/gluk-w/claworc/control-plane/internal/database"
)

// mockOrchRestore embeds mockOrch and overrides ExecInInstance to track calls.
type mockOrchRestore struct {
	mockOrch
	execCalls   []string
	execResults map[string]execResult
}

type execResult struct {
	stderr   string
	exitCode int
	err      error
}

func (m *mockOrchRestore) addExecResult(cmdFragment string, stderr string, exitCode int, err error) {
	if m.execResults == nil {
		m.execResults = make(map[string]execResult)
	}
	m.execResults[cmdFragment] = execResult{stderr: stderr, exitCode: exitCode, err: err}
}

func (m *mockOrchRestore) ExecInInstance(_ context.Context, _ string, cmd []string) (string, string, int, error) {
	full := strings.Join(cmd, " ")
	m.execCalls = append(m.execCalls, full)
	for frag, res := range m.execResults {
		if strings.Contains(full, frag) {
			return "", res.stderr, res.exitCode, res.err
		}
	}
	return "", "", 0, nil
}

// createTestBackup creates a completed backup with a small file.
func createTestBackup(t *testing.T, instName string) *database.Backup {
	t.Helper()
	dir := t.TempDir()
	config.Cfg.DataPath = dir

	b := &database.Backup{
		InstanceID:   1,
		InstanceName: instName,
		Status:       "running",
		FilePath:     "partial.tar.gz",
		Paths:        `["HOME"]`,
	}
	database.DB.Create(b)

	instDir := dir + "/backups/" + instName
	os.MkdirAll(instDir, 0755)

	content := "test backup content for restore"
	os.WriteFile(instDir+"/partial.tar.gz", []byte(content), 0644)

	now := time.Now().UTC()
	b.Status = "completed"
	b.FilePath = instName + "/partial.tar.gz"
	b.SizeBytes = int64(len(content))
	b.CompletedAt = &now
	database.DB.Save(b)

	return b
}

func TestRestoreBackup_Success(t *testing.T) {
	setupTestDB(t)
	b := createTestBackup(t, "test-inst")

	orch := &mockOrchRestore{}

	err := RestoreBackup(context.Background(), orch, "test-inst", b.ID)
	if err != nil {
		t.Fatalf("RestoreBackup failed: %v", err)
	}

	// Should have: 1 cleanup + N chunk writes + 1 extract
	if len(orch.execCalls) < 3 {
		t.Errorf("expected at least 3 exec calls, got %d: %v", len(orch.execCalls), orch.execCalls)
	}

	if !strings.Contains(orch.execCalls[0], "rm -f") {
		t.Errorf("first call should be cleanup, got: %s", orch.execCalls[0])
	}

	last := orch.execCalls[len(orch.execCalls)-1]
	if !strings.Contains(last, "tar xzf") {
		t.Errorf("last call should be tar extract, got: %s", last)
	}
}

func TestRestoreBackup_ChunksAreBase64Encoded(t *testing.T) {
	setupTestDB(t)
	b := createTestBackup(t, "test-inst")

	orch := &mockOrchRestore{}

	err := RestoreBackup(context.Background(), orch, "test-inst", b.ID)
	if err != nil {
		t.Fatalf("RestoreBackup failed: %v", err)
	}

	for _, call := range orch.execCalls {
		if !strings.Contains(call, "base64 -d") {
			continue
		}
		start := strings.Index(call, "echo '")
		end := strings.Index(call, "' | base64")
		if start == -1 || end == -1 {
			t.Errorf("malformed base64 command: %s", call)
			continue
		}
		payload := call[start+6 : end]
		decoded, err := base64.StdEncoding.DecodeString(payload)
		if err != nil {
			t.Errorf("base64 payload should be valid: %v", err)
		}
		if string(decoded) != "test backup content for restore" {
			t.Errorf("decoded chunk doesn't match archive content, got: %q", string(decoded))
		}
		break
	}
}

func TestRestoreBackup_NotCompletedStatus(t *testing.T) {
	setupTestDB(t)
	dir := t.TempDir()
	config.Cfg.DataPath = dir

	b := &database.Backup{
		InstanceID:   1,
		InstanceName: "test-inst",
		Status:       "running",
	}
	database.DB.Create(b)

	orch := &mockOrchRestore{}
	err := RestoreBackup(context.Background(), orch, "test-inst", b.ID)
	if err == nil {
		t.Fatal("expected error for non-completed backup")
	}
	if !strings.Contains(err.Error(), "expected completed") {
		t.Errorf("wrong error: %v", err)
	}
}

func TestRestoreBackup_MissingFile(t *testing.T) {
	setupTestDB(t)
	dir := t.TempDir()
	config.Cfg.DataPath = dir

	now := time.Now().UTC()
	b := &database.Backup{
		InstanceID:   1,
		InstanceName: "test-inst",
		Status:       "completed",
		FilePath:     "nonexistent/backup.tar.gz",
		CompletedAt:  &now,
	}
	database.DB.Create(b)

	orch := &mockOrchRestore{}
	err := RestoreBackup(context.Background(), orch, "test-inst", b.ID)
	if err == nil {
		t.Fatal("expected error for missing backup file")
	}
	if !strings.Contains(err.Error(), "backup file missing") {
		t.Errorf("wrong error: %v", err)
	}
}

func TestRestoreBackup_ChunkWriteFailure(t *testing.T) {
	setupTestDB(t)
	b := createTestBackup(t, "test-inst")

	orch := &mockOrchRestore{}
	orch.addExecResult("base64 -d", "disk full", 1, nil)

	err := RestoreBackup(context.Background(), orch, "test-inst", b.ID)
	if err == nil {
		t.Fatal("expected error on chunk write failure")
	}
	if !strings.Contains(err.Error(), "write chunk failed") {
		t.Errorf("wrong error: %v", err)
	}
}

func TestRestoreBackup_ExtractFailure(t *testing.T) {
	setupTestDB(t)
	b := createTestBackup(t, "test-inst")

	orch := &mockOrchRestore{}
	orch.addExecResult("tar xzf", "corrupt archive", 1, nil)

	err := RestoreBackup(context.Background(), orch, "test-inst", b.ID)
	if err == nil {
		t.Fatal("expected error on extract failure")
	}
	if !strings.Contains(err.Error(), "extract failed") {
		t.Errorf("wrong error: %v", err)
	}
}

func TestRestoreBackup_ExtractCleanupOnError(t *testing.T) {
	setupTestDB(t)
	b := createTestBackup(t, "test-inst")

	orch := &mockOrchRestore{}
	orch.addExecResult("tar xzf", "corrupt archive", 1, nil)

	_ = RestoreBackup(context.Background(), orch, "test-inst", b.ID)

	last := orch.execCalls[len(orch.execCalls)-1]
	if !strings.Contains(last, "rm -f") {
		t.Errorf("extract command should clean up temp file on failure, got: %s", last)
	}
	if !strings.Contains(last, "exit 1") {
		t.Errorf("extract command should exit 1 on failure, got: %s", last)
	}
}

func TestRestoreBackup_LargeArchive_MultipleChunks(t *testing.T) {
	setupTestDB(t)
	dir := t.TempDir()
	config.Cfg.DataPath = dir

	instDir := dir + "/backups/large-inst"
	os.MkdirAll(instDir, 0755)

	content := make([]byte, 100*1024)
	for i := range content {
		content[i] = byte(i % 256)
	}
	os.WriteFile(instDir+"/large.tar.gz", content, 0644)

	now := time.Now().UTC()
	b := &database.Backup{
		InstanceID:   1,
		InstanceName: "large-inst",
		Status:       "completed",
		FilePath:     "large-inst/large.tar.gz",
		SizeBytes:    int64(len(content)),
		CompletedAt:  &now,
	}
	database.DB.Create(b)

	orch := &mockOrchRestore{}
	err := RestoreBackup(context.Background(), orch, "large-inst", b.ID)
	if err != nil {
		t.Fatalf("RestoreBackup failed: %v", err)
	}

	chunkCount := 0
	for _, call := range orch.execCalls {
		if strings.Contains(call, "base64 -d") {
			chunkCount++
		}
	}
	if chunkCount < 2 {
		t.Errorf("expected multiple chunks for 100KB file, got %d (should be >= 2)", chunkCount)
	}

	fmt.Printf("100KB archive: %d total exec calls (%d chunks)\n", len(orch.execCalls), chunkCount)
}
