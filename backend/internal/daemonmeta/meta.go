package daemonmeta

import "runtime/debug"

// ServiceName identifies the AO daemon in loopback health/readiness probes.
// The CLI uses it with the reported PID to avoid signaling an unrelated process
// when a stale run-file's PID has been reused.
const ServiceName = "agent-orchestrator-daemon"

// buildStamp is the build identity injected at link time by the build scripts:
//
//	-ldflags "-X github.com/aoagents/agent-orchestrator/backend/internal/daemonmeta.buildStamp=<rev>"
//
// It exists because Go's own VCS stamping cannot be relied on. Measured: a build
// run inside a LINKED GIT WORKTREE produces no `vcs.revision` at all — and does
// so silently, exiting 0 even under an explicit `-buildvcs=true` — while `git
// rev-parse` in that same directory works fine. Every AO agent session builds
// from a worktree, and the app-bundled daemon shipped with no stamp for exactly
// this reason. A signal that is absent in the default deployment is not a signal.
var buildStamp string

// BuildSource records where a build identity came from. It exists so a consumer
// can tell "this daemon does not know its build" apart from "this daemon told me
// its build", instead of comparing two empty strings and concluding they match.
// An unknown build must never compare equal to anything, including another
// unknown one.
type BuildSource string

const (
	// BuildSourceStamp is a link-time -X stamp from the build scripts. Reliable
	// everywhere, including worktrees.
	BuildSourceStamp BuildSource = "stamp"
	// BuildSourceVCS is Go's own VCS stamping. Present for a plain `go build` in
	// a normal checkout; absent in a linked worktree and under -buildvcs=false.
	BuildSourceVCS BuildSource = "vcs"
	// BuildSourceUnknown means this daemon cannot identify its own build —
	// `go run`, `-buildvcs=false`, or a worktree build with no -X stamp.
	BuildSourceUnknown BuildSource = "unknown"
)

// Build is this daemon's build identity plus how it was derived.
//
// Identity is omitted entirely when Source is BuildSourceUnknown, so a payload
// never carries an empty string that a naive equality check would match against
// another daemon's empty string. Read Source first: only when BOTH sides report
// a non-unknown Source does comparing Identity mean anything.
type Build struct {
	Identity string      `json:"identity,omitempty"`
	Source   BuildSource `json:"source"`
}

// CurrentBuild returns this daemon's build identity. The link-time stamp wins
// over Go's VCS stamping because it is the one that survives a worktree build.
func CurrentBuild() Build {
	if buildStamp != "" {
		return Build{Identity: buildStamp, Source: BuildSourceStamp}
	}
	if id := vcsBuildIdentity(); id != "" {
		return Build{Identity: id, Source: BuildSourceVCS}
	}
	return Build{Source: BuildSourceUnknown}
}

// BuildIdentity returns a stable identifier for the daemon build, or "" when
// this daemon cannot identify its own build. Callers must treat "" as "unknown"
// and fall back to another identity signal (the executable path) rather than
// assuming a match — see CurrentBuild for a form that makes that explicit.
//
// The point: two daemons built from the same commit report the same
// BuildIdentity even when installed at different paths (e.g. the app-bundled
// daemon vs. a launchd-supervised one under ~/.ao/bin). A client can then attach
// to an independently-supervised daemon as long as it is the same build, while a
// genuinely mismatched build is still rejected.
func BuildIdentity() string { return CurrentBuild().Identity }

// vcsBuildIdentity reads Go's VCS stamp: the revision, suffixed "-dirty" when
// the working tree had uncommitted changes.
func vcsBuildIdentity() string {
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
