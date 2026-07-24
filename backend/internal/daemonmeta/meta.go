package daemonmeta

import "runtime/debug"

// ServiceName identifies the AO daemon in loopback health/readiness probes.
// The CLI uses it with the reported PID to avoid signaling an unrelated process
// when a stale run-file's PID has been reused.
const ServiceName = "agent-orchestrator-daemon"

// BuildIdentity returns a stable identifier for the daemon build: the VCS
// revision Go embeds at build time (from the enclosing git checkout), suffixed
// "-dirty" when the working tree had uncommitted changes. It is empty when the
// build carries no VCS stamp (e.g. `go run`, or `-buildvcs=false`), so callers
// must treat "" as "unknown" and fall back to another identity signal (the
// executable path) rather than assuming a match.
//
// The point: two daemons built from the same commit report the same
// BuildIdentity even when installed at different paths (e.g. the app-bundled
// daemon vs. a launchd-supervised one under ~/.ao/bin). A client can then attach
// to an independently-supervised daemon as long as it is the same build, while a
// genuinely mismatched build is still rejected.
func BuildIdentity() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	var revision, modified string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			modified = s.Value
		}
	}
	return formatBuildIdentity(revision, modified)
}

// formatBuildIdentity is the pure core of BuildIdentity, split out for testing:
// empty revision -> "" (unknown), and a modified working tree gets a "-dirty"
// suffix so a build with uncommitted changes never collides with the clean
// commit it descends from.
func formatBuildIdentity(revision, modified string) string {
	if revision == "" {
		return ""
	}
	if modified == "true" {
		return revision + "-dirty"
	}
	return revision
}
