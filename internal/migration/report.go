package migration

import (
	"bytes"
	"fmt"
	"github.com/jung-kurt/gofpdf"
	"strings"
)

func renderPDF(path string, report Report) error {
	pdf := gofpdf.New("P", "mm", "A4", "")
	pdf.SetCompression(false)
	pdf.SetMargins(15, 15, 15)
	pdf.SetAutoPageBreak(true, 18)
	safe := func(s string) string {
		var b strings.Builder
		for _, r := range s {
			if r >= 32 && r <= 126 {
				b.WriteRune(r)
			} else if r == '\n' {
				b.WriteRune(r)
			} else {
				b.WriteByte('?')
			}
		}
		return b.String()
	}
	pdf.SetHeaderFunc(func() {
		pdf.SetFont("Arial", "B", 13)
		pdf.Cell(0, 8, "DeploymentConfig migration report")
		pdf.Ln(9)
		pdf.SetFont("Arial", "", 8)
		pdf.MultiCell(180, 4, safe("Context: "+report.Context+" | "+report.Timestamp), "", "L", false)
		pdf.Ln(3)
	})
	pdf.SetFooterFunc(func() {
		pdf.SetY(-12)
		pdf.SetFont("Arial", "", 8)
		pdf.CellFormat(0, 5, fmt.Sprintf("Page %d", pdf.PageNo()), "", 0, "C", false, 0, "")
	})
	pdf.AddPage()
	pdf.SetFont("Arial", "", 10)
	pdf.MultiCell(180, 6, fmt.Sprintf("Generated %d | Applied %d | Ready %d | Failed %d | Manual %d | Skipped %d", report.Totals.Generated, report.Totals.Applied, report.Totals.Ready, report.Totals.Failed, report.Totals.Manual, report.Totals.Skipped), "", "L", false)
	pdf.Ln(4)
	for _, res := range report.Results {
		pdf.SetFont("Arial", "B", 10)
		height := float64(len(pdf.SplitLines([]byte(safe(res.Source.Namespace+"/"+res.Source.Name)), 180)))*5 + 10
		pdf.SetFont("Arial", "", 9)
		for _, line := range append(append(append([]string{}, res.Warnings...), res.Unsupported...), res.Error) {
			if line != "" {
				height += float64(len(pdf.SplitLines([]byte(safe(line)), 180))) * 4
			}
		}
		if height < 230 && pdf.GetY()+height > 279 {
			pdf.AddPage()
		}

		pdf.SetFont("Arial", "B", 10)
		pdf.MultiCell(180, 5, safe(res.Source.Namespace+"/"+res.Source.Name), "", "L", false)
		pdf.SetFont("Arial", "", 9)
		pdf.MultiCell(180, 5, safe("Stage: "+res.Stage+" | Outcome: "+res.Outcome), "", "L", false)
		for _, line := range append(append(append([]string{}, res.Warnings...), res.Unsupported...), res.Error) {
			if line != "" {
				pdf.MultiCell(180, 4, safe(line), "", "L", false)
			}
		}
		pdf.Ln(5)
	}
	var b bytes.Buffer
	if err := pdf.Output(&b); err != nil {
		return err
	}
	return atomicWrite(path, b.Bytes())
}
