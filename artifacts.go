package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func atomicWrite(path string, data []byte) (err error) {
	if path == "" {
		return errors.New("artifact path must not be empty")
	}
	dir := filepath.Dir(path)
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".migration-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}

type Totals struct {
	Requested int `json:"requested"`
	Generated int `json:"generated"`
	Applied   int `json:"applied"`
	Ready     int `json:"ready"`
	Skipped   int `json:"skipped"`
	Manual    int `json:"manual"`
	Failed    int `json:"failed"`
	Empty     int `json:"empty"`
}

func (r *Report) summarize() {
	r.Totals = Totals{}
	for _, v := range r.Results {
		r.Totals.Requested++
		if v.Generated {
			r.Totals.Generated++
		}
		if v.Applied {
			r.Totals.Applied++
		}
		if v.Ready {
			r.Totals.Ready++
		}
		switch v.Outcome {
		case "skipped":
			r.Totals.Skipped++
		case "manual":
			r.Totals.Manual++
		case "failed":
			r.Totals.Failed++
		case "empty":
			r.Totals.Empty++
		}
	}
}
func (r *Runner) saveReports() error {
	var failures []error
	recordFailure := func(stage string, err error) {
		failures = append(failures, fmt.Errorf("%s: %w", stage, err))
		r.Report.Results = append(r.Report.Results, Result{Stage: stage, Outcome: "failed", Error: err.Error()})
		r.Report.summarize()
	}
	r.Report.summarize()
	if r.Options.LogPath != "" {
		data, err := json.MarshalIndent(r.Report, "", "  ")
		if err == nil {
			err = atomicWrite(r.Options.LogPath, data)
		}
		if err != nil {
			recordFailure("log", err)
		}
	}
	if r.Options.PDFPath != "" {
		if err := renderPDF(r.Options.PDFPath, r.Report); err != nil {
			recordFailure("PDF report", err)
		}
	}
	data, err := json.MarshalIndent(r.Report, "", "  ")
	if err == nil {
		err = atomicWrite(r.Options.ReportPath, append(data, '\n'))
	}
	if err != nil {
		recordFailure("JSON report", err)
		if r.Options.PDFPath != "" {
			if pdfErr := renderPDF(r.Options.PDFPath, r.Report); pdfErr != nil {
				failures = append(failures, pdfErr)
			}
		}
	}
	if _, err := fmt.Fprintf(r.Out, "Generated: %d; applied: %d; ready: %d; manual: %d; failed: %d; skipped: %d\n", r.Report.Totals.Generated, r.Report.Totals.Applied, r.Report.Totals.Ready, r.Report.Totals.Manual, r.Report.Totals.Failed, r.Report.Totals.Skipped); err != nil {
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}
