package migration

import (
	"os"
	"strings"
	"testing"
)

func TestPDFLongNamesAndFailures(t *testing.T) {
	report := Report{SchemaVersion: "v1", Timestamp: "2026-10-15T00:00:00Z", Context: strings.Repeat("context-", 30)}
	for i := 0; i < 100; i++ {
		report.Results = append(report.Results, Result{Source: Identity{Namespace: strings.Repeat("n", 63), Name: strings.Repeat("d", 253)}, Stage: "admission", Outcome: "failed", Error: strings.Repeat("Admission rejected for quota. ", 10)})
	}
	report.summarize()
	path := t.TempDir() + "/report.pdf"
	if dir := os.Getenv("PDF_QA_DIR"); dir != "" {
		path = dir + "/report.pdf"
	}
	if err := renderPDF(path, report); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "Admission rejected") || !strings.Contains(string(b), "Page 2") || !strings.Contains(string(b), "Failed 100") {
		t.Fatal("report missing content, repeated headers or totals")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
}
