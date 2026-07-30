package shipcmd

import (
	"strconv"
	"strings"

	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/output"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/ship"
)

// PlanResult is the stable, secret-free output of ship plan and ship run
// --dry-run.
type PlanResult struct {
	ship.Plan
	RequiresConfirmation bool `json:"requiresConfirmation"`
	MutationsPerformed   bool `json:"mutationsPerformed"`
}

func newPlanResult(plan ship.Plan) PlanResult {
	return PlanResult{
		Plan:                 plan,
		RequiresConfirmation: true,
		MutationsPerformed:   false,
	}
}

// OutputHeaders implements output.RowSource.
func (result PlanResult) OutputHeaders() []string {
	return []string{"PLAN ID", "CONTENT ID", "APP STATUS", "BINARY", "GMS", "STEPS"}
}

// OutputRows implements output.RowSource.
func (result PlanResult) OutputRows() [][]string {
	return [][]string{{
		result.ID,
		result.ContentID,
		result.AppStatus,
		result.Binary.Path,
		result.GMS,
		joinSteps(result.Steps),
	}}
}

// RunResult deliberately projects only non-secret checkpoint state. Upload
// session IDs and file keys never cross the command output boundary.
type RunResult struct {
	PlanID             string      `json:"planId"`
	ContentID          string      `json:"contentId"`
	AppStatus          string      `json:"appStatus"`
	Complete           bool        `json:"complete"`
	CompletedSteps     []ship.Step `json:"completedSteps"`
	MutationsPerformed bool        `json:"mutationsPerformed"`
}

func newRunResult(plan ship.Plan, result ship.Result) RunResult {
	return RunResult{
		PlanID:             plan.ID,
		ContentID:          plan.ContentID,
		AppStatus:          ship.Registration,
		Complete:           result.Complete,
		CompletedSteps:     append([]ship.Step(nil), result.Checkpoint.CompletedSteps...),
		MutationsPerformed: result.MutationsPerformed,
	}
}

// OutputHeaders implements output.RowSource.
func (result RunResult) OutputHeaders() []string {
	return []string{"PLAN ID", "CONTENT ID", "APP STATUS", "COMPLETE", "COMPLETED STEPS"}
}

// OutputRows implements output.RowSource.
func (result RunResult) OutputRows() [][]string {
	return [][]string{{
		result.PlanID,
		result.ContentID,
		result.AppStatus,
		strconv.FormatBool(result.Complete),
		joinSteps(result.CompletedSteps),
	}}
}

func joinSteps(steps []ship.Step) string {
	values := make([]string, len(steps))
	for index, step := range steps {
		values[index] = string(step)
	}
	return strings.Join(values, ",")
}

var (
	_ output.RowSource = PlanResult{}
	_ output.RowSource = RunResult{}
)
