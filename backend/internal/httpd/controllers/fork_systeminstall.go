package controllers

import "github.com/aoagents/agent-orchestrator/backend/internal/service/systeminstall"

// Fork compatibility: the frozen harness UI polls only installing/verifying jobs.
func forkInstallJobView(job systeminstall.Job) systeminstall.Job {
	if job.Status == systeminstall.StatusWaitingForIdle {
		job.Status = systeminstall.StatusInstalling
	}
	return job
}
