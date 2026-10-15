package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOfflineRunnerIsIsolated(t *testing.T) {
	for i := 0; i < 4; i++ {
		t.Run(string(rune('a'+i)), func(t *testing.T) {
			t.Parallel()
			o := DefaultOptions()
			o.Inputs = []string{"testdata/ocp-4.14-dc.yaml"}
			o.OutputDir = t.TempDir()
			o.ReportPath = filepath.Join(o.OutputDir, "report.json")
			o.PDFPath = ""
			o.PDFPath = ""
			r := NewRunner(o)
			r.Now = func() time.Time { return time.Unix(0, 0) }
			if err := r.Run(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(r.Report.Results) != 1 || !r.Report.Results[0].Generated {
				t.Fatalf("report: %+v", r.Report)
			}
			if _, err := os.Stat(filepath.Join(o.OutputDir, "example", "web.yaml")); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestOfflineMalformedAndCanceled(t *testing.T) {
	o := DefaultOptions()
	o.Inputs = []string{"missing"}
	o.OutputDir = t.TempDir()
	o.ReportPath = filepath.Join(o.OutputDir, "report.json")
	o.PDFPath = ""
	r := NewRunner(o)
	if r.Run(context.Background()) == nil || len(r.Report.Results) != 1 {
		t.Fatal("input failure swallowed")
	}
	o.Inputs = []string{"testdata/ocp-4.14-dc.yaml"}
	r = NewRunner(o)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if r.Run(ctx) == nil || r.Report.Results[0].Generated {
		t.Fatal("cancellation swallowed")
	}
}
func TestIndependentCLIOptions(t *testing.T) {
	a, b := NewCommand(), NewCommand()
	if err := a.Flags().Set("projects", "one"); err != nil {
		t.Fatal(err)
	}
	v, _ := b.Flags().GetStringSlice("projects")
	if len(v) != 0 {
		t.Fatal(v)
	}
}
