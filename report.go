package main

import (
	"fmt"
	"time"

	"github.com/jung-kurt/gofpdf"
)

func generatePDFReport(reportPath string) error {
	pdf := gofpdf.New("L", "mm", "A4", "")
	pdf.AddPage()

	// Set up fonts
	pdf.SetFont("Arial", "B", 16)
	pdf.Cell(0, 10, "DeploymentConfig to Deployment Conversion Report")
	pdf.Ln(15)

	// Define column widths
	colWidths := []float64{25, 30, 50, 25, 35, 30, 35}
	pageWidth, _ := pdf.GetPageSize()
	tableWidth := 0.0
	for _, w := range colWidths {
		tableWidth += w
	}

	// Center the table
	leftMargin := (pageWidth - tableWidth) / 2
	pdf.SetLeftMargin(leftMargin)

	// Table headers
	pdf.SetFont("Arial", "B", 10)
	pdf.SetFillColor(200, 200, 200)
	headers := []string{"Date", "Namespace", "DeploymentConfig Name", "Triggers", "Lifecycle Hooks", "Auto Rollbacks", "Custom Strategies"}
	for i, header := range headers {
		pdf.CellFormat(colWidths[i], 7, header, "1", 0, "C", true, 0, "")
	}
	pdf.Ln(-1)

	// Table content
	pdf.SetFont("Arial", "", 9)
	pdf.SetFillColor(255, 255, 255)
	for i, info := range conversionInfos {
		fillColor := false
		if i%2 == 0 {
			fillColor = true
			pdf.SetFillColor(240, 240, 240)
		} else {
			pdf.SetFillColor(255, 255, 255)
		}

		date := parseAndFormatDate(info.Timestamp)
		pdf.CellFormat(colWidths[0], 6, date, "1", 0, "C", fillColor, 0, "")
		pdf.CellFormat(colWidths[1], 6, info.Namespace, "1", 0, "L", fillColor, 0, "")
		pdf.CellFormat(colWidths[2], 6, info.DeploymentConfigName, "1", 0, "L", fillColor, 0, "")
		pdf.CellFormat(colWidths[3], 6, boolToString(info.HasTriggers), "1", 0, "C", fillColor, 0, "")
		pdf.CellFormat(colWidths[4], 6, boolToString(info.HasLifecycleHooks), "1", 0, "C", fillColor, 0, "")
		pdf.CellFormat(colWidths[5], 6, boolToString(info.HasAutoRollbacks), "1", 0, "C", fillColor, 0, "")
		pdf.CellFormat(colWidths[6], 6, boolToString(info.UsesCustomStrategies), "1", 0, "C", fillColor, 0, "")
		pdf.Ln(-1)
	}

	// Add summary
	pdf.Ln(10)
	pdf.SetFont("Arial", "B", 12)
	pdf.CellFormat(0, 10, fmt.Sprintf("Total Conversions: %d", len(conversionInfos)), "", 0, "L", false, 0, "")

	return pdf.OutputFileAndClose(reportPath)
}

func boolToString(b bool) string {
	if b {
		return "Yes"
	}
	return "No"
}

func parseAndFormatDate(timestamp string) string {
	t, err := time.Parse(time.RFC3339, timestamp)
	if err != nil {
		return "Invalid Date"
	}
	return t.Format("2006-01-02")
}
