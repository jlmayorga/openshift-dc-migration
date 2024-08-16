package main

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGeneratePDFReport(t *testing.T) {
	// Set up test data
	conversionInfos = []ConversionInfo{
		{
			Timestamp:            "2024-08-16T08:47:03-05:00",
			Namespace:            "test-namespace",
			DeploymentConfigName: "test-dc",
			HasTriggers:          true,
			HasLifecycleHooks:    false,
			HasAutoRollbacks:     false,
			UsesCustomStrategies: false,
		},
	}

	// Generate the report
	reportPath := "test_report.pdf"
	err := generatePDFReport(reportPath)

	// Assert no error occurred
	assert.NoError(t, err)

	// Check if the file was created
	_, err = os.Stat(reportPath)
	assert.NoError(t, err)

	// Clean up the test file
	os.Remove(reportPath)
}

func TestBoolToString(t *testing.T) {
	assert.Equal(t, "Yes", boolToString(true))
	assert.Equal(t, "No", boolToString(false))
}

func TestParseAndFormatDate(t *testing.T) {
	tests := []struct {
		name      string
		timestamp string
		expected  string
	}{
		{"Valid timestamp", "2024-08-16T08:47:03-05:00", "2024-08-16"},
		{"Invalid timestamp", "invalid", "Invalid Date"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseAndFormatDate(tt.timestamp)
			assert.Equal(t, tt.expected, result)
		})
	}
}
