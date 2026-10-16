package migration

import (
	"context"
	"fmt"
	"github.com/spf13/cobra"
)

// BuildInfo identifies the executable that constructed this command.
type BuildInfo struct {
	Version   string
	Commit    string
	BuildDate string
}

func NewCommand(info BuildInfo) *cobra.Command {
	o := DefaultOptions()
	cmd := &cobra.Command{Use: "openshift-dc-converter", Args: cobra.NoArgs, Short: "Plan migration of DeploymentConfigs to Deployments", Version: fmt.Sprintf("%s (%s, %s)", info.Version, info.Commit, info.BuildDate), SilenceUsage: true}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		if o.ApplyChanges {
			o.Mode = "stage"
		}
		if err := o.Validate(); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), o.OperationTimeout)
		defer cancel()
		r := NewRunner(o)
		r.Out = cmd.OutOrStdout()
		return r.Run(ctx)
	}
	f := cmd.Flags()
	f.StringVar(&o.Mode, "mode", o.Mode, "generate, plan, dry-run, stage, preflight, cutover or rollback")
	f.BoolVar(&o.AutomationQuiesced, "automation-quiesced", false, "Acknowledge external reconcilers/scalers paused before cutover")
	f.StringVar(&o.PlanFile, "plan-file", "", "Reviewed live migration plan")
	f.StringVar(&o.CutoverMode, "cutover-mode", o.CutoverMode, "exclusive or overlap (recorded in plan)")
	f.StringVar(&o.OverlapJustification, "overlap-justification", "", "Operator reason the workload permits overlap")
	f.StringVar(&o.HealthURL, "health-url", "", "Operator-specified application health URL for cutover")
	f.DurationVar(&o.ReadinessTimeout, "readiness-timeout", o.ReadinessTimeout, "Readiness and retirement timeout")
	f.StringVar(&o.Kubeconfig, "kubeconfig", "", "Explicit kubeconfig; otherwise standard loading rules")
	f.StringVar(&o.Context, "context", "", "Kubeconfig context")
	f.StringSliceVar(&o.Projects, "projects", nil, "Namespaces to convert")
	f.StringSliceVar(&o.Inputs, "input", nil, "Offline exported DeploymentConfig YAML/JSON files")
	f.StringVar(&o.OutputDir, "output-dir", o.OutputDir, "Manifest and plan directory")
	f.StringVar(&o.ReportPath, "json-report", o.ReportPath, "Canonical JSON report")
	f.StringVar(&o.PDFPath, "report-path", o.PDFPath, "Optional PDF report (empty disables)")
	f.StringVar(&o.LogPath, "log-file", "", "Optional log file")
	f.BoolVar(&o.PreserveLabels, "preserve-labels", true, "Preserve workload metadata labels; pod labels always retained")
	f.BoolVar(&o.PreserveAnnotations, "preserve-annotations", true, "Preserve workload metadata annotations")
	f.StringSliceVar(&o.Reserved, "reserved-namespaces", o.Reserved, "Reserved namespaces to skip")
	f.DurationVar(&o.RequestTimeout, "request-timeout", o.RequestTimeout, "Per-request deadline")
	f.DurationVar(&o.OperationTimeout, "operation-timeout", o.OperationTimeout, "Overall operation deadline")
	f.BoolVar(&o.ApplyChanges, "apply-changes", false, "Stage zero-replica destination after validation")
	return cmd
}
