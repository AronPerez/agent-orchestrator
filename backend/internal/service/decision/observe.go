package decision

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// ObserveRow counts one decision kind's calls raised in observe mode.
type ObserveRow struct {
	Kind     domain.DecisionKindID
	Label    string
	Reserved bool
	// Calls counts every call of this kind that was not withdrawn.
	Calls int
	// Answered counts calls with both a prediction and the person's answer.
	Answered int
	Agreed   int
	Differed int
}

// ObserveReport is how often a project's sessions predicted the person's
// answer, per decision kind, across calls raised in observe mode.
type ObserveReport struct {
	ProjectID domain.ProjectID
	Mode      domain.AutonomyMode
	Rows      []ObserveRow
}

// observeStates are the states whose calls the readout counts. Withdrawn
// calls never reached the person, so they say nothing about agreement.
var observeStates = []domain.DecisionCallState{
	domain.DecisionStateNeedsYou, domain.DecisionStateInProgress,
	domain.DecisionStateDone, domain.DecisionStateDismissed,
}

// Observe reports, per decision kind, how often the person's answers agreed
// with what sessions said they would have decided. Only calls raised while
// the project was in observe mode count; rows follow the kind vocabulary.
func (s *Service) Observe(ctx context.Context, actor Actor, projectID domain.ProjectID) (ObserveReport, error) {
	projectID, autonomy, err := s.projectAutonomy(ctx, actor, projectID)
	if err != nil {
		return ObserveReport{}, err
	}
	report := ObserveReport{ProjectID: projectID, Mode: autonomy.EffectiveMode()}
	rows := make(map[domain.DecisionKindID]*ObserveRow)
	for _, state := range observeStates {
		calls, err := s.store.ListDecisionCallsByState(ctx, state, -1)
		if err != nil {
			return ObserveReport{}, err
		}
		for _, call := range calls {
			raised := call.Raised
			if projectID == "" || raised.ProjectID != projectID || raised.AutonomyMode != domain.AutonomyObserve || raised.Kind == "" {
				continue
			}
			row := rows[raised.Kind]
			if row == nil {
				row = &ObserveRow{Kind: raised.Kind, Reserved: raised.Reserved}
				rows[raised.Kind] = row
			}
			// A kind's reserved flag can change between calls; a row is
			// reserved if any of its calls was raised as reserved.
			row.Reserved = row.Reserved || raised.Reserved
			row.Calls++
			agreed, known := call.PredictionAgreed()
			if !known {
				continue
			}
			row.Answered++
			if agreed {
				row.Agreed++
			} else {
				row.Differed++
			}
		}
	}
	for _, kind := range domain.DecisionKinds() {
		if row := rows[kind.ID]; row != nil {
			row.Label = kind.Label
			report.Rows = append(report.Rows, *row)
		}
	}
	return report, nil
}
